package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxLoggedArgsBytes  = 4 << 10
	maxLoggedErrorChars = 500
	headerRequestID     = "X-Request-ID"
)

// logRequests logs every MCP request the server handles. Calls to tools in
// writes are logged at info with their arguments, as an audit trail of
// account changes; read tools include arguments only at debug.
func logRequests(logger *slog.Logger, writes map[string]bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			start := time.Now()
			res, err := next(ctx, method, req)
			duration := time.Since(start)

			msg, level := "mcp request", slog.LevelDebug
			attrs := []slog.Attr{slog.String("method", method)}
			attrs = append(attrs, identity(req)...)

			switch p := req.GetParams().(type) {
			case *mcp.InitializeParams:
				msg, level = "session started", slog.LevelInfo
				if p.ClientInfo != nil {
					attrs = append(attrs, slog.String("client_name", p.ClientInfo.Name),
						slog.String("client_version", p.ClientInfo.Version))
				}
				attrs = append(attrs, slog.String("protocol_version", p.ProtocolVersion))
			case *mcp.CallToolParamsRaw:
				msg, level = "tool call", slog.LevelInfo
				write := writes[p.Name]
				attrs = append(attrs, slog.String("tool", p.Name), slog.Bool("write", write))
				if write || logger.Enabled(ctx, slog.LevelDebug) {
					attrs = append(attrs, argsAttr(p.Arguments))
				}
			case *mcp.ReadResourceParams:
				msg, level = "resource read", slog.LevelInfo
				attrs = append(attrs, slog.String("uri", p.URI))
			}

			attrs = append(attrs, slog.Float64("duration_ms", float64(duration.Microseconds())/1000))
			if failure := failureText(res, err); failure != "" {
				level = max(level, slog.LevelWarn)
				attrs = append(attrs, slog.String("outcome", "error"), slog.String("error", failure))
			} else {
				attrs = append(attrs, slog.String("outcome", "ok"))
			}
			logger.LogAttrs(ctx, level, msg, attrs...)
			return res, err
		}
	}
}

// identity returns the authenticated client, session and request IDs, when
// the transport provides them.
func identity(req mcp.Request) []slog.Attr {
	var attrs []slog.Attr
	if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss != nil && ss.ID() != "" {
		attrs = append(attrs, slog.String("session", ss.ID()))
	}
	if extra := req.GetExtra(); extra != nil {
		if extra.TokenInfo != nil && extra.TokenInfo.UserID != "" {
			attrs = append(attrs, slog.String("client", extra.TokenInfo.UserID))
		}
		if id := extra.Header.Get(headerRequestID); id != "" {
			attrs = append(attrs, slog.String("request_id", id))
		}
	}
	return attrs
}

func argsAttr(raw json.RawMessage) slog.Attr {
	if len(raw) == 0 {
		return slog.Attr{Key: "args", Value: slog.AnyValue(map[string]any{})}
	}
	if len(raw) > maxLoggedArgsBytes {
		return slog.String("args", "(too large to log)")
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return slog.String("args", "(not a JSON object)")
	}
	return slog.Any("args", args)
}

// failureText describes a protocol error or a tool result flagged as an
// error. Tool error text comes from this server's own error messages, which
// never include credentials.
func failureText(res mcp.Result, err error) string {
	if err != nil {
		return truncate(err.Error())
	}
	r, ok := res.(*mcp.CallToolResult)
	if !ok || r == nil || !r.IsError {
		return ""
	}
	var parts []string
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	if len(parts) == 0 {
		return "tool returned an error"
	}
	return truncate(strings.Join(parts, " "))
}

func truncate(s string) string {
	if r := []rune(s); len(r) > maxLoggedErrorChars {
		return string(r[:maxLoggedErrorChars]) + "…"
	}
	return s
}
