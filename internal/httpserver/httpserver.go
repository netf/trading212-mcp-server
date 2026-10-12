// Package httpserver serves an MCP handler over HTTP with bearer-token
// authentication, access logging, request limits and a health check.
package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/netf/trading212-mcp-server/internal/config"
)

const (
	// HeaderRequestID carries the request ID. A well-formed client value is
	// kept so requests can be correlated across a proxy; otherwise one is
	// generated. It is echoed in the response and the access log.
	HeaderRequestID = "X-Request-ID"
	// MaxRequestBodyBytes caps request bodies. MCP messages are small.
	MaxRequestBodyBytes = 1 << 20
	// HealthPath answers "ok" without authentication, for tunnels and probes.
	HealthPath = "/healthz"

	maxRequestIDLength = 64
	shutdownTimeout    = 5 * time.Second
)

// Options configures Handler.
type Options struct {
	// Path is where the MCP handler is mounted, e.g. "/mcp".
	Path string
	// Tokens are the accepted bearer tokens. Empty disables authentication;
	// config.Load only allows that on a loopback address.
	Tokens []config.AuthToken
	// Logger receives access logs. Nil discards them.
	Logger *slog.Logger
}

// Handler wraps mcpHandler for exposure over HTTP. Requests to the MCP path
// pass cross-origin protection, bearer-token authentication and a body limit;
// every request is access-logged with its status, latency and client name.
func Handler(mcpHandler http.Handler, opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	protected := http.MaxBytesHandler(mcpHandler, MaxRequestBodyBytes)
	if len(opts.Tokens) > 0 {
		protected = auth.RequireBearerToken(newTokenVerifier(opts.Tokens),
			&auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(protected)
	}

	mux := http.NewServeMux()
	mux.Handle(opts.Path, http.NewCrossOriginProtection().Handler(protected))
	mux.HandleFunc("GET "+HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	return accessLog(logger, mux)
}

// newTokenVerifier checks a presented token against every configured token
// in constant time. Comparing fixed-size SHA-256 digests hides the token
// length, and scanning all entries hides which one (if any) matched.
func newTokenVerifier(tokens []config.AuthToken) auth.TokenVerifier {
	type entry struct {
		name   string
		digest [sha256.Size]byte
	}
	entries := make([]entry, len(tokens))
	for i, t := range tokens {
		entries[i] = entry{name: t.Name, digest: sha256.Sum256([]byte(t.Secret))}
	}
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		presented := sha256.Sum256([]byte(token))
		match := -1
		for i := range entries {
			if subtle.ConstantTimeCompare(presented[:], entries[i].digest[:]) == 1 {
				match = i
			}
		}
		if match < 0 {
			if info := infoFrom(ctx); info != nil {
				info.authError = "invalid token"
			}
			return nil, auth.ErrInvalidToken
		}
		name := entries[match].name
		if info := infoFrom(ctx); info != nil {
			info.client = name
		}
		// UserID binds MCP sessions to the client that created them, so one
		// token cannot take over another client's session.
		return &auth.TokenInfo{UserID: name}, nil
	}
}

// requestInfo collects facts discovered deeper in the handler chain for the
// access log.
type requestInfo struct {
	client    string
	authError string
}

type infoKey struct{}

func infoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(infoKey{}).(*requestInfo)
	return info
}

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = rand.Text()
		}
		// Downstream handlers (including MCP tool middleware, via the
		// request headers the SDK exposes) see the same ID.
		r.Header.Set(HeaderRequestID, id)
		w.Header().Set(HeaderRequestID, id)

		info := &requestInfo{}
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), infoKey{}, info)))

		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			slog.Int64("bytes", rec.bytes),
			slog.String("remote", r.RemoteAddr),
			slog.String("request_id", id),
		}
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			attrs = append(attrs, slog.String("forwarded_for", fwd))
		}
		if ua := r.UserAgent(); ua != "" {
			attrs = append(attrs, slog.String("user_agent", ua))
		}
		if info.client != "" {
			attrs = append(attrs, slog.String("client", info.client))
		}
		if rec.status == http.StatusUnauthorized {
			reason := info.authError
			if reason == "" {
				reason = "missing or malformed bearer token"
			}
			attrs = append(attrs, slog.String("auth_error", reason))
		}
		logger.LogAttrs(r.Context(), accessLevel(r.URL.Path, rec.status), "http request", attrs...)
	})
}

func accessLevel(path string, status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	case path == HealthPath:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, r := range id {
		ok := 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// recorder captures the status and size of a response. It exposes the
// underlying writer through Unwrap so http.ResponseController can still
// flush streaming (SSE) responses.
type recorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *recorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err //nolint:wrapcheck // http.ResponseWriter contract: return the writer's error as is.
}

func (r *recorder) Flush() {
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Serve listens on addr and serves handler until ctx is cancelled, then shuts
// down gracefully. Write timeouts are left unset because MCP responses may be
// long-lived event streams.
func Serve(ctx context.Context, logger *slog.Logger, addr string, handler http.Handler) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutdown http: %w", err)
	}
	return nil
}
