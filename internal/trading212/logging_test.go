package trading212

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	s.mu.Lock()
	raw := s.buf.String()
	s.mu.Unlock()
	if strings.Contains(raw, testAPIKey) || strings.Contains(raw, "Basic ") {
		t.Fatalf("logs leaked credentials: %s", raw)
	}
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

func debugLogger(s *logSink) *slog.Logger {
	return slog.New(slog.NewJSONHandler(s, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestRequestsAreLoggedAtDebug(t *testing.T) {
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `[]`) })
	sink := &logSink{}
	c := newTestClient(t, srv, nil, WithLogger(debugLogger(sink)), WithCacheTTLs(TTLs{Account: time.Minute}))

	for range 2 {
		if _, err := c.Orders(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	reqs := sink.records(t, "trading212 request")
	if len(reqs) != 1 {
		t.Fatalf("want 1 upstream request record, got %d", len(reqs))
	}
	r := reqs[0]
	if r["level"] != "DEBUG" || r["method"] != "GET" || r["path"] != "/equity/orders" ||
		r["status"] != float64(200) || r["attempt"] != float64(1) {
		t.Errorf("record: %v", r)
	}
	if hits := sink.records(t, "trading212 cache hit"); len(hits) != 1 || hits[0]["path"] != "/equity/orders" {
		t.Errorf("cache hits: %v", hits)
	}
}

func TestRateLimitIsLoggedAsWarning(t *testing.T) {
	var n int
	var mu sync.Mutex
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	})
	sink := &logSink{}
	c, err := NewClient(testAPIKey, "secret", "demo", WithBaseURL(srv.URL+"/api/v0"),
		WithClock(nil, func(context.Context, time.Duration) error { return nil }), WithLogger(debugLogger(sink)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Orders(context.Background()); err != nil {
		t.Fatal(err)
	}
	warns := sink.records(t, "trading212 rate limited")
	if len(warns) != 1 || warns[0]["level"] != "WARN" || warns[0]["retry_after_s"] != float64(3) {
		t.Fatalf("rate-limit records: %v", warns)
	}
	if reqs := sink.records(t, "trading212 request"); len(reqs) != 2 || reqs[1]["attempt"] != float64(2) {
		t.Errorf("request records: %v", reqs)
	}
}

func TestConnectionFailuresAreLogged(t *testing.T) {
	_, srv := newFakeAPI(t, func(http.ResponseWriter, *http.Request) {})
	srv.Close()
	sink := &logSink{}
	c := newTestClient(t, srv, nil, WithLogger(debugLogger(sink)))
	if err := c.CancelOrder(context.Background(), 1); err == nil {
		t.Fatal("expected an error")
	}
	recs := sink.records(t, "trading212 request failed")
	if len(recs) != 1 || recs[0]["level"] != "WARN" || recs[0]["method"] != "DELETE" {
		t.Fatalf("records: %v", recs)
	}
}
