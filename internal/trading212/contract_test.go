package trading212

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// These tests check the client against testdata/openapi.yaml, a snapshot of
// the official Trading 212 OpenAPI document (refresh with `make update-spec`).

type openAPI struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths      map[string]map[string]operation `yaml:"paths"`
	Components struct {
		Schemas         map[string]*schema `yaml:"schemas"`
		SecuritySchemes map[string]struct {
			Type   string `yaml:"type"`
			Scheme string `yaml:"scheme"`
			In     string `yaml:"in"`
			Name   string `yaml:"name"`
		} `yaml:"securitySchemes"`
	} `yaml:"components"`
}

type operation struct {
	Deprecated bool `yaml:"deprecated"`
	Parameters []struct {
		Name     string  `yaml:"name"`
		In       string  `yaml:"in"`
		Required bool    `yaml:"required"`
		Schema   *schema `yaml:"schema"`
	} `yaml:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema *schema `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *schema `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"responses"`
}

type schema struct {
	Ref                  string             `yaml:"$ref"`
	Type                 any                `yaml:"type"`
	Format               string             `yaml:"format"`
	Enum                 []string           `yaml:"enum"`
	Items                *schema            `yaml:"items"`
	Properties           map[string]*schema `yaml:"properties"`
	AdditionalProperties *schema            `yaml:"additionalProperties"`
	Required             []string           `yaml:"required"`
}

func (s *schema) refName() string { return strings.TrimPrefix(s.Ref, "#/components/schemas/") }

// typeName returns the non-null JSON type ("type" may be a list such as
// [number, null]).
func (s *schema) typeName() string {
	switch t := s.Type.(type) {
	case string:
		return t
	case []any:
		for _, v := range t {
			if str, ok := v.(string); ok && str != "null" {
				return str
			}
		}
	}
	return ""
}

func loadSpec(t *testing.T) *openAPI {
	t.Helper()
	raw, err := os.ReadFile("testdata/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec openAPI
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return &spec
}

// modelForSchema maps every object schema in the spec to its Go model.
var modelForSchema = map[string]reflect.Type{
	"AccountBucketDetailedResponse":            reflect.TypeFor[PieSettings](),
	"AccountBucketInstrumentResult":            reflect.TypeFor[PieInstrument](),
	"AccountBucketInstrumentsDetailedResponse": reflect.TypeFor[PieDetails](),
	"AccountBucketResultResponse":              reflect.TypeFor[Pie](),
	"AccountSummary":                           reflect.TypeFor[AccountSummary](),
	"Cash":                                     reflect.TypeFor[Cash](),
	"DividendDetails":                          reflect.TypeFor[DividendDetails](),
	"DuplicateBucketRequest":                   reflect.TypeFor[DuplicatePieRequest](),
	"EnqueuedReportResponse":                   reflect.TypeFor[EnqueuedReport](),
	"Exchange":                                 reflect.TypeFor[Exchange](),
	"Fill":                                     reflect.TypeFor[Fill](),
	"FillWalletImpact":                         reflect.TypeFor[FillWalletImpact](),
	"HistoricalOrder":                          reflect.TypeFor[HistoricalOrder](),
	"HistoryDividendItem":                      reflect.TypeFor[DividendItem](),
	"HistoryTransactionItem":                   reflect.TypeFor[TransactionItem](),
	"Instrument":                               reflect.TypeFor[Instrument](),
	"InstrumentIssue":                          reflect.TypeFor[InstrumentIssue](),
	"InvestmentResult":                         reflect.TypeFor[InvestmentResult](),
	"Investments":                              reflect.TypeFor[Investments](),
	"LimitRequest":                             reflect.TypeFor[LimitRequest](),
	"MarketRequest":                            reflect.TypeFor[MarketRequest](),
	"Order":                                    reflect.TypeFor[Order](),
	"PaginatedResponseHistoricalOrder":         reflect.TypeFor[Page[HistoricalOrder]](),
	"PaginatedResponseHistoryDividendItem":     reflect.TypeFor[Page[DividendItem]](),
	"PaginatedResponseHistoryTransactionItem":  reflect.TypeFor[Page[TransactionItem]](),
	"PieRequest":                               reflect.TypeFor[PieRequest](),
	"Position":                                 reflect.TypeFor[Position](),
	"PositionWalletImpact":                     reflect.TypeFor[PositionWalletImpact](),
	"PublicReportRequest":                      reflect.TypeFor[ExportRequest](),
	"ReportDataIncluded":                       reflect.TypeFor[ReportDataIncluded](),
	"ReportResponse":                           reflect.TypeFor[Report](),
	"StopLimitRequest":                         reflect.TypeFor[StopLimitRequest](),
	"StopRequest":                              reflect.TypeFor[StopRequest](),
	"Tax":                                      reflect.TypeFor[Tax](),
	"TimeEvent":                                reflect.TypeFor[TimeEvent](),
	"TradableInstrument":                       reflect.TypeFor[TradeableInstrument](),
	"WorkingSchedule":                          reflect.TypeFor[WorkingSchedule](),
}

func jsonFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = f
		}
	}
	return out
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func TestSpecSchemasAreAllModelled(t *testing.T) {
	spec := loadSpec(t)
	for name, s := range spec.Components.Schemas {
		if s.typeName() != "object" {
			continue
		}
		if _, ok := modelForSchema[name]; !ok {
			t.Errorf("spec schema %s has no Go model", name)
		}
	}
	for name := range modelForSchema {
		if _, ok := spec.Components.Schemas[name]; !ok {
			t.Errorf("Go model mapped to %s, which is not in the spec", name)
		}
	}
}

