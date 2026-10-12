package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

const (
	resourceScheme = "trading212://"
	jsonMIME       = "application/json"
)

type fetchFunc func(ctx context.Context) (any, error)

type paramFetchFunc func(ctx context.Context, param string) (any, error)

func (h *handlers) registerResources(s *mcp.Server) {
	summary := func(ctx context.Context) (any, error) { return h.client.AccountSummary(ctx) }
	positions := func(ctx context.Context) (any, error) { return h.client.Positions(ctx, "") }
	position := func(ctx context.Context, ticker string) (any, error) {
		return h.client.PositionByTicker(ctx, ticker)
	}

	static := []struct {
		path, name, desc string
		fetch            fetchFunc
	}{
		{"account/info", "account-info", "Account summary.", summary},
		{"account/summary", "account-summary", "Account summary.", summary},
		{"account/cash", "account-cash", "Account cash balance.", func(ctx context.Context) (any, error) {
			return h.client.AccountCash(ctx)
		}},
		{"positions", "positions", "All open positions.", positions},
		{"account/positions", "account-positions", "All open positions (alias of trading212://positions).", positions},
		{"account/portfolio", "account-portfolio", "All open positions (alias of trading212://positions).", positions},
		{"orders", "orders", "Pending orders.", func(ctx context.Context) (any, error) { return h.client.Orders(ctx) }},
		{"pies", "pies", "All pies.", func(ctx context.Context) (any, error) { return h.client.Pies(ctx) }},
		{"instruments", "instruments", "All tradeable instruments.", func(ctx context.Context) (any, error) {
			return h.client.Instruments(ctx)
		}},
		{"exchanges", "exchanges", "Exchanges and their working schedules.", func(ctx context.Context) (any, error) {
			return h.client.Exchanges(ctx)
		}},
		{"history/exports", "history-exports", "CSV account exports.", func(ctx context.Context) (any, error) {
			return h.client.Reports(ctx)
		}},
	}
	for _, r := range static {
		uri := resourceScheme + r.path
		fetch := r.fetch
		s.AddResource(&mcp.Resource{URI: uri, Name: r.name, Description: r.desc, MIMEType: jsonMIME},
			func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return readJSON(ctx, req.Params.URI, fetch)
			})
	}

	templates := []struct {
		prefix, param, name, desc string
		fetch                     paramFetchFunc
	}{
		{"positions/", "ticker", "position", "Open position for a ticker.", position},
		{"account/positions/", "ticker", "account-position", "Open position for a ticker (alias).", position},
		{"account/portfolio/", "ticker", "account-portfolio-position", "Open position for a ticker (alias).", position},
		{"orders/", "order_id", "order", "Pending order by ID.", func(ctx context.Context, id string) (any, error) {
			n, err := parseID("order_id", id)
			if err != nil {
				return nil, err
			}
			return h.client.Order(ctx, n)
		}},
		{"pies/", "pie_id", "pie", "Pie by ID.", func(ctx context.Context, id string) (any, error) {
			n, err := parseID("pie_id", id)
			if err != nil {
				return nil, err
			}
			return h.client.Pie(ctx, n)
		}},
	}
	for _, t := range templates {
		prefix := resourceScheme + t.prefix
		fetch := t.fetch
		s.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: prefix + "{" + t.param + "}",
			Name:        t.name,
			Description: t.desc,
			MIMEType:    jsonMIME,
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			raw, ok := strings.CutPrefix(uri, prefix)
			if !ok || raw == "" || strings.Contains(raw, "/") {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			param, err := url.PathUnescape(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: malformed resource URI", t212.ErrInvalidRequest)
			}
			return readJSON(ctx, uri, func(ctx context.Context) (any, error) { return fetch(ctx, param) })
		})
	}
}

func parseID(name, raw string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", t212.ErrInvalidRequest, name)
	}
	return n, nil
}

func readJSON(ctx context.Context, uri string, fetch fetchFunc) (*mcp.ReadResourceResult, error) {
	ctx, obs := t212.WithObserver(ctx)
	v, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", uri, err)
	}
	contents := &mcp.ResourceContents{URI: uri, MIMEType: jsonMIME, Text: string(body)}
	if o := obs.Observations(); len(o) > 0 {
		contents.Meta = mcp.Meta{CacheMetaKey: o}
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{contents}}, nil
}
