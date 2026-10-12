package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

type apiCall struct {
	Method, Path, Query, Body string
}

type fakeAPI struct {
	mu    sync.Mutex
	calls []apiCall
}

func (f *fakeAPI) recorded() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

func (f *fakeAPI) last(t *testing.T) apiCall {
	t.Helper()
	c := f.recorded()
	if len(c) == 0 {
		t.Fatal("no upstream calls recorded")
	}
	return c[len(c)-1]
}

// routes maps "METHOD /path" (relative to /api/v0) to a canned JSON response.
var routes = map[string]string{
	"GET /equity/account/summary":      `{"id":1,"currency":"GBP","totalValue":1234.5,"cash":{"availableToTrade":100,"inPies":10,"reservedForOrders":0}}`,
	"GET /equity/positions":            `[{"quantity":3,"currentPrice":190.1,"instrument":{"ticker":"AAPL_US_EQ","name":"Apple"}}]`,
	"GET /equity/orders":               `[{"id":11,"ticker":"AAPL_US_EQ","type":"LIMIT","status":"NEW"}]`,
	"GET /equity/orders/11":            `{"id":11,"ticker":"AAPL_US_EQ","type":"LIMIT","status":"NEW"}`,
	"DELETE /equity/orders/11":         ``,
	"POST /equity/orders/market":       `{"id":12,"type":"MARKET","status":"NEW"}`,
	"POST /equity/orders/limit":        `{"id":13,"type":"LIMIT","status":"NEW"}`,
	"POST /equity/orders/stop":         `{"id":14,"type":"STOP","status":"NEW"}`,
	"POST /equity/orders/stop_limit":   `{"id":15,"type":"STOP_LIMIT","status":"NEW"}`,
	"GET /equity/pies":                 `[{"id":5,"cash":1,"status":"AHEAD"}]`,
	"GET /equity/pies/5":               `{"settings":{"id":5,"name":"Tech"},"instruments":[{"ticker":"AAPL_US_EQ"}]}`,
	"POST /equity/pies":                `{"settings":{"id":6,"name":"New"}}`,
	"POST /equity/pies/5":              `{"settings":{"id":5,"name":"Renamed"}}`,
	"POST /equity/pies/5/duplicate":    `{"settings":{"id":7,"name":"Copy"}}`,
	"DELETE /equity/pies/5":            ``,
	"GET /equity/history/orders":       `{"items":[{"order":{"id":1},"fill":{"id":2,"price":10}}],"nextPagePath":"/api/v0/equity/history/orders?cursor=1"}`,
	"GET /equity/history/dividends":    `{"items":[{"amount":1.2,"ticker":"AAPL_US_EQ"}]}`,
	"GET /equity/history/transactions": `{"items":[{"amount":100,"type":"DEPOSIT"}]}`,
	"GET /equity/history/exports":      `[{"reportId":9,"status":"Finished","downloadLink":"https://example.test/r.csv"}]`,
	"POST /equity/history/exports":     `{"reportId":10}`,
	"GET /equity/metadata/instruments": `[{"ticker":"AAPL_US_EQ","name":"Apple Inc"},{"ticker":"MSFT_US_EQ","name":"Microsoft"},{"ticker":"VOD_LON_EQ","shortName":"VOD","name":"Vodafone"}]`,
	"GET /equity/metadata/exchanges":   `[{"id":1,"name":"NASDAQ","workingSchedules":[]},{"id":2,"name":"London Stock Exchange","workingSchedules":[]}]`,
}

type harness struct {
	api     *fakeAPI
	session *mcp.ClientSession
	status  atomic.Int32
}