func TestModelsMatchSpec(t *testing.T) {
	spec := loadSpec(t)
	for name, goType := range modelForSchema {
		s := spec.Components.Schemas[name]
		if s == nil {
			continue
		}
		fields := jsonFields(goType)
		for prop, ps := range s.Properties {
			f, ok := fields[prop]
			if !ok {
				t.Errorf("%s.%s is in the spec but missing from %s", name, prop, goType.Name())
				continue
			}
			checkFieldType(t, spec, name+"."+prop, ps, f.Type)
		}
		for field := range fields {
			if _, ok := s.Properties[field]; !ok {
				t.Errorf("%s.%s is not in the spec schema %s", goType.Name(), field, name)
			}
		}
	}
}

func checkFieldType(t *testing.T, spec *openAPI, where string, s *schema, goType reflect.Type) {
	t.Helper()
	gt := deref(goType)
	if s.Ref != "" {
		ref := spec.Components.Schemas[s.refName()]
		if ref.typeName() != "object" {
			checkFieldType(t, spec, where, ref, goType)
			return
		}
		if want := modelForSchema[s.refName()]; gt != want {
			t.Errorf("%s: Go type %s, spec references %s (%v)", where, gt, s.refName(), want)
		}
		return
	}
	ok := true
	switch s.typeName() {
	case "string":
		if s.Format == "date-time" {
			ok = gt == reflect.TypeFor[time.Time]()
		} else {
			ok = gt.Kind() == reflect.String
		}
	case "integer":
		ok = gt.Kind() == reflect.Int64 || (s.Format == "int32" && gt.Kind() == reflect.Int)
	case "number":
		ok = gt.Kind() == reflect.Float64
	case "boolean":
		ok = gt.Kind() == reflect.Bool
	case "array":
		ok = gt.Kind() == reflect.Slice
		if ok {
			checkFieldType(t, spec, where+"[]", s.Items, gt.Elem())
		}
	case "object":
		ok = gt.Kind() == reflect.Map && gt.Key().Kind() == reflect.String
		if ok && s.AdditionalProperties != nil {
			checkFieldType(t, spec, where+"{}", s.AdditionalProperties, gt.Elem())
		}
	default:
		t.Errorf("%s: unhandled spec type %v", where, s.Type)
	}
	if !ok {
		t.Errorf("%s: Go type %s does not match spec type %s (format %q)", where, goType, s.typeName(), s.Format)
	}
}

