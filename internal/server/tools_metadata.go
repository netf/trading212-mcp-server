package server

import (
	"context"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

// InstrumentsResult wraps an instrument list so structured output is an object.
type InstrumentsResult struct {
	Instruments []t212.TradeableInstrument `json:"instruments"`
}

// ExchangesResult wraps an exchange list so structured output is an object.
type ExchangesResult struct {
	Exchanges []t212.Exchange `json:"exchanges"`
}

type SearchInput struct {
	SearchTerm string `json:"search_term,omitempty" jsonschema:"case-insensitive filter; omit to return everything"`
}

func filterInstruments(all []t212.TradeableInstrument, term string) []t212.TradeableInstrument {
	if term == "" {
		return all
	}
	needle := strings.ToLower(term)
	out := []t212.TradeableInstrument{}
	for _, inst := range all {
		if containsFold(inst.Ticker, needle) || containsFold(inst.Name, needle) || containsFold(inst.ShortName, needle) {
			out = append(out, inst)
		}
	}
	return out
}

func filterExchanges(all []t212.Exchange, term string) []t212.Exchange {
	if term == "" {
		return all
	}
	needle := strings.ToLower(term)
	out := []t212.Exchange{}
	for _, ex := range all {
		if strings.Contains(strings.ToLower(ex.Name), needle) || strconv.FormatInt(ex.ID, 10) == term {
			out = append(out, ex)
		}
	}
	return out
}

func containsFold(s *string, lowerNeedle string) bool {
	return s != nil && strings.Contains(strings.ToLower(*s), lowerNeedle)
}

func (h *handlers) registerMetadataTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "search_instrument",
		Description: "List tradeable instruments, optionally filtered by ticker or name (case-insensitive substring).",
		Annotations: readOnly("Search instruments"),
	}, func(ctx context.Context, in SearchInput) (InstrumentsResult, error) {
		all, err := h.client.Instruments(ctx)
		if err != nil {
			return InstrumentsResult{}, err
		}
		return InstrumentsResult{Instruments: filterInstruments(all, strings.TrimSpace(in.SearchTerm))}, nil
	})
	addTool(s, &mcp.Tool{
		Name:        "search_exchange",
		Description: "List exchanges and their working schedules, optionally filtered by name (substring) or exact ID.",
		Annotations: readOnly("Search exchanges"),
	}, func(ctx context.Context, in SearchInput) (ExchangesResult, error) {
		all, err := h.client.Exchanges(ctx)
		if err != nil {
			return ExchangesResult{}, err
		}
		return ExchangesResult{Exchanges: filterExchanges(all, strings.TrimSpace(in.SearchTerm))}, nil
	})
}
