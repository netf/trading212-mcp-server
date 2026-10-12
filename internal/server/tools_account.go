package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

// PositionsResult wraps a position list so structured output is an object.
type PositionsResult struct {
	Positions []t212.Position `json:"positions"`
}

// TickerInput selects a single instrument by ticker.
type TickerInput struct {
	Ticker string `json:"ticker" jsonschema:"Trading 212 ticker, e.g. AAPL_US_EQ"`
}

// OptionalTickerInput optionally filters by ticker.
type OptionalTickerInput struct {
	Ticker string `json:"ticker,omitempty" jsonschema:"optional Trading 212 ticker filter, e.g. AAPL_US_EQ"`
}

func (h *handlers) registerAccountTools(s *mcp.Server) {
	summary := func(ctx context.Context, _ struct{}) (*t212.AccountSummary, error) {
		return h.client.AccountSummary(ctx)
	}
	addTool(s, &mcp.Tool{
		Name:        "fetch_account_info",
		Description: "Fetch the account summary: id, currency, cash breakdown, investments and total value.",
		Annotations: readOnly("Account info"),
	}, summary)
	addTool(s, &mcp.Tool{
		Name:        "fetch_account_summary",
		Description: "Fetch the account summary: id, currency, cash breakdown, investments and total value.",
		Annotations: readOnly("Account summary"),
	}, summary)
	addTool(s, &mcp.Tool{
		Name:        "fetch_account_cash",
		Description: "Fetch the account cash balance (available to trade, in pies, reserved for orders).",
		Annotations: readOnly("Account cash"),
	}, func(ctx context.Context, _ struct{}) (*t212.Cash, error) {
		return h.client.AccountCash(ctx)
	})

	addTool(s, &mcp.Tool{
		Name:        "fetch_positions",
		Description: "Fetch open positions, optionally filtered by ticker.",
		Annotations: readOnly("Open positions"),
	}, func(ctx context.Context, in OptionalTickerInput) (PositionsResult, error) {
		p, err := h.client.Positions(ctx, in.Ticker)
		return PositionsResult{Positions: p}, err
	})
	addTool(s, &mcp.Tool{
		Name:        "fetch_all_open_positions",
		Description: "Fetch all open positions. Alias of fetch_positions without a filter.",
		Annotations: readOnly("All open positions"),
	}, func(ctx context.Context, _ struct{}) (PositionsResult, error) {
		p, err := h.client.Positions(ctx, "")
		return PositionsResult{Positions: p}, err
	})

	byTicker := func(ctx context.Context, in TickerInput) (*t212.Position, error) {
		return h.client.PositionByTicker(ctx, in.Ticker)
	}
	for _, name := range []string{"fetch_position_by_ticker", "fetch_open_position_by_ticker", "search_specific_position_by_ticker"} {
		desc := "Fetch a single open position by ticker."
		if name != "fetch_position_by_ticker" {
			desc += " Alias of fetch_position_by_ticker."
		}
		addTool(s, &mcp.Tool{Name: name, Description: desc, Annotations: readOnly("Position by ticker")}, byTicker)
	}
}