func TestEnumsMatchSpec(t *testing.T) {
	spec := loadSpec(t)
	prop := func(schemaName, field string) []string {
		t.Helper()
		s := spec.Components.Schemas[schemaName]
		if field != "" {
			s = s.Properties[field]
		}
		if s.Ref != "" {
			s = spec.Components.Schemas[s.refName()]
		}
		return s.Enum
	}
	enums := []struct {
		schema, field string
		goValues      []string
	}{
		{"TimeValidity", "", strs(TimeValidityDay, TimeValidityGoodTillCancel)},
		{"Order", "status", strs(OrderStatusLocal, OrderStatusUnconfirmed, OrderStatusConfirmed, OrderStatusNew,
			OrderStatusCancelling, OrderStatusCancelled, OrderStatusPartiallyFilled, OrderStatusFilled,
			OrderStatusRejected, OrderStatusReplacing, OrderStatusReplaced)},
		{"Order", "strategy", strs(OrderStrategyQuantity, OrderStrategyValue)},
		{"Order", "type", strs(OrderTypeLimit, OrderTypeStop, OrderTypeMarket, OrderTypeStopLimit)},
		{"Order", "side", strs(OrderSideBuy, OrderSideSell)},
		{"Order", "initiatedFrom", strs(InitiatedFromAPI, InitiatedFromIOS, InitiatedFromAndroid, InitiatedFromWeb,
			InitiatedFromSystem, InitiatedFromAutoinvest, InitiatedFromInstrumentAutoinvest)},
		{"TimeEvent", "type", strs(TimeEventOpen, TimeEventClose, TimeEventBreakStart, TimeEventBreakEnd,
			TimeEventPreMarketOpen, TimeEventAfterHoursOpen, TimeEventAfterHoursClose, TimeEventOvernightOpen)},
		{"TradableInstrument", "type", strs(InstrumentTypeCryptocurrency, InstrumentTypeETF, InstrumentTypeForex,
			InstrumentTypeFutures, InstrumentTypeIndex, InstrumentTypeStock, InstrumentTypeWarrant,
			InstrumentTypeCrypto, InstrumentTypeCVR, InstrumentTypeCorpAct)},
		{"PieRequest", "dividendCashAction", strs(DividendCashActionReinvest, DividendCashActionToAccountCash)},
		{"AccountBucketDetailedResponse", "dividendCashAction", strs(DividendCashActionReinvest, DividendCashActionToAccountCash)},
		{"AccountBucketResultResponse", "status", strs(PieStatusAhead, PieStatusOnTrack, PieStatusBehind)},
		{"HistoryTransactionItem", "type", strs(TransactionWithdraw, TransactionDeposit, TransactionFee,
			TransactionTransfer, TransactionInterestOnFreeCash, TransactionLendingInterest)},
		{"ReportResponse", "status", strs(ReportStatusQueued, ReportStatusProcessing, ReportStatusRunning,
			ReportStatusCanceled, ReportStatusFailed, ReportStatusFinished)},
		{"Fill", "tradingMethod", strs(TradingMethodTOTV, TradingMethodOTC)},
		{"InstrumentIssue", "severity", strs(IssueSeverityIrreversible, IssueSeverityReversible, IssueSeverityInformative)},
	}
	for _, e := range enums {
		want := append([]string(nil), prop(e.schema, e.field)...)
		got := append([]string(nil), e.goValues...)
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			t.Errorf("%s.%s enum:\n spec %v\n   go %v", e.schema, e.field, want, got)
		}
	}
}

func strs[T ~string](vs ...T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}

func TestServersAndAuthMatchSpec(t *testing.T) {
	spec := loadSpec(t)
	var hosts []string
	for _, s := range spec.Servers {
		hosts = append(hosts, s.URL)
	}
	for _, env := range []string{"demo", "live"} {
		base := BaseURL(env)
		found := false
		for _, h := range hosts {
			if strings.HasPrefix(base, h+"/") {
				found = true
			}
		}
		if !found {
			t.Errorf("BaseURL(%s)=%s is not under any spec server %v", env, base, hosts)
		}
	}
	for path := range spec.Paths {
		if !strings.HasPrefix(path, "/api/"+defaultAPIVersion+"/") {
			t.Errorf("spec path %s is not under /api/%s", path, defaultAPIVersion)
		}
	}
	schemes := spec.Components.SecuritySchemes
	if s := schemes["authWithSecretKey"]; s.Type != "http" || s.Scheme != "basic" {
		t.Errorf("basic auth scheme changed: %+v", s)
	}
	if s := schemes["legacyApiKeyHeader"]; s.Type != "apiKey" || s.In != "header" || s.Name != "Authorization" {
		t.Errorf("legacy API key scheme changed: %+v", s)
	}
}

var numericSegment = regexp.MustCompile(`/\d+(/|$)`)

