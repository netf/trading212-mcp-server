package trading212

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAPIKey = "test-api-key-5f3c"

type recorded struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   string
}

// fakeAPI is an httptest server whose responses are scripted per path.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	handler  http.HandlerFunc
}

func newFakeAPI(t *testing.T, h http.HandlerFunc) (*fakeAPI, *httptest.Server) {
	t.Helper()
	f := &fakeAPI{t: t, handler: h}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(body)})
		f.mu.Unlock()
		f.handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAPI) calls() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestClient(t *testing.T, srv *httptest.Server, clock *fakeClock, opts ...Option) *Client {
	t.Helper()
	if clock == nil {
		clock = &fakeClock{now: time.Unix(1_700_000_000, 0)}
	}
	all := append([]Option{WithBaseURL(srv.URL + "/api/v0"), WithClock(clock.Now, clock.Sleep)}, opts...)
	c, err := NewClient(testAPIKey, "", "demo", all...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient("", "", "demo"); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty key: got %v", err)
	}
	if _, err := NewClient("k", "", "prod"); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("bad env: got %v", err)
	}
	if got := BaseURL("live"); got != "https://live.trading212.com/api/v0" {
		t.Errorf("BaseURL: %s", got)
	}
}

func TestAuthorizationHeader(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"id": 1})
	})
	ctx := context.Background()

	c := newTestClient(t, srv, nil)
	if _, err := c.AccountSummary(ctx); err != nil {
		t.Fatal(err)
	}
	withSecret, err := NewClient("key", "secret", "live", WithBaseURL(srv.URL+"/api/v0"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withSecret.AccountSummary(ctx); err != nil {
		t.Fatal(err)
	}

	calls := f.calls()
	if calls[0].Auth != testAPIKey {
		t.Errorf("key-only auth: got %q", calls[0].Auth)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("key:secret"))
	if calls[1].Auth != want {
		t.Errorf("basic auth: got %q, want %q", calls[1].Auth, want)
	}
	if calls[0].Path != "/api/v0/equity/account/summary" {
		t.Errorf("path: %s", calls[0].Path)
	}
}

func TestAccountSummaryDecoding(t *testing.T) {
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":42,"currency":"GBP","totalValue":1000.5,
			"cash":{"availableToTrade":100,"inPies":5,"reservedForOrders":1},
			"investments":{"currentValue":900},"someFutureField":true}`)
	})
	c := newTestClient(t, srv, nil)
	s, err := c.AccountSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *s.ID != 42 || *s.Currency != "GBP" || *s.TotalValue != 1000.5 || *s.Cash.AvailableToTrade != 100 {
		t.Errorf("unexpected summary: %+v", s)
	}
	cash, err := c.AccountCash(context.Background())
	if err != nil || *cash.InPies != 5 {
		t.Errorf("cash: %+v %v", cash, err)
	}
}

func TestPositionsQueryAndLookup(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticker") == "NONE" {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_, _ = io.WriteString(w, `[{"quantity":2,"createdAt":"2024-01-02T03:04:05Z","instrument":{"ticker":"AAPL_US_EQ"}}]`)
	})
	c := newTestClient(t, srv, nil)
	ctx := context.Background()

	p, err := c.PositionByTicker(ctx, "AAPL_US_EQ")
	if err != nil {
		t.Fatal(err)
	}
	if *p.Quantity != 2 || p.CreatedAt.Year() != 2024 {
		t.Errorf("position: %+v", p)
	}
	if q := f.calls()[0].Query; q != "ticker=AAPL_US_EQ" {
		t.Errorf("query: %s", q)
	}
	if _, err := c.PositionByTicker(ctx, "NONE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing position: got %v", err)
	}
	if _, err := c.PositionByTicker(ctx, " "); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("blank ticker: got %v", err)
	}
}

func TestHistoryQueryParams(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":null,"nextPagePath":"/api/v0/equity/history/orders?cursor=5"}`)
	})
	c := newTestClient(t, srv, nil)
	ctx := context.Background()
	cursor := int64(99)

	page, err := c.HistoricalOrders(ctx, HistoryQuery{Cursor: &cursor, Ticker: "TSLA_US_EQ", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || *page.NextPagePath == "" {
		t.Errorf("page: %+v", page)
	}
	if _, err := c.Dividends(ctx, HistoryQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Transactions(ctx, TransactionQuery{Cursor: "abc", Time: "2024-01-01T00:00:00Z", Limit: 5}); err != nil {
		t.Fatal(err)
	}

	calls := f.calls()
	wants := []struct{ path, query string }{
		{"/api/v0/equity/history/orders", "cursor=99&limit=50&ticker=TSLA_US_EQ"},
		{"/api/v0/equity/history/dividends", "limit=20"},
		{"/api/v0/equity/history/transactions", "cursor=abc&limit=5&time=2024-01-01T00%3A00%3A00Z"},
	}
	for i, w := range wants {
		if calls[i].Path != w.path || calls[i].Query != w.query {
			t.Errorf("call %d: got %s?%s, want %s?%s", i, calls[i].Path, calls[i].Query, w.path, w.query)
		}
	}
}

func TestPlaceOrdersSendTypedBodies(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"id": 7, "status": "NEW", "type": "LIMIT"})
	})
	c := newTestClient(t, srv, nil)
	ctx := context.Background()

	if _, err := c.PlaceMarketOrder(ctx, MarketRequest{Ticker: "AAPL_US_EQ", Quantity: -1.5}); err != nil {
		t.Fatal(err)
	}
	o, err := c.PlaceLimitOrder(ctx, LimitRequest{Ticker: "AAPL_US_EQ", Quantity: 1, LimitPrice: 100, TimeValidity: TimeValidityDay})
	if err != nil {
		t.Fatal(err)
	}
	if *o.ID != 7 || *o.Status != OrderStatusNew {
		t.Errorf("order: %+v", o)
	}
	if _, err := c.PlaceStopOrder(ctx, StopRequest{Ticker: "X", Quantity: 1, StopPrice: 5, TimeValidity: TimeValidityGoodTillCancel}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PlaceStopLimitOrder(ctx, StopLimitRequest{Ticker: "X", Quantity: 1, StopPrice: 5, LimitPrice: 6, TimeValidity: TimeValidityDay}); err != nil {
		t.Fatal(err)
	}

	calls := f.calls()
	wants := []struct{ path, body string }{
		{"/api/v0/equity/orders/market", `{"extendedHours":false,"quantity":-1.5,"ticker":"AAPL_US_EQ"}`},
		{"/api/v0/equity/orders/limit", `{"limitPrice":100,"quantity":1,"ticker":"AAPL_US_EQ","timeValidity":"DAY"}`},
		{"/api/v0/equity/orders/stop", `{"quantity":1,"stopPrice":5,"ticker":"X","timeValidity":"GOOD_TILL_CANCEL"}`},
		{"/api/v0/equity/orders/stop_limit", `{"limitPrice":6,"quantity":1,"stopPrice":5,"ticker":"X","timeValidity":"DAY"}`},
	}
	for i, w := range wants {
		if calls[i].Method != http.MethodPost || calls[i].Path != w.path || strings.TrimSpace(calls[i].Body) != w.body {
			t.Errorf("call %d: got %s %s %s, want POST %s %s", i, calls[i].Method, calls[i].Path, calls[i].Body, w.path, w.body)
		}
	}
}

