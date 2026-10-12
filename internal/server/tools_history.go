package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

// ReportsResult wraps an export list so structured output is an object.
type ReportsResult struct {
	Reports []t212.Report `json:"reports"`
}

type HistoryInput struct {
	NextPagePath string `json:"next_page_path,omitempty" jsonschema:"nextPagePath from the previous page, passed back verbatim to fetch the next page; other parameters are then ignored"`
	Cursor       *int64 `json:"cursor,omitempty" jsonschema:"pagination cursor (prefer next_page_path)"`
	Ticker       string `json:"ticker,omitempty" jsonschema:"optional ticker filter, e.g. AAPL_US_EQ"`
	Limit        int    `json:"limit,omitempty" jsonschema:"page size, 1-50"`
}

type TransactionsInput struct {
	NextPagePath string `json:"next_page_path,omitempty" jsonschema:"nextPagePath from the previous page, passed back verbatim to fetch the next page; other parameters are then ignored"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"pagination cursor (prefer next_page_path)"`
	Time         string `json:"time,omitempty" jsonschema:"only return transactions from this ISO 8601 instant (with timezone) onwards"`
	Limit        int    `json:"limit,omitempty" jsonschema:"page size, 1-50"`
}

type ExportInput struct {
	IncludeDividends    bool   `json:"include_dividends,omitempty" jsonschema:"include dividends"`
	IncludeInterest     bool   `json:"include_interest,omitempty" jsonschema:"include interest"`
	IncludeOrders       bool   `json:"include_orders,omitempty" jsonschema:"include order history"`
	IncludeTransactions bool   `json:"include_transactions,omitempty" jsonschema:"include transactions"`
	TimeFrom            string `json:"time_from,omitempty" jsonschema:"report start, ISO 8601 with timezone (e.g. 2023-01-01T00:00:00Z)"`
	TimeTo              string `json:"time_to,omitempty" jsonschema:"report end, ISO 8601 with timezone (e.g. 2023-12-31T23:59:59Z)"`
}

func historyQuery(in HistoryInput) t212.HistoryQuery {
	return t212.HistoryQuery{Cursor: in.Cursor, Ticker: in.Ticker, Limit: in.Limit, NextPagePath: in.NextPagePath}
}

func (h *handlers) registerHistoryTools(s *mcp.Server) {
	limitDefault := map[string]field{"limit": {Default: 20}}

	addTool(s, &mcp.Tool{
		Name:        "fetch_historical_order_data",
		Description: "Fetch filled and historical orders, newest first, with cursor pagination.",
		Annotations: readOnly("Order history"),
		InputSchema: inputSchema[HistoryInput](limitDefault),
	}, func(ctx context.Context, in HistoryInput) (*t212.Page[t212.HistoricalOrder], error) {
		return h.client.HistoricalOrders(ctx, historyQuery(in))
	})
	addTool(s, &mcp.Tool{
		Name:        "fetch_paid_out_dividends",
		Description: "Fetch paid-out dividends with cursor pagination.",
		Annotations: readOnly("Dividends"),
		InputSchema: inputSchema[HistoryInput](limitDefault),
	}, func(ctx context.Context, in HistoryInput) (*t212.Page[t212.DividendItem], error) {
		return h.client.Dividends(ctx, historyQuery(in))
	})
	addTool(s, &mcp.Tool{
		Name:        "fetch_transaction_list",
		Description: "Fetch deposits, withdrawals, fees, transfers and interest with cursor pagination.",
		Annotations: readOnly("Transactions"),
		InputSchema: inputSchema[TransactionsInput](limitDefault),
	}, func(ctx context.Context, in TransactionsInput) (*t212.Page[t212.TransactionItem], error) {
		return h.client.Transactions(ctx, t212.TransactionQuery{
			Cursor: in.Cursor, Time: in.Time, Limit: in.Limit, NextPagePath: in.NextPagePath,
		})
	})
	addTool(s, &mcp.Tool{
		Name:        "fetch_exports_list",
		Description: "List all CSV account exports with their status and download link.",
		Annotations: readOnly("CSV exports"),
	}, func(ctx context.Context, _ struct{}) (ReportsResult, error) {
		r, err := h.client.Reports(ctx)
		return ReportsResult{Reports: r}, err
	})
	addTool(s, &mcp.Tool{
		Name: "request_csv_export",
		Description: "Request a CSV export of orders, dividends, interest and transactions. " +
			"When finished, the download link appears in fetch_exports_list.",
		Annotations: mutating("Request CSV export"),
		InputSchema: inputSchema[ExportInput](map[string]field{
			"include_dividends":    {Default: true},
			"include_interest":     {Default: true},
			"include_orders":       {Default: true},
			"include_transactions": {Default: true},
		}),
	}, func(ctx context.Context, in ExportInput) (*t212.EnqueuedReport, error) {
		from, err := t212.ParseTimestamp("time_from", in.TimeFrom)
		if err != nil {
			return nil, err
		}
		to, err := t212.ParseTimestamp("time_to", in.TimeTo)
		if err != nil {
			return nil, err
		}
		return h.client.RequestExport(ctx, t212.ExportRequest{
			DataIncluded: t212.ReportDataIncluded{
				IncludeDividends:    in.IncludeDividends,
				IncludeInterest:     in.IncludeInterest,
				IncludeOrders:       in.IncludeOrders,
				IncludeTransactions: in.IncludeTransactions,
			},
			TimeFrom: from,
			TimeTo:   to,
		})
	})
}
