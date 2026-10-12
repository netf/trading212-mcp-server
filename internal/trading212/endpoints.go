package trading212

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AccountSummary fetches GET /equity/account/summary.
func (c *Client) AccountSummary(ctx context.Context) (*AccountSummary, error) {
	var out AccountSummary
	if err := c.do(ctx, http.MethodGet, "/equity/account/summary", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch account summary: %w", err)
	}
	return &out, nil
}

// AccountCash returns the cash section of the account summary.
func (c *Client) AccountCash(ctx context.Context) (*Cash, error) {
	summary, err := c.AccountSummary(ctx)
	if err != nil {
		return nil, err
	}
	if summary.Cash == nil {
		return &Cash{}, nil
	}
	return summary.Cash, nil
}

// Positions fetches open positions, optionally filtered by ticker.
func (c *Client) Positions(ctx context.Context, ticker string) ([]Position, error) {
	q := url.Values{}
	if ticker != "" {
		q.Set("ticker", ticker)
	}
	out := []Position{}
	if err := c.do(ctx, http.MethodGet, "/equity/positions", q, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch positions: %w", err)
	}
	return nonNil(out), nil
}

// PositionByTicker returns the open position for ticker.
func (c *Client) PositionByTicker(ctx context.Context, ticker string) (*Position, error) {
	if strings.TrimSpace(ticker) == "" {
		return nil, fmt.Errorf("%w: ticker is required", ErrInvalidRequest)
	}
	positions, err := c.Positions(ctx, ticker)
	if err != nil {
		return nil, err
	}
	for i := range positions {
		if p := positions[i]; p.Instrument != nil && p.Instrument.Ticker != nil && *p.Instrument.Ticker == ticker {
			return &positions[i], nil
		}
	}
	if len(positions) > 0 {
		return &positions[0], nil
	}
	return nil, fmt.Errorf("%w: no open position for ticker %q", ErrNotFound, ticker)
}

// Orders fetches all pending orders.
func (c *Client) Orders(ctx context.Context) ([]Order, error) {
	out := []Order{}
	if err := c.do(ctx, http.MethodGet, "/equity/orders", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch orders: %w", err)
	}
	return nonNil(out), nil
}

// Order fetches a pending order by ID.
func (c *Client) Order(ctx context.Context, id int64) (*Order, error) {
	var out Order
	if err := c.do(ctx, http.MethodGet, "/equity/orders/"+strconv.FormatInt(id, 10), nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch order %d: %w", id, err)
	}
	return &out, nil
}

// CancelOrder cancels a pending order.
func (c *Client) CancelOrder(ctx context.Context, id int64) error {
	if err := c.do(ctx, http.MethodDelete, "/equity/orders/"+strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		return fmt.Errorf("cancel order %d: %w", id, err)
	}
	return nil
}

// PlaceMarketOrder places a market order. Positive quantity buys, negative sells.
func (c *Client) PlaceMarketOrder(ctx context.Context, r MarketRequest) (*Order, error) {
	if err := validateOrder(r.Ticker, r.Quantity); err != nil {
		return nil, err
	}
	return c.placeOrder(ctx, "market", r)
}

// PlaceLimitOrder places a limit order.
func (c *Client) PlaceLimitOrder(ctx context.Context, r LimitRequest) (*Order, error) {
	if err := validateOrder(r.Ticker, r.Quantity, price("limit price", r.LimitPrice)); err != nil {
		return nil, err
	}
	if err := validateValidity(r.TimeValidity); err != nil {
		return nil, err
	}
	return c.placeOrder(ctx, "limit", r)
}

// PlaceStopOrder places a stop order.
func (c *Client) PlaceStopOrder(ctx context.Context, r StopRequest) (*Order, error) {
	if err := validateOrder(r.Ticker, r.Quantity, price("stop price", r.StopPrice)); err != nil {
		return nil, err
	}
	if err := validateValidity(r.TimeValidity); err != nil {
		return nil, err
	}
	return c.placeOrder(ctx, "stop", r)
}

// PlaceStopLimitOrder places a stop-limit order.
func (c *Client) PlaceStopLimitOrder(ctx context.Context, r StopLimitRequest) (*Order, error) {
	if err := validateOrder(r.Ticker, r.Quantity, price("stop price", r.StopPrice), price("limit price", r.LimitPrice)); err != nil {
		return nil, err
	}
	if err := validateValidity(r.TimeValidity); err != nil {
		return nil, err
	}
	return c.placeOrder(ctx, "stop_limit", r)
}

func (c *Client) placeOrder(ctx context.Context, kind string, body any) (*Order, error) {
	var out Order
	if err := c.do(ctx, http.MethodPost, "/equity/orders/"+kind, nil, body, &out); err != nil {
		return nil, fmt.Errorf("place %s order: %w", strings.ReplaceAll(kind, "_", "-"), err)
	}
	return &out, nil
}

