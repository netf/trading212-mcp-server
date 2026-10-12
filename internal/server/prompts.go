package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const analysisPrompt = `Analyse the user's Trading 212 data carefully. Distinguish observed
account data from assumptions, and describe limitations of any financial analysis.
Use the account currency if the instrument currency is not supplied.
GBX represents pence: 100 GBX equals 1 GBP.
`

func (h *handlers) registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "analyse_trading212_data",
		Title:       "Analyse Trading 212 data",
		Description: "Guidance for analysing Trading 212 account data, including the account currency when available.",
	}, func(ctx context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := analysisPrompt
		// The prompt stays useful without account access, so a failed
		// lookup degrades to the generic text instead of failing.
		if summary, err := h.client.AccountSummary(ctx); err != nil {
			h.logger.WarnContext(ctx, "account currency unavailable for analysis prompt", "error", err)
		} else if summary.Currency != nil && *summary.Currency != "" {
			text += "\nCurrency: " + *summary.Currency
		}
		return &mcp.GetPromptResult{
			Description: "Analyse Trading 212 data",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}