func newHarness(t *testing.T, opts ...t212.Option) *harness {
	t.Helper()
	h := &harness{api: &fakeAPI{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v0")
		h.api.mu.Lock()
		h.api.calls = append(h.api.calls, apiCall{r.Method, path, r.URL.RawQuery, string(body)})
		h.api.mu.Unlock()
		if status := h.status.Load(); status != 0 {
			w.WriteHeader(int(status))
			return
		}
		resp, ok := routes[r.Method+" "+path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)

	client, err := t212.NewClient("test-api-key-5f3c", "", "demo", append([]t212.Option{t212.WithBaseURL(srv.URL + "/api/v0")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := New(client, Options{Version: "test"}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	h.session = cs
	return h
}

func (h *harness) call(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func (h *harness) callOK(t *testing.T, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res := h.call(t, name, args)
	if res.IsError {
		t.Fatalf("%s returned error: %s", name, text(res))
	}
	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s output: %v (%s)", name, err, raw)
		}
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestListToolsAndAnnotations(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mutating := map[string]bool{
		"cancel_order": true, "place_market_order": true, "place_limit_order": true, "place_stop_order": true,
		"place_stop_limit_order": true, "create_pie": true, "update_pie": true, "duplicate_pie": true,
		"delete_pie": true, "request_csv_export": true,
	}
	want := []string{
		"cancel_order", "create_pie", "delete_pie", "duplicate_pie", "fetch_a_pie", "fetch_account_cash",
		"fetch_account_info", "fetch_account_summary", "fetch_all_open_positions", "fetch_all_orders",
		"fetch_exports_list", "fetch_historical_order_data", "fetch_open_position_by_ticker", "fetch_order",
		"fetch_paid_out_dividends", "fetch_pies", "fetch_position_by_ticker", "fetch_positions",
		"fetch_transaction_list", "place_limit_order", "place_market_order", "place_stop_limit_order",
		"place_stop_order", "request_csv_export", "search_exchange", "search_instrument",
		"search_specific_position_by_ticker", "update_pie",
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s has no annotations", tool.Name)
			continue
		}
		if a.ReadOnlyHint == mutating[tool.Name] || a.DestructiveHint == nil || *a.DestructiveHint != mutating[tool.Name] {
			t.Errorf("%s: readOnly=%v destructive=%v", tool.Name, a.ReadOnlyHint, a.DestructiveHint)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no output schema", tool.Name)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tools:\n got %v\nwant %v", got, want)
	}
}

func TestAccountTools(t *testing.T) {
	h := newHarness(t)
	var summary t212.AccountSummary
	for _, name := range []string{"fetch_account_info", "fetch_account_summary"} {
		h.callOK(t, name, nil, &summary)
		if *summary.Currency != "GBP" || *summary.TotalValue != 1234.5 {
			t.Errorf("%s: %+v", name, summary)
		}
	}
	var cash t212.Cash
	h.callOK(t, "fetch_account_cash", nil, &cash)
	if *cash.AvailableToTrade != 100 {
		t.Errorf("cash: %+v", cash)
	}

	var positions PositionsResult
	h.callOK(t, "fetch_positions", map[string]any{"ticker": "AAPL_US_EQ"}, &positions)
	if len(positions.Positions) != 1 || h.api.last(t).Query != "ticker=AAPL_US_EQ" {
		t.Errorf("positions: %+v, query %q", positions, h.api.last(t).Query)
	}
	h.callOK(t, "fetch_all_open_positions", nil, &positions)
	if h.api.last(t).Query != "" {
		t.Errorf("unfiltered positions sent query %q", h.api.last(t).Query)
	}
	for _, name := range []string{"fetch_position_by_ticker", "fetch_open_position_by_ticker", "search_specific_position_by_ticker"} {
		var p t212.Position
		h.callOK(t, name, map[string]any{"ticker": "AAPL_US_EQ"}, &p)
		if *p.Instrument.Ticker != "AAPL_US_EQ" {
			t.Errorf("%s: %+v", name, p)
		}
	}
	if res := h.call(t, "fetch_position_by_ticker", map[string]any{}); !res.IsError {
		t.Error("missing ticker should fail input validation")
	}
}

func TestOrderTools(t *testing.T) {
	h := newHarness(t)
	var orders OrdersResult
	h.callOK(t, "fetch_all_orders", nil, &orders)
	if len(orders.Orders) != 1 || *orders.Orders[0].ID != 11 {
		t.Errorf("orders: %+v", orders)
	}
	var order t212.Order
	h.callOK(t, "fetch_order", map[string]any{"order_id": 11}, &order)
	if *order.ID != 11 {
		t.Errorf("order: %+v", order)
	}

	var action ActionResult
	h.callOK(t, "cancel_order", map[string]any{"order_id": 11}, &action)
	if !action.OK || h.api.last(t).Method != http.MethodDelete || h.api.last(t).Path != "/equity/orders/11" {
		t.Errorf("cancel: %+v %+v", action, h.api.last(t))
	}

	tests := []struct {
		tool string
		args map[string]any
		path string
		body string
	}{
		{"place_market_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 2},
			"/equity/orders/market", `{"extendedHours":false,"quantity":2,"ticker":"AAPL_US_EQ"}`},
		{"place_market_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": -1, "extended_hours": true},
			"/equity/orders/market", `{"extendedHours":true,"quantity":-1,"ticker":"AAPL_US_EQ"}`},
		{"place_limit_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 1, "limit_price": 150},
			"/equity/orders/limit", `{"limitPrice":150,"quantity":1,"ticker":"AAPL_US_EQ","timeValidity":"DAY"}`},
		{"place_stop_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 1, "stop_price": 120, "time_validity": "GOOD_TILL_CANCEL"},
			"/equity/orders/stop", `{"quantity":1,"stopPrice":120,"ticker":"AAPL_US_EQ","timeValidity":"GOOD_TILL_CANCEL"}`},
		{"place_stop_limit_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 1, "stop_price": 120, "limit_price": 119},
			"/equity/orders/stop_limit", `{"limitPrice":119,"quantity":1,"stopPrice":120,"ticker":"AAPL_US_EQ","timeValidity":"DAY"}`},
	}
	for _, tc := range tests {
		h.callOK(t, tc.tool, tc.args, &order)
		last := h.api.last(t)
		if last.Method != http.MethodPost || last.Path != tc.path || strings.TrimSpace(last.Body) != tc.body {
			t.Errorf("%s: got %s %s %s, want %s %s", tc.tool, last.Method, last.Path, last.Body, tc.path, tc.body)
		}
	}

	before := len(h.api.recorded())
	invalid := []struct {
		tool string
		args map[string]any
	}{
		{"place_market_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 0}},
		{"place_limit_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 1, "limit_price": -5}},
		{"place_limit_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 1, "limit_price": 5, "time_validity": "WEEK"}},
		{"place_stop_order", map[string]any{"ticker": "", "quantity": 1, "stop_price": 1}},
	}
	for _, tc := range invalid {
		if res := h.call(t, tc.tool, tc.args); !res.IsError {
			t.Errorf("%s %v: expected error result", tc.tool, tc.args)
		}
	}
	if after := len(h.api.recorded()); after != before {
		t.Errorf("invalid orders reached the API (%d calls)", after-before)
	}
}

func TestPieTools(t *testing.T) {
	h := newHarness(t)
	var pies PiesResult
	h.callOK(t, "fetch_pies", nil, &pies)
	if len(pies.Pies) != 1 || *pies.Pies[0].ID != 5 {
		t.Errorf("pies: %+v", pies)
	}
	var pie t212.PieDetails
	h.callOK(t, "fetch_a_pie", map[string]any{"pie_id": 5}, &pie)
	if *pie.Settings.Name != "Tech" {
		t.Errorf("pie: %+v", pie.Settings)
	}

	h.callOK(t, "create_pie", map[string]any{
		"name":                 "New",
		"instrument_shares":    map[string]any{"AAPL_US_EQ": 0.6, "MSFT_US_EQ": 0.4},
		"dividend_cash_action": "TO_ACCOUNT_CASH",
		"end_date":             "2030-12-31T23:59:59Z",
		"goal":                 5000,
	}, &pie)
	var body map[string]any
	if err := json.Unmarshal([]byte(h.api.last(t).Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "New" || body["dividendCashAction"] != "TO_ACCOUNT_CASH" || body["goal"] != 5000.0 ||
		body["endDate"] != "2030-12-31T23:59:59Z" || body["instrumentShares"].(map[string]any)["AAPL_US_EQ"] != 0.6 {
		t.Errorf("create body: %v", body)
	}
	if _, ok := body["icon"]; ok {
		t.Error("unset icon should be omitted")
	}

	h.callOK(t, "update_pie", map[string]any{"pie_id": 5, "name": "Renamed"}, &pie)
	if last := h.api.last(t); last.Path != "/equity/pies/5" || strings.TrimSpace(last.Body) != `{"name":"Renamed"}` {
		t.Errorf("update: %+v", last)
	}
	h.callOK(t, "duplicate_pie", map[string]any{"pie_id": 5, "name": "Copy"}, &pie)
	if *pie.Settings.ID != 7 {
		t.Errorf("duplicate: %+v", pie.Settings)
	}
	var action ActionResult
	h.callOK(t, "delete_pie", map[string]any{"pie_id": 5}, &action)
	if !action.OK || h.api.last(t).Method != http.MethodDelete {
		t.Errorf("delete: %+v", action)
	}

	if res := h.call(t, "create_pie", map[string]any{"name": "Bad", "instrument_shares": map[string]any{"X": 1}, "end_date": "next year"}); !res.IsError {
		t.Error("invalid end_date should fail")
	}
	if res := h.call(t, "create_pie", map[string]any{"name": "Bad", "instrument_shares": map[string]any{"X": 1}, "dividend_cash_action": "SPEND"}); !res.IsError {
		t.Error("invalid dividend_cash_action should fail")
	}
}

func TestHistoryTools(t *testing.T) {
	h := newHarness(t)
	var orders t212.Page[t212.HistoricalOrder]
	h.callOK(t, "fetch_historical_order_data", map[string]any{"cursor": 123, "ticker": "AAPL_US_EQ"}, &orders)
	if len(orders.Items) != 1 || orders.NextPagePath == nil {
		t.Errorf("orders: %+v", orders)
	}
	if q := h.api.last(t).Query; q != "cursor=123&limit=20&ticker=AAPL_US_EQ" {
		t.Errorf("history query: %s", q)
	}

	var dividends t212.Page[t212.DividendItem]
	h.callOK(t, "fetch_paid_out_dividends", map[string]any{"limit": 5}, &dividends)
	if len(dividends.Items) != 1 || h.api.last(t).Query != "limit=5" {
		t.Errorf("dividends: %+v %s", dividends, h.api.last(t).Query)
	}

	h.callOK(t, "fetch_historical_order_data", map[string]any{"next_page_path": "/api/v0/equity/history/orders?limit=2&cursor=7"}, &orders)
	if q := h.api.last(t).Query; q != "cursor=7&limit=2" {
		t.Errorf("next page query: %s", q)
	}
	if res := h.call(t, "fetch_paid_out_dividends", map[string]any{"next_page_path": "https://evil.example/x"}); !res.IsError {
		t.Error("foreign next_page_path should be rejected")
	}

	var tx t212.Page[t212.TransactionItem]
	h.callOK(t, "fetch_transaction_list", map[string]any{"cursor": "abc", "time": "2024-01-01T00:00:00Z"}, &tx)
	if len(tx.Items) != 1 || *tx.Items[0].Type != t212.TransactionDeposit {
		t.Errorf("transactions: %+v", tx)
	}

	var reports ReportsResult
	h.callOK(t, "fetch_exports_list", nil, &reports)
	if len(reports.Reports) != 1 || *reports.Reports[0].Status != t212.ReportStatusFinished {
		t.Errorf("reports: %+v", reports)
	}

	var enq t212.EnqueuedReport
	h.callOK(t, "request_csv_export", map[string]any{"include_interest": false, "time_from": "2024-01-01T00:00:00Z"}, &enq)
	if enq.ReportID != 10 {
		t.Errorf("export: %+v", enq)
	}
	want := `{"dataIncluded":{"includeDividends":true,"includeInterest":false,"includeOrders":true,"includeTransactions":true},"timeFrom":"2024-01-01T00:00:00Z"}`
	if got := strings.TrimSpace(h.api.last(t).Body); got != want {
		t.Errorf("export body:\n got %s\nwant %s", got, want)
	}

	if res := h.call(t, "request_csv_export", map[string]any{"time_from": "2024-02-01T00:00:00Z", "time_to": "2024-01-01T00:00:00Z"}); !res.IsError {
		t.Error("reversed export range should fail")
	}
}

func TestMetadataTools(t *testing.T) {
	h := newHarness(t)
	var inst InstrumentsResult
	h.callOK(t, "search_instrument", nil, &inst)
	if len(inst.Instruments) != 3 {
		t.Errorf("all instruments: %d", len(inst.Instruments))
	}
	h.callOK(t, "search_instrument", map[string]any{"search_term": "micro"}, &inst)
	if len(inst.Instruments) != 1 || *inst.Instruments[0].Ticker != "MSFT_US_EQ" {
		t.Errorf("filtered: %+v", inst)
	}
	h.callOK(t, "search_instrument", map[string]any{"search_term": "nothing-matches"}, &inst)
	if inst.Instruments == nil || len(inst.Instruments) != 0 {
		t.Errorf("no match should be an empty list: %+v", inst)
	}

	var ex ExchangesResult
	h.callOK(t, "search_exchange", map[string]any{"search_term": "london"}, &ex)
	if len(ex.Exchanges) != 1 || ex.Exchanges[0].ID != 2 {
		t.Errorf("by name: %+v", ex)
	}
	h.callOK(t, "search_exchange", map[string]any{"search_term": "1"}, &ex)
	if len(ex.Exchanges) != 1 || ex.Exchanges[0].Name != "NASDAQ" {
		t.Errorf("by id: %+v", ex)
	}
}

func TestToolErrorsAreSafeResults(t *testing.T) {
	h := newHarness(t)
	h.status.Store(http.StatusUnauthorized)
	res := h.call(t, "fetch_account_summary", nil)
	if !res.IsError || !strings.Contains(text(res), "rejected the API credentials") {
		t.Fatalf("expected auth error result, got %+v", res)
	}
	if strings.Contains(text(res), "test-api-key-5f3c") {
		t.Errorf("error leaks credentials: %s", text(res))
	}
}

func TestCacheMetadataIsAttached(t *testing.T) {
	h := newHarness(t, t212.WithCacheTTLs(t212.TTLs{Account: time.Minute}))
	first := h.callOK(t, "fetch_all_orders", nil, nil)
	second := h.callOK(t, "fetch_all_orders", nil, nil)
	decode := func(res *mcp.CallToolResult) []t212.CacheObservation {
		raw, _ := json.Marshal(res.Meta[CacheMetaKey])
		var obs []t212.CacheObservation
		if err := json.Unmarshal(raw, &obs); err != nil {
			t.Fatalf("decode meta: %v", err)
		}
		return obs
	}
	if o := decode(first); len(o) != 1 || o[0].CacheHit || o[0].TTLSeconds != 60 {
		t.Errorf("first: %+v", o)
	}
	if o := decode(second); len(o) != 1 || !o[0].CacheHit {
		t.Errorf("second: %+v", o)
	}
	if n := len(h.api.recorded()); n != 1 {
		t.Errorf("expected one upstream call, got %d", n)
	}
}

func TestResources(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	list, err := h.session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Resources) != 11 {
		t.Errorf("resources: %d", len(list.Resources))
	}
	templates, err := h.session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates.ResourceTemplates) != 5 {
		t.Errorf("templates: %d", len(templates.ResourceTemplates))
	}

	read := func(uri string) string {
		t.Helper()
		res, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("ReadResource(%s): %v", uri, err)
		}
		if len(res.Contents) != 1 || res.Contents[0].MIMEType != jsonMIME {
			t.Fatalf("ReadResource(%s): %+v", uri, res.Contents)
		}
		return res.Contents[0].Text
	}
	checks := map[string]string{
		"trading212://account/info":                 `"currency":"GBP"`,
		"trading212://account/summary":              `"totalValue":1234.5`,
		"trading212://account/cash":                 `"availableToTrade":100`,
		"trading212://positions":                    `"AAPL_US_EQ"`,
		"trading212://account/positions":            `"AAPL_US_EQ"`,
		"trading212://account/portfolio":            `"AAPL_US_EQ"`,
		"trading212://orders":                       `"id":11`,
		"trading212://pies":                         `"id":5`,
		"trading212://instruments":                  `"MSFT_US_EQ"`,
		"trading212://exchanges":                    `"NASDAQ"`,
		"trading212://history/exports":              `"reportId":9`,
		"trading212://positions/AAPL_US_EQ":         `"quantity":3`,
		"trading212://account/positions/AAPL_US_EQ": `"quantity":3`,
		"trading212://account/portfolio/AAPL_US_EQ": `"quantity":3`,
		"trading212://orders/11":                    `"id":11`,
		"trading212://pies/5":                       `"name":"Tech"`,
	}
	for uri, want := range checks {
		if got := read(uri); !strings.Contains(got, want) {
			t.Errorf("%s: %s does not contain %s", uri, got, want)
		}
	}

	if _, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "trading212://orders/abc"}); err == nil {
		t.Error("non-numeric order id should fail")
	}
}

func TestPrompt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	list, err := h.session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Prompts) != 1 || list.Prompts[0].Name != "analyse_trading212_data" {
		t.Fatalf("prompts: %+v", list.Prompts)
	}
	res, err := h.session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "analyse_trading212_data"})
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "GBX represents pence") || !strings.Contains(msg, "Currency: GBP") {
		t.Errorf("prompt: %s", msg)
	}

	h.status.Store(http.StatusUnauthorized)
	res, err = h.session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "analyse_trading212_data"})
	if err != nil {
		t.Fatalf("prompt should degrade gracefully: %v", err)
	}
	if msg := res.Messages[0].Content.(*mcp.TextContent).Text; strings.Contains(msg, "Currency:") {
		t.Errorf("prompt without account access should omit currency: %s", msg)
	}
}