// Pies fetches all pies.
func (c *Client) Pies(ctx context.Context) ([]Pie, error) {
	out := []Pie{}
	if err := c.do(ctx, http.MethodGet, "/equity/pies", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch pies: %w", err)
	}
	return nonNil(out), nil
}

// Pie fetches a pie by ID.
func (c *Client) Pie(ctx context.Context, id int64) (*PieDetails, error) {
	var out PieDetails
	if err := c.do(ctx, http.MethodGet, pieURL(id), nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch pie %d: %w", id, err)
	}
	return &out, nil
}

// CreatePie creates a pie.
func (c *Client) CreatePie(ctx context.Context, r PieRequest) (*PieDetails, error) {
	if r.Name == nil || strings.TrimSpace(*r.Name) == "" {
		return nil, fmt.Errorf("%w: pie name is required", ErrInvalidRequest)
	}
	if len(r.InstrumentShares) == 0 {
		return nil, fmt.Errorf("%w: at least one instrument share is required", ErrInvalidRequest)
	}
	if err := validatePie(r); err != nil {
		return nil, err
	}
	var out PieDetails
	if err := c.do(ctx, http.MethodPost, "/equity/pies", nil, r, &out); err != nil {
		return nil, fmt.Errorf("create pie: %w", err)
	}
	return &out, nil
}

// UpdatePie updates a pie. Only non-nil fields are sent.
func (c *Client) UpdatePie(ctx context.Context, id int64, r PieRequest) (*PieDetails, error) {
	if err := validatePie(r); err != nil {
		return nil, err
	}
	var out PieDetails
	if err := c.do(ctx, http.MethodPost, pieURL(id), nil, r, &out); err != nil {
		return nil, fmt.Errorf("update pie %d: %w", id, err)
	}
	return &out, nil
}

// DuplicatePie duplicates a pie.
func (c *Client) DuplicatePie(ctx context.Context, id int64, r DuplicatePieRequest) (*PieDetails, error) {
	var out PieDetails
	if err := c.do(ctx, http.MethodPost, pieURL(id)+"/duplicate", nil, r, &out); err != nil {
		return nil, fmt.Errorf("duplicate pie %d: %w", id, err)
	}
	return &out, nil
}

// DeletePie deletes a pie.
func (c *Client) DeletePie(ctx context.Context, id int64) error {
	if err := c.do(ctx, http.MethodDelete, pieURL(id), nil, nil, nil); err != nil {
		return fmt.Errorf("delete pie %d: %w", id, err)
	}
	return nil
}

func pieURL(id int64) string { return "/equity/pies/" + strconv.FormatInt(id, 10) }

// HistoryQuery holds the common history pagination parameters. When
// NextPagePath is set (the nextPagePath of a previous page), it is used
// verbatim, as the API documentation recommends, and the other fields are
// ignored.
type HistoryQuery struct {
	Cursor       *int64
	Ticker       string
	Limit        int
	NextPagePath string
}

func (q HistoryQuery) values(endpoint string) (url.Values, error) {
	if q.NextPagePath != "" {
		return nextPageValues(endpoint, q.NextPagePath)
	}
	v := url.Values{}
	if q.Cursor != nil {
		v.Set("cursor", strconv.FormatInt(*q.Cursor, 10))
	}
	if q.Ticker != "" {
		v.Set("ticker", q.Ticker)
	}
	v.Set("limit", strconv.Itoa(clampLimit(q.Limit)))
	return v, nil
}

// nextPageValues validates that nextPagePath points at endpoint on this API
// and returns its query parameters. Anything else is rejected so a model
// cannot redirect authenticated requests elsewhere.
func nextPageValues(endpoint, nextPagePath string) (url.Values, error) {
	u, err := url.Parse(nextPagePath)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("%w: next_page_path must be a relative API path", ErrInvalidRequest)
	}
	if u.Path != "/api/"+defaultAPIVersion+endpoint && u.Path != endpoint {
		return nil, fmt.Errorf("%w: next_page_path must point at %s", ErrInvalidRequest, endpoint)
	}
	v, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: next_page_path has a malformed query", ErrInvalidRequest)
	}
	return v, nil
}

const (
	historyOrdersPath       = "/equity/history/orders"
	historyDividendsPath    = "/equity/history/dividends"
	historyTransactionsPath = "/equity/history/transactions"
)

// HistoricalOrders fetches GET /equity/history/orders.
func (c *Client) HistoricalOrders(ctx context.Context, q HistoryQuery) (*Page[HistoricalOrder], error) {
	return fetchPage[HistoricalOrder](ctx, c, "historical orders", historyOrdersPath, q.values)
}

// Dividends fetches GET /equity/history/dividends.
func (c *Client) Dividends(ctx context.Context, q HistoryQuery) (*Page[DividendItem], error) {
	return fetchPage[DividendItem](ctx, c, "dividends", historyDividendsPath, q.values)
}

// TransactionQuery holds parameters for the transactions history endpoint.
// NextPagePath behaves as in HistoryQuery.
type TransactionQuery struct {
	Cursor string
	// Time restricts results to transactions from this RFC 3339 instant onwards.
	Time         string
	Limit        int
	NextPagePath string
}