func TestOrderValidationNeverHitsNetwork(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{}) })
	c := newTestClient(t, srv, nil)
	ctx := context.Background()

	cases := map[string]func() error{
		"zero quantity": func() error { _, err := c.PlaceMarketOrder(ctx, MarketRequest{Ticker: "X"}); return err },
		"empty ticker":  func() error { _, err := c.PlaceMarketOrder(ctx, MarketRequest{Quantity: 1}); return err },
		"negative limit": func() error {
			_, err := c.PlaceLimitOrder(ctx, LimitRequest{Ticker: "X", Quantity: 1, LimitPrice: -1, TimeValidity: TimeValidityDay})
			return err
		},
		"bad validity": func() error {
			_, err := c.PlaceStopOrder(ctx, StopRequest{Ticker: "X", Quantity: 1, StopPrice: 1, TimeValidity: "FOREVER"})
			return err
		},
		"pie without name": func() error {
			_, err := c.CreatePie(ctx, PieRequest{InstrumentShares: map[string]float64{"X": 1}})
			return err
		},
		"pie bad weight": func() error {
			name := "p"
			_, err := c.CreatePie(ctx, PieRequest{Name: &name, InstrumentShares: map[string]float64{"X": 0}})
			return err
		},
		"export reversed": func() error {
			from, to := time.Now(), time.Now().Add(-time.Hour)
			_, err := c.RequestExport(ctx, ExportRequest{TimeFrom: &from, TimeTo: &to})
			return err
		},
	}
	for name, fn := range cases {
		if err := fn(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: got %v, want ErrInvalidRequest", name, err)
		}
	}
	if n := len(f.calls()); n != 0 {
		t.Errorf("validation failures sent %d requests", n)
	}
}