// TestEndpointsMatchSpec calls every client method and checks that the set of
// (method, path template) pairs is exactly the set of operations in the spec,
// and that only documented query parameters are sent.
func TestEndpointsMatchSpec(t *testing.T) {
	spec := loadSpec(t)

	type call struct {
		op    string
		query []string
	}
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := numericSegment.ReplaceAllString(r.URL.Path, "/{id}$1")
		var keys []string
		for k := range r.URL.Query() {
			keys = append(keys, k)
		}
		mu.Lock()
		calls = append(calls, call{strings.ToLower(r.Method) + " " + path, keys})
		mu.Unlock()
		switch {
		case strings.Contains(path, "/history/") && r.Method == http.MethodGet && !strings.HasSuffix(path, "/exports"):
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == http.MethodGet && (strings.HasSuffix(path, "/positions") || strings.HasSuffix(path, "/orders") ||
			strings.HasSuffix(path, "/pies") || strings.HasSuffix(path, "/exports") ||
			strings.HasSuffix(path, "/instruments") || strings.HasSuffix(path, "/exchanges")):
			_, _ = w.Write([]byte(`[{"instrument":{"ticker":"X"}}]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("k", "", "demo", WithBaseURL(srv.URL+"/api/v0"))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	name := "p"
	cursor := int64(1)
	steps := []func() error{
		func() error { _, err := c.AccountSummary(ctx); return err },
		func() error { _, err := c.Positions(ctx, "X"); return err },
		func() error { _, err := c.Orders(ctx); return err },
		func() error { _, err := c.Order(ctx, 1); return err },
		func() error { return c.CancelOrder(ctx, 1) },
		func() error { _, err := c.PlaceMarketOrder(ctx, MarketRequest{Ticker: "X", Quantity: 1}); return err },
		func() error {
			_, err := c.PlaceLimitOrder(ctx, LimitRequest{Ticker: "X", Quantity: 1, LimitPrice: 1, TimeValidity: TimeValidityDay})
			return err
		},
		func() error {
			_, err := c.PlaceStopOrder(ctx, StopRequest{Ticker: "X", Quantity: 1, StopPrice: 1, TimeValidity: TimeValidityDay})
			return err
		},
		func() error {
			_, err := c.PlaceStopLimitOrder(ctx, StopLimitRequest{Ticker: "X", Quantity: 1, StopPrice: 1, LimitPrice: 1, TimeValidity: TimeValidityDay})
			return err
		},
		func() error { _, err := c.Pies(ctx); return err },
		func() error { _, err := c.Pie(ctx, 1); return err },
		func() error {
			_, err := c.CreatePie(ctx, PieRequest{Name: &name, InstrumentShares: map[string]float64{"X": 1}})
			return err
		},
		func() error { _, err := c.UpdatePie(ctx, 1, PieRequest{Name: &name}); return err },
		func() error { _, err := c.DuplicatePie(ctx, 1, DuplicatePieRequest{}); return err },
		func() error { return c.DeletePie(ctx, 1) },
		func() error {
			_, err := c.HistoricalOrders(ctx, HistoryQuery{Cursor: &cursor, Ticker: "X", Limit: 5})
			return err
		},
		func() error {
			_, err := c.Dividends(ctx, HistoryQuery{Cursor: &cursor, Ticker: "X", Limit: 5})
			return err
		},
		func() error {
			_, err := c.Transactions(ctx, TransactionQuery{Cursor: "c", Time: "2024-01-01T00:00:00Z", Limit: 5})
			return err
		},
		func() error { _, err := c.Reports(ctx); return err },
		func() error { _, err := c.RequestExport(ctx, ExportRequest{}); return err },
		func() error { _, err := c.Instruments(ctx); return err },
		func() error { _, err := c.Exchanges(ctx); return err },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	documented := map[string]operation{}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			documented[method+" "+path] = op
		}
	}
	used := map[string]bool{}
	for _, c := range calls {
		used[c.op] = true
		op, ok := documented[c.op]
		if !ok {
			t.Errorf("client calls undocumented endpoint %s", c.op)
			continue
		}
		allowed := map[string]bool{}
		for _, p := range op.Parameters {
			if p.In == "query" {
				allowed[p.Name] = true
			}
		}
		for _, q := range c.query {
			if !allowed[q] {
				t.Errorf("%s sends undocumented query parameter %q", c.op, q)
			}
		}
	}
	for op := range documented {
		if !used[op] {
			t.Errorf("spec operation %s is not implemented by the client", op)
		}
	}
}