func (q TransactionQuery) values(endpoint string) (url.Values, error) {
	if q.NextPagePath != "" {
		return nextPageValues(endpoint, q.NextPagePath)
	}
	v := url.Values{}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Time != "" {
		t, err := ParseTimestamp("time", q.Time)
		if err != nil {
			return nil, err
		}
		v.Set("time", t.Format(time.RFC3339Nano))
	}
	v.Set("limit", strconv.Itoa(clampLimit(q.Limit)))
	return v, nil
}

// Transactions fetches GET /equity/history/transactions.
func (c *Client) Transactions(ctx context.Context, q TransactionQuery) (*Page[TransactionItem], error) {
	return fetchPage[TransactionItem](ctx, c, "transactions", historyTransactionsPath, q.values)
}

func fetchPage[T any](ctx context.Context, c *Client, what, endpoint string, values func(string) (url.Values, error)) (*Page[T], error) {
	q, err := values(endpoint)
	if err != nil {
		return nil, err
	}
	var out Page[T]
	if err := c.do(ctx, http.MethodGet, endpoint, q, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", what, err)
	}
	out.Items = nonNil(out.Items)
	return &out, nil
}

// Reports lists CSV exports.
func (c *Client) Reports(ctx context.Context) ([]Report, error) {
	out := []Report{}
	if err := c.do(ctx, http.MethodGet, "/equity/history/exports", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch exports: %w", err)
	}
	return nonNil(out), nil
}

// RequestExport enqueues a CSV export.
func (c *Client) RequestExport(ctx context.Context, r ExportRequest) (*EnqueuedReport, error) {
	if r.TimeFrom != nil && r.TimeTo != nil && r.TimeFrom.After(*r.TimeTo) {
		return nil, fmt.Errorf("%w: export start must not be after its end", ErrInvalidRequest)
	}
	var out EnqueuedReport
	if err := c.do(ctx, http.MethodPost, "/equity/history/exports", nil, r, &out); err != nil {
		return nil, fmt.Errorf("request export: %w", err)
	}
	return &out, nil
}

// Instruments fetches all tradeable instruments.
func (c *Client) Instruments(ctx context.Context) ([]TradeableInstrument, error) {
	out := []TradeableInstrument{}
	if err := c.do(ctx, http.MethodGet, "/equity/metadata/instruments", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch instruments: %w", err)
	}
	return nonNil(out), nil
}

// Exchanges fetches all exchanges and their working schedules.
func (c *Client) Exchanges(ctx context.Context) ([]Exchange, error) {
	out := []Exchange{}
	if err := c.do(ctx, http.MethodGet, "/equity/metadata/exchanges", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch exchanges: %w", err)
	}
	return nonNil(out), nil
}

// ParseTimestamp parses an RFC 3339 / ISO 8601 timestamp that must carry a
// timezone offset.
func ParseTimestamp(field, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil //nolint:nilnil // an empty optional timestamp is valid and means "unset"
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be an ISO 8601 timestamp with timezone (e.g. 2024-01-01T00:00:00Z)", ErrInvalidRequest, field)
	}
	return &t, nil
}

type check func() error

func price(name string, v float64) check {
	return func() error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return fmt.Errorf("%w: %s must be a positive number", ErrInvalidRequest, name)
		}
		return nil
	}
}

func validateOrder(ticker string, quantity float64, checks ...check) error {
	if strings.TrimSpace(ticker) == "" {
		return fmt.Errorf("%w: ticker is required", ErrInvalidRequest)
	}
	if math.IsNaN(quantity) || math.IsInf(quantity, 0) || quantity == 0 {
		return fmt.Errorf("%w: quantity must be a non-zero number (positive buys, negative sells)", ErrInvalidRequest)
	}
	for _, c := range checks {
		if err := c(); err != nil {
			return err
		}
	}
	return nil
}

func validateValidity(v TimeValidity) error {
	if !v.Valid() {
		return fmt.Errorf("%w: time validity must be %s or %s", ErrInvalidRequest, TimeValidityDay, TimeValidityGoodTillCancel)
	}
	return nil
}

func validatePie(r PieRequest) error {
	if r.DividendCashAction != nil && !r.DividendCashAction.Valid() {
		return fmt.Errorf("%w: dividend cash action must be %s or %s",
			ErrInvalidRequest, DividendCashActionReinvest, DividendCashActionToAccountCash)
	}
	if r.Goal != nil && (math.IsNaN(*r.Goal) || math.IsInf(*r.Goal, 0) || *r.Goal < 0) {
		return fmt.Errorf("%w: goal must be a non-negative number", ErrInvalidRequest)
	}
	for ticker, share := range r.InstrumentShares {
		if strings.TrimSpace(ticker) == "" || math.IsNaN(share) || math.IsInf(share, 0) || share <= 0 {
			return fmt.Errorf("%w: instrument shares need non-empty tickers and positive weights", ErrInvalidRequest)
		}
	}
	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
