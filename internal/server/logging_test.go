package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// find returns log records with the given message and tool (if non-empty).
func (b *syncBuffer) find(t *testing.T, msg, tool string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		if m["msg"] == msg && (tool == "" || m["tool"] == tool) {
			out = append(out, m)
		}
	}
	return out
}

func (b *syncBuffer) one(t *testing.T, msg, tool string) map[string]any {
	t.Helper()
	recs := b.find(t, msg, tool)
	if len(recs) != 1 {
		t.Fatalf("want 1 %q record for %q, got %d:\n%s", msg, tool, len(recs), b.String())
	}
	return recs[0]
}

func loggingHarness(t *testing.T, level slog.Level) (*harness, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: level}))
	h := newHarnessWith(t, Options{Version: "test", Logger: logger})
	t.Cleanup(func() {
		if strings.Contains(logs.String(), "test-api-key-5f3c") {
			t.Errorf("logs leaked the API key:\n%s", logs.String())
		}
	})
	return h, logs
}

// TestSessionStartIsLogged sends a pre-2026 handshake request by hand,
// because the SDK client negotiates the newer protocol without one.
func TestSessionStartIsLogged(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	client, err := t212.NewClient("test-api-key-5f3c", "", "demo")
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := New(client, Options{Logger: logger}).Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	conn, err := clientTransport.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	id, err := jsonrpc.MakeID(float64(1))
	if err != nil {
		t.Fatal(err)
	}
	params := json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"grok","version":"4"}}`)
	if err := conn.Write(t.Context(), &jsonrpc.Request{ID: id, Method: "initialize", Params: params}); err != nil { //nolint:misspell // MCP method name.
		t.Fatal(err)
	}
	if _, err := conn.Read(t.Context()); err != nil {
		t.Fatal(err)
	}

	rec := logs.one(t, "session started", "")
	if rec["client_name"] != "grok" || rec["client_version"] != "4" || rec["protocol_version"] != "2025-06-18" {
		t.Errorf("record: %v", rec)
	}
}

func TestWriteToolCallsAreAudited(t *testing.T) {
	h, logs := loggingHarness(t, slog.LevelInfo)
	h.callOK(t, "place_market_order", map[string]any{"ticker": "AAPL_US_EQ", "quantity": 5}, nil)

	rec := logs.one(t, "tool call", "place_market_order")
	if rec["level"] != "INFO" || rec["write"] != true || rec["outcome"] != "ok" {
		t.Errorf("record: %v", rec)
	}
	args, _ := rec["args"].(map[string]any)
	if args["ticker"] != "AAPL_US_EQ" || args["quantity"] != float64(5) {
		t.Errorf("args: %v", rec["args"])
	}
	if _, ok := rec["duration_ms"].(float64); !ok {
		t.Errorf("missing duration: %v", rec)
	}
}

func TestReadToolArgsOnlyAtDebug(t *testing.T) {
	h, logs := loggingHarness(t, slog.LevelInfo)
	h.callOK(t, "fetch_order", map[string]any{"order_id": 11}, nil)
	rec := logs.one(t, "tool call", "fetch_order")
	if rec["level"] != "INFO" || rec["write"] != false {
		t.Errorf("record: %v", rec)
	}
	if _, ok := rec["args"]; ok {
		t.Errorf("read tool args logged at info: %v", rec)
	}

	h, logs = loggingHarness(t, slog.LevelDebug)
	h.callOK(t, "fetch_order", map[string]any{"order_id": 11}, nil)
	rec = logs.one(t, "tool call", "fetch_order")
	if args, _ := rec["args"].(map[string]any); args["order_id"] != float64(11) {
		t.Errorf("debug record should include args: %v", rec)
	}
}

func TestFailedToolCallsAreWarnings(t *testing.T) {
	h, logs := loggingHarness(t, slog.LevelInfo)
	h.status.Store(http.StatusInternalServerError)
	res := h.call(t, "cancel_order", map[string]any{"order_id": 11})
	if !res.IsError {
		t.Fatal("expected a tool error")
	}
	rec := logs.one(t, "tool call", "cancel_order")
	if rec["level"] != "WARN" || rec["outcome"] != "error" || rec["error"] == "" {
		t.Errorf("record: %v", rec)
	}
}

func TestResourceReadsAreLogged(t *testing.T) {
	h, logs := loggingHarness(t, slog.LevelInfo)
	if _, err := h.session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "trading212://account/summary"}); err != nil {
		t.Fatal(err)
	}
	rec := logs.one(t, "resource read", "")
	if rec["uri"] != "trading212://account/summary" || rec["outcome"] != "ok" {
		t.Errorf("record: %v", rec)
	}
}
