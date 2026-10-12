// Package server exposes the Trading 212 API as MCP tools, resources and
// prompts.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/netf/trading212-mcp-server/internal/trading212"
)

// Name is the MCP implementation name advertised to clients.
const Name = "trading212"

// CacheMetaKey is the _meta key under which per-call cache observations
// (cache hit, retrieval time, TTL) are attached to tool and resource results.
const CacheMetaKey = "io.github.netf.trading212/cache"

const instructions = `Tools, resources and a prompt for a Trading 212 Invest/ISA account.
Read-only tools are safe to call freely. Tools that place or cancel orders,
change pies or request exports act on the configured account (demo or live);
confirm intent with the user before calling them. Quantities are positive to
buy and negative to sell. GBX prices are in pence (100 GBX = 1 GBP).`

// Options configures the MCP server.
type Options struct {
	Version string
	// Logger receives application warnings. Nil discards them.
	Logger *slog.Logger
	// ProtocolLogger receives MCP session/transport logs from the SDK. Nil
	// disables them.
	ProtocolLogger *slog.Logger
}

// New builds an MCP server backed by client.
func New(client *trading212.Client, opts Options) *mcp.Server {
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Title: "Trading 212", Version: version}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       opts.ProtocolLogger,
	})
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &handlers{client: client, logger: logger}
	h.registerAccountTools(s)
	h.registerOrderTools(s)
	h.registerPieTools(s)
	h.registerHistoryTools(s)
	h.registerMetadataTools(s)
	h.registerResources(s)
	h.registerPrompts(s)
	return s
}

type handlers struct {
	client *trading212.Client
	logger *slog.Logger
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: ptr(false),
		IdempotentHint:  true,
		OpenWorldHint:   ptr(true),
	}
}

func mutating(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		DestructiveHint: ptr(true),
		IdempotentHint:  false,
		OpenWorldHint:   ptr(true),
	}
}

// addTool registers a typed tool whose handler only sees the context and
// input, and attaches cache observations to the result metadata.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		ctx, obs := trading212.WithObserver(ctx)
		out, err := h(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		res := &mcp.CallToolResult{}
		if o := obs.Observations(); len(o) > 0 {
			res.Meta = mcp.Meta{CacheMetaKey: o}
		}
		return res, out, nil
	})
}

// field customises one property of an inferred input schema.
type field struct {
	Default any
	Enum    []any
}

// inputSchema infers the schema for In and applies defaults and enums, which
// struct tags cannot express. The SDK fills defaults in before decoding.
func inputSchema[In any](fields map[string]field) *jsonschema.Schema {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("infer input schema: %v", err))
	}
	for name, f := range fields {
		prop, ok := schema.Properties[name]
		if !ok {
			panic(fmt.Sprintf("input schema has no property %q", name))
		}
		if f.Default != nil {
			raw, err := json.Marshal(f.Default)
			if err != nil {
				panic(fmt.Sprintf("marshal default for %q: %v", name, err))
			}
			prop.Default = raw
		}
		if f.Enum != nil {
			prop.Enum = f.Enum
		}
	}
	return schema
}

// ActionResult is returned by tools whose upstream endpoint has no body.
type ActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func ptr[T any](v T) *T { return &v }
