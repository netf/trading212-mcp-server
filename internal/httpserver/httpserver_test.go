package httpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/netf/trading212-mcp-server/internal/config"
)

const (
	grokToken   = "grokTOKENgrokTOKENgrokTOKENgrokTOKEN0001"
	laptopToken = "laptopTOKENlaptopTOKENlaptopTOKENlaptop02"
)

var testTokens = []config.AuthToken{
	{Name: "grok", Secret: grokToken},
	{Name: "laptop", Secret: laptopToken},
}

// logBuffer is a concurrency-safe sink for JSON log records.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *logBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		out = append(out, m)
	}
	return out
}

// accessLog returns the single access-log record for path.
func (b *logBuffer) accessLog(t *testing.T, path string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, r := range b.records(t) {
		if r["msg"] == "http request" && r["path"] == path {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want 1 access log for %s, got %d: %s", path, len(found), b.String())
	}
	return found[0]
}

func newLogger(b *logBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// stub records the auth identity and request ID the MCP handler would see.
type stub struct {
	mu        sync.Mutex
	userID    string
	requestID string
	calls     int
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if ti := auth.TokenInfoFromContext(r.Context()); ti != nil {
		s.userID = ti.UserID
	}
	s.requestID = r.Header.Get(HeaderRequestID)
	if _, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	_, _ = io.WriteString(w, "mcp")
}

func newTestServer(t *testing.T, tokens []config.AuthToken) (*httptest.Server, *stub, *logBuffer) {
	t.Helper()
	logs := &logBuffer{}
	s := &stub{}
	srv := httptest.NewServer(Handler(s, Options{Path: "/mcp", Tokens: tokens, Logger: newLogger(logs)}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		for _, tok := range []string{grokToken, laptopToken} {
			if strings.Contains(logs.String(), tok) {
				t.Errorf("logs leaked a bearer token: %s", logs.String())
			}
		}
	})
	return srv, s, logs
}

// response is what do returns after reading and closing the body.
type response struct {
	StatusCode int
	Header     http.Header
}

func do(t *testing.T, method, url, authz string, body io.Reader, headers map[string]string) (response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

func TestAuthRejectsMissingAndMalformedCredentials(t *testing.T) {
	cases := map[string]string{
		"no header":     "",
		"basic scheme":  "Basic " + grokToken,
		"bearer only":   "Bearer",
		"extra fields":  "Bearer " + grokToken + " extra",
		"wrong token":   "Bearer " + strings.Repeat("x", 40),
		"token prefix":  "Bearer " + grokToken[:len(grokToken)-1],
		"token + extra": "Bearer " + grokToken + "1",
	}
	for name, authz := range cases {
		t.Run(name, func(t *testing.T) {
			srv, s, logs := newTestServer(t, testTokens)
			resp, _ := do(t, http.MethodPost, srv.URL+"/mcp", authz, strings.NewReader("{}"), nil)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", resp.StatusCode)
			}
			if s.calls != 0 {
				t.Fatal("MCP handler ran for an unauthenticated request")
			}
			rec := logs.accessLog(t, "/mcp")
			if rec["level"] != "WARN" || rec["status"] != float64(401) || rec["auth_error"] == "" {
				t.Errorf("access log: %v", rec)
			}
			if _, ok := rec["client"]; ok {
				t.Errorf("unauthenticated request logged a client: %v", rec)
			}
		})
	}
}

func TestAuthAcceptsEachConfiguredToken(t *testing.T) {
	for _, tok := range testTokens {
		t.Run(tok.Name, func(t *testing.T) {
			srv, s, logs := newTestServer(t, testTokens)
			resp, body := do(t, http.MethodPost, srv.URL+"/mcp", "bearer "+tok.Secret, strings.NewReader("{}"), nil)
			if resp.StatusCode != http.StatusOK || body != "mcp" {
				t.Fatalf("status %d body %q", resp.StatusCode, body)
			}
			if s.userID != tok.Name {
				t.Errorf("MCP handler saw user %q, want %q", s.userID, tok.Name)
			}
			rec := logs.accessLog(t, "/mcp")
			if rec["client"] != tok.Name || rec["level"] != "INFO" || rec["method"] != "POST" {
				t.Errorf("access log: %v", rec)
			}
		})
	}
}

func TestAuthDisabledPassesThrough(t *testing.T) {
	srv, s, _ := newTestServer(t, nil)
	resp, _ := do(t, http.MethodPost, srv.URL+"/mcp", "", strings.NewReader("{}"), nil)
	if resp.StatusCode != http.StatusOK || s.calls != 1 {
		t.Fatalf("status %d calls %d", resp.StatusCode, s.calls)
	}
}

func TestHealthzNeedsNoAuth(t *testing.T) {
	srv, s, logs := newTestServer(t, testTokens)
	resp, body := do(t, http.MethodGet, srv.URL+"/healthz", "", nil, nil)
	if resp.StatusCode != http.StatusOK || body != "ok\n" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if s.calls != 0 {
		t.Fatal("health check reached the MCP handler")
	}
	if rec := logs.accessLog(t, "/healthz"); rec["level"] != "DEBUG" {
		t.Errorf("health checks should log at debug: %v", rec)
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	srv, _, _ := newTestServer(t, testTokens)
	resp, _ := do(t, http.MethodGet, srv.URL+"/mcp/../secret", "", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, _ = do(t, http.MethodGet, srv.URL+"/other", "Bearer "+grokToken, nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestBodyLimit(t *testing.T) {
	srv, _, _ := newTestServer(t, testTokens)
	big := bytes.NewReader(make([]byte, MaxRequestBodyBytes+1))
	resp, _ := do(t, http.MethodPost, srv.URL+"/mcp", "Bearer "+grokToken, big, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", resp.StatusCode)
	}
}

func TestCrossOriginRequestsRejected(t *testing.T) {
	srv, s, _ := newTestServer(t, testTokens)
	resp, _ := do(t, http.MethodPost, srv.URL+"/mcp", "Bearer "+grokToken, strings.NewReader("{}"),
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != http.StatusForbidden || s.calls != 0 {
		t.Fatalf("status %d calls %d", resp.StatusCode, s.calls)
	}
}

func TestRequestID(t *testing.T) {
	srv, s, logs := newTestServer(t, testTokens)

	resp, _ := do(t, http.MethodPost, srv.URL+"/mcp", "Bearer "+grokToken, strings.NewReader("{}"),
		map[string]string{HeaderRequestID: "abc-123"})
	if got := resp.Header.Get(HeaderRequestID); got != "abc-123" || s.requestID != "abc-123" {
		t.Fatalf("supplied ID not kept: response %q, handler %q", got, s.requestID)
	}
	if rec := logs.accessLog(t, "/mcp"); rec["request_id"] != "abc-123" {
		t.Errorf("access log: %v", rec)
	}

	for _, bad := range []string{"", "has space", strings.Repeat("a", 65), "bad/slash", "ünicode"} {
		resp, _ = do(t, http.MethodGet, srv.URL+"/healthz", "", nil, map[string]string{HeaderRequestID: bad})
		got := resp.Header.Get(HeaderRequestID)
		if got == "" || got == bad {
			t.Errorf("%q: want a generated ID, got %q", bad, got)
		}
	}
}

func TestForwardedForIsLogged(t *testing.T) {
	srv, _, logs := newTestServer(t, testTokens)
	do(t, http.MethodPost, srv.URL+"/mcp", "", nil, map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if rec := logs.accessLog(t, "/mcp"); rec["forwarded_for"] != "203.0.113.7" {
		t.Errorf("access log: %v", rec)
	}
}

// TestRealMCPHandler checks the auth middleware in front of the SDK's
// streamable HTTP handler, using the SDK client.
func TestRealMCPHandler(t *testing.T) {
	logs := &logBuffer{}
	server := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "whoami"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		user := ""
		if ti := req.GetExtra().TokenInfo; ti != nil {
			user = ti.UserID
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: user}}}, struct{}{}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
	srv := httptest.NewServer(Handler(mcpHandler, Options{Path: "/mcp", Tokens: testTokens, Logger: newLogger(logs)}))
	t.Cleanup(srv.Close)

	connect := func(token string) (*mcp.ClientSession, error) {
		client := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil)
		hc := &http.Client{Transport: bearer{token: token}}
		return client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: hc}, nil)
	}

	if _, err := connect("wrong" + strings.Repeat("x", 40)); err == nil {
		t.Fatal("connected with a wrong token")
	}

	session, err := connect(grokToken)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := res.Content[0].(*mcp.TextContent).Text; got != "grok" {
		t.Errorf("tool saw user %q, want grok", got)
	}
	if strings.Contains(logs.String(), grokToken) {
		t.Errorf("logs leaked the token")
	}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestServeShutsDownOnCancel(t *testing.T) {
	logs := &logBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, newLogger(logs), "127.0.0.1:0", http.NotFoundHandler())
	}()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}