func TestPieEndpoints(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, `{"instruments":[{"ticker":"AAPL_US_EQ","expectedShare":0.5}],
			"settings":{"id":3,"name":"Tech","dividendCashAction":"REINVEST","instrumentShares":{"AAPL_US_EQ":0.5}}}`)
	})
	c := newTestClient(t, srv, nil)
	ctx := context.Background()
	name := "Tech"

	pie, err := c.CreatePie(ctx, PieRequest{Name: &name, InstrumentShares: map[string]float64{"AAPL_US_EQ": 0.5, "MSFT_US_EQ": 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	if *pie.Settings.ID != 3 || pie.Settings.InstrumentShares["AAPL_US_EQ"] != 0.5 {
		t.Errorf("pie: %+v", pie.Settings)
	}
	if _, err := c.UpdatePie(ctx, 3, PieRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DuplicatePie(ctx, 3, DuplicatePieRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pie(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePie(ctx, 3); err != nil {
		t.Fatal(err)
	}

	calls := f.calls()
	wants := []string{
		"POST /api/v0/equity/pies",
		"POST /api/v0/equity/pies/3",
		"POST /api/v0/equity/pies/3/duplicate",
		"GET /api/v0/equity/pies/3",
		"DELETE /api/v0/equity/pies/3",
	}
	for i, w := range wants {
		if got := calls[i].Method + " " + calls[i].Path; got != w {
			t.Errorf("call %d: got %s, want %s", i, got, w)
		}
	}
	if b := strings.TrimSpace(calls[1].Body); b != `{"name":"Tech"}` {
		t.Errorf("update body should only contain set fields: %s", b)
	}
	if b := strings.TrimSpace(calls[2].Body); b != `{}` {
		t.Errorf("duplicate body: %s", b)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   error
		code   string
	}{
		{401, ``, ErrAuthentication, ""},
		{403, `{"code":"Forbidden"}`, ErrPermission, "Forbidden"},
		{408, ``, ErrTimeout, ""},
		{404, `{}`, ErrNotFound, ""},
		{400, `{"code":"InsufficientFreeForStocksBuy","clarification":"secret details"}`, ErrInvalidRequest, "InsufficientFreeForStocksBuy"},
		{400, `{"code":"has spaces <script>"}`, ErrInvalidRequest, ""},
	}
	for _, tc := range tests {
		_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		c := newTestClient(t, srv, nil)
		_, err := c.PlaceMarketOrder(context.Background(), MarketRequest{Ticker: "X", Quantity: 1})
		if !errors.Is(err, tc.want) {
			t.Errorf("HTTP %d: got %v, want %v", tc.status, err, tc.want)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || apiErr.Code != tc.code {
			t.Errorf("HTTP %d: APIError = %+v", tc.status, apiErr)
		}
		if strings.Contains(err.Error(), "secret details") || strings.Contains(err.Error(), testAPIKey) {
			t.Errorf("error leaks upstream body or credentials: %v", err)
		}
	}
}

func TestInvalidJSON(t *testing.T) {
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{not json`) })
	c := newTestClient(t, srv, nil)
	if _, err := c.Orders(context.Background()); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v", err)
	}
}

func TestWrongShape(t *testing.T) {
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"items":"nope"}`) })
	c := newTestClient(t, srv, nil)
	if _, err := c.Dividends(context.Background(), HistoryQuery{}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v", err)
	}
}

func TestGetRetriesRateLimitWithRetryAfter(t *testing.T) {
	var n int
	var mu sync.Mutex
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		attempt := n
		mu.Unlock()
		switch attempt {
		case 1:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	})
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	c := newTestClient(t, srv, clock)
	if _, err := c.Orders(context.Background()); err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if got := len(f.calls()); got != 3 {
		t.Errorf("attempts: %d", got)
	}
	if want := []time.Duration{3 * time.Second, 2 * time.Second}; len(clock.sleeps) != 2 ||
		clock.sleeps[0] != want[0] || clock.sleeps[1] != want[1] {
		t.Errorf("sleeps: got %v, want %v", clock.sleeps, want)
	}
}

func TestRateLimitResetHeader(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := http.Header{}
	h.Set("x-ratelimit-reset", "1700000005")
	if got := retryAfter(h, now); got != 5*time.Second {
		t.Errorf("reset header: %v", got)
	}
	h = http.Header{}
	h.Set("Retry-After", now.Add(7*time.Second).UTC().Format(http.TimeFormat))
	if got := retryAfter(h, now); got != 7*time.Second {
		t.Errorf("http date: %v", got)
	}
}

func TestGetGivesUpAfterMaxAttempts(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := newTestClient(t, srv, nil)
	_, err := c.Pies(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
	if got := len(f.calls()); got != maxAttempts {
		t.Errorf("attempts: %d", got)
	}
}

func TestRetryBeyondBudgetFailsFast(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	clock := &fakeClock{now: time.Now()}
	c := newTestClient(t, srv, clock)
	_, err := c.Pies(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 120*time.Second {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "retry after 2m0s") {
		t.Errorf("message should advise wait: %v", err)
	}
	if len(f.calls()) != 1 || len(clock.sleeps) != 0 {
		t.Errorf("should not retry past budget: calls=%d sleeps=%v", len(f.calls()), clock.sleeps)
	}
}

func TestMutationsAreNeverRetried(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := newTestClient(t, srv, nil)
	err := c.CancelOrder(context.Background(), 1)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "outcome is unknown") {
		t.Fatalf("got %v", err)
	}
	if got := len(f.calls()); got != 1 {
		t.Errorf("attempts: %d", got)
	}
}

func TestMutationConnectionFailureWarnsAboutUnknownOutcome(t *testing.T) {
	_, srv := newFakeAPI(t, func(http.ResponseWriter, *http.Request) {})
	c := newTestClient(t, srv, nil)
	srv.Close()
	err := c.DeletePie(context.Background(), 1)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "outcome is unknown") {
		t.Fatalf("got %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `[]`) })
	c := newTestClient(t, srv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Orders(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestCachingAndInvalidation(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/instruments"):
			_, _ = io.WriteString(w, `[{"ticker":"AAPL_US_EQ"}]`)
		case strings.Contains(r.URL.Path, "/history/"):
			_, _ = io.WriteString(w, `{"items":[]}`)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	})
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	c := newTestClient(t, srv, clock, WithCacheTTLs(TTLs{Account: 15 * time.Second, Metadata: time.Hour}))

	ctx, obs := WithObserver(context.Background())
	mustOK := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mustOK(c.Orders(ctx))
	mustOK(c.Orders(ctx))
	mustOK(c.Instruments(ctx))
	mustOK(c.Instruments(ctx))
	if got := len(f.calls()); got != 2 {
		t.Fatalf("expected 2 upstream calls with warm cache, got %d", got)
	}
	o := obs.Observations()
	if len(o) != 4 || o[0].CacheHit || !o[1].CacheHit || o[1].TTLSeconds != 15 || o[3].TTLSeconds != 3600 {
		t.Errorf("observations: %+v", o)
	}

	// A mutation invalidates account data but not instrument metadata.
	if err := c.CancelOrder(ctx, 1); err != nil {
		t.Fatal(err)
	}
	mustOK(c.Orders(ctx))
	mustOK(c.Instruments(ctx))
	if got := len(f.calls()); got != 4 {
		t.Fatalf("expected orders refetch only, got %d calls", got)
	}

	// Entries expire after their TTL.
	clock.Advance(16 * time.Second)
	mustOK(c.Orders(ctx))
	if got := len(f.calls()); got != 5 {
		t.Fatalf("expected refetch after TTL, got %d calls", got)
	}

	// History TTL is zero here, so it is never cached.
	mustOK(c.Dividends(ctx, HistoryQuery{}))
	mustOK(c.Dividends(ctx, HistoryQuery{}))
	if got := len(f.calls()); got != 7 {
		t.Fatalf("history should bypass cache, got %d calls", got)
	}
}

func TestFailedMutationStillInvalidates(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	})
	c := newTestClient(t, srv, nil, WithCacheTTLs(TTLs{Account: time.Minute}))
	ctx := context.Background()
	if _, err := c.Pies(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePie(ctx, 1); err == nil {
		t.Fatal("expected error")
	}
	if _, err := c.Pies(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(f.calls()); got != 3 {
		t.Fatalf("expected refetch after failed mutation, got %d calls", got)
	}
}

func TestParseTimestamp(t *testing.T) {
	if ts, err := ParseTimestamp("f", ""); ts != nil || err != nil {
		t.Errorf("empty: %v %v", ts, err)
	}
	if ts, err := ParseTimestamp("f", "2024-01-01T10:00:00+02:00"); err != nil || ts.UTC().Hour() != 8 {
		t.Errorf("offset: %v %v", ts, err)
	}
	if _, err := ParseTimestamp("f", "2024-01-01T10:00:00"); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("naive time should fail: %v", err)
	}
}

func TestGetRetriesRequestTimeout(t *testing.T) {
	var mu sync.Mutex
	n := 0
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	})
	c := newTestClient(t, srv, nil)
	if _, err := c.Positions(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := len(f.calls()); got != 2 {
		t.Errorf("attempts: %d", got)
	}
}

func TestMutationTimeoutIsNotRetried(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestTimeout)
	})
	c := newTestClient(t, srv, nil)
	_, err := c.PlaceLimitOrder(context.Background(), LimitRequest{Ticker: "X", Quantity: 1, LimitPrice: 1, TimeValidity: TimeValidityDay})
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "outcome is unknown") {
		t.Fatalf("got %v", err)
	}
	if got := len(f.calls()); got != 1 {
		t.Errorf("attempts: %d", got)
	}
}

