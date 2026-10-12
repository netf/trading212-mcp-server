package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

// PiesResult wraps a pie list so structured output is an object.
type PiesResult struct {
	Pies []t212.Pie `json:"pies"`
}

// PieIDInput selects a pie.
type PieIDInput struct {
	PieID int64 `json:"pie_id" jsonschema:"ID of the pie"`
}

type CreatePieInput struct {
	Name               string             `json:"name" jsonschema:"name of the pie"`
	InstrumentShares   map[string]float64 `json:"instrument_shares" jsonschema:"ticker to target weight, e.g. {\"AAPL_US_EQ\": 0.5, \"MSFT_US_EQ\": 0.5}"`
	DividendCashAction string             `json:"dividend_cash_action,omitempty" jsonschema:"REINVEST or TO_ACCOUNT_CASH"`
	EndDate            string             `json:"end_date,omitempty" jsonschema:"optional end date, ISO 8601 with timezone (e.g. 2030-12-31T23:59:59Z)"`
	Goal               *float64           `json:"goal,omitempty" jsonschema:"total desired value of the pie in account currency"`
	Icon               string             `json:"icon,omitempty" jsonschema:"optional icon identifier"`
}

type UpdatePieInput struct {
	PieID              int64              `json:"pie_id" jsonschema:"ID of the pie to update"`
	Name               string             `json:"name,omitempty" jsonschema:"new name"`
	InstrumentShares   map[string]float64 `json:"instrument_shares,omitempty" jsonschema:"new ticker to target weight map"`
	DividendCashAction string             `json:"dividend_cash_action,omitempty" jsonschema:"REINVEST or TO_ACCOUNT_CASH"`
	EndDate            string             `json:"end_date,omitempty" jsonschema:"new end date, ISO 8601 with timezone"`
	Goal               *float64           `json:"goal,omitempty" jsonschema:"new total desired value in account currency"`
	Icon               string             `json:"icon,omitempty" jsonschema:"new icon identifier"`
}

type DuplicatePieInput struct {
	PieID int64  `json:"pie_id" jsonschema:"ID of the pie to duplicate"`
	Name  string `json:"name,omitempty" jsonschema:"optional name for the copy"`
	Icon  string `json:"icon,omitempty" jsonschema:"optional icon for the copy"`
}

var dividendCashActionField = field{
	Enum: []any{string(t212.DividendCashActionReinvest), string(t212.DividendCashActionToAccountCash)},
}

func pieRequest(name string, shares map[string]float64, action, endDate string, goal *float64, icon string) (t212.PieRequest, error) {
	end, err := t212.ParseTimestamp("end_date", endDate)
	if err != nil {
		return t212.PieRequest{}, err
	}
	r := t212.PieRequest{
		EndDate:          end,
		Goal:             goal,
		InstrumentShares: shares,
		Name:             optional(name),
		Icon:             optional(icon),
	}
	if action != "" {
		a := t212.DividendCashAction(action)
		r.DividendCashAction = &a
	}
	return r, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Trading 212 marks every pies endpoint as deprecated in its API reference.
const piesDeprecation = " Note: Trading 212 has deprecated the pies API; it may change or be removed."

func (h *handlers) registerPieTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "fetch_pies",
		Description: "Fetch all pies with their cash, progress, result and dividend details." + piesDeprecation,
		Annotations: readOnly("Pies"),
	}, func(ctx context.Context, _ struct{}) (PiesResult, error) {
		p, err := h.client.Pies(ctx)
		return PiesResult{Pies: p}, err
	})
	addTool(s, &mcp.Tool{
		Name:        "fetch_a_pie",
		Description: "Fetch a pie by ID, including its instruments and settings." + piesDeprecation,
		Annotations: readOnly("Pie by ID"),
	}, func(ctx context.Context, in PieIDInput) (*t212.PieDetails, error) {
		return h.client.Pie(ctx, in.PieID)
	})
	addTool(s, &mcp.Tool{
		Name:        "create_pie",
		Description: "Create a new pie from a ticker-to-weight map." + piesDeprecation,
		Annotations: mutating("Create pie"),
		InputSchema: inputSchema[CreatePieInput](map[string]field{"dividend_cash_action": dividendCashActionField}),
	}, func(ctx context.Context, in CreatePieInput) (*t212.PieDetails, error) {
		r, err := pieRequest(in.Name, in.InstrumentShares, in.DividendCashAction, in.EndDate, in.Goal, in.Icon)
		if err != nil {
			return nil, err
		}
		return h.client.CreatePie(ctx, r)
	})
	addTool(s, &mcp.Tool{
		Name:        "update_pie",
		Description: "Update an existing pie. Only the fields provided are changed." + piesDeprecation,
		Annotations: mutating("Update pie"),
		InputSchema: inputSchema[UpdatePieInput](map[string]field{"dividend_cash_action": dividendCashActionField}),
	}, func(ctx context.Context, in UpdatePieInput) (*t212.PieDetails, error) {
		r, err := pieRequest(in.Name, in.InstrumentShares, in.DividendCashAction, in.EndDate, in.Goal, in.Icon)
		if err != nil {
			return nil, err
		}
		return h.client.UpdatePie(ctx, in.PieID, r)
	})
	addTool(s, &mcp.Tool{
		Name:        "duplicate_pie",
		Description: "Create a copy of an existing pie, optionally with a new name and icon." + piesDeprecation,
		Annotations: mutating("Duplicate pie"),
	}, func(ctx context.Context, in DuplicatePieInput) (*t212.PieDetails, error) {
		return h.client.DuplicatePie(ctx, in.PieID, t212.DuplicatePieRequest{Name: optional(in.Name), Icon: optional(in.Icon)})
	})
	addTool(s, &mcp.Tool{
		Name:        "delete_pie",
		Description: "Delete a pie by ID." + piesDeprecation,
		Annotations: mutating("Delete pie"),
	}, func(ctx context.Context, in PieIDInput) (ActionResult, error) {
		if err := h.client.DeletePie(ctx, in.PieID); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{OK: true, Message: fmt.Sprintf("pie %d deleted", in.PieID)}, nil
	})
}