func TestNextPagePath(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[],"nextPagePath":null}`)
	})
	c := newTestClient(t, srv, nil)
	ctx := context.Background()

	page, err := c.HistoricalOrders(ctx, HistoryQuery{
		NextPagePath: "/api/v0/equity/history/orders?limit=2&cursor=1760346100000",
		Ticker:       "IGNORED",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextPagePath != nil {
		t.Errorf("null nextPagePath should decode as nil: %v", *page.NextPagePath)
	}
	if _, err := c.Transactions(ctx, TransactionQuery{NextPagePath: "/equity/history/transactions?cursor=abc&limit=5"}); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	if calls[0].Path != "/api/v0/equity/history/orders" || calls[0].Query != "cursor=1760346100000&limit=2" {
		t.Errorf("orders page: %s?%s", calls[0].Path, calls[0].Query)
	}
	if calls[1].Query != "cursor=abc&limit=5" {
		t.Errorf("transactions page: %s", calls[1].Query)
	}

	bad := []string{
		"https://evil.example/api/v0/equity/history/orders?cursor=1",
		"//evil.example/api/v0/equity/history/orders",
		"/api/v0/equity/history/dividends?cursor=1",
		"/api/v0/equity/history/orders/../../account/summary",
		"/api/v1/equity/history/orders?cursor=1",
	}
	for _, p := range bad {
		if _, err := c.HistoricalOrders(ctx, HistoryQuery{NextPagePath: p}); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%q: got %v, want ErrInvalidRequest", p, err)
		}
	}
	if n := len(f.calls()); n != 2 {
		t.Errorf("rejected paths reached the API: %d calls", n)
	}
}

func TestTransactionTimeIsValidated(t *testing.T) {
	f, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"items":[]}`) })
	c := newTestClient(t, srv, nil)
	if _, err := c.Transactions(context.Background(), TransactionQuery{Time: "yesterday"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("got %v", err)
	}
	if len(f.calls()) != 0 {
		t.Error("invalid time reached the API")
	}
}
