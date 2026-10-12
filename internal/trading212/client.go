// Package trading212 is a typed client for the Trading 212 public equity API.
package trading212

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIVersion  = "v0"
	maxAttempts        = 3
	requestBudget      = 30 * time.Second
	attemptTimeout     = 10 * time.Second
	maxResponseBytes   = 64 << 20
	maxPageLimit       = 50
	defaultPageLimit   = 20
	userAgent          = "trading212-mcp-server (Go)"
	headerRetryAfter   = "Retry-After"
	headerRateLimitRst = "X-Ratelimit-Reset"
)

// BaseURL returns the API root for the "demo" or "live" environment.
func BaseURL(environment string) string {
	return fmt.Sprintf("https://%s.trading212.com/api/%s", environment, defaultAPIVersion)
}

// Client talks to the Trading 212 API. It is safe for concurrent use.
type Client struct {
	baseURL       string
	authorization string
	httpClient    *http.Client
	cache         *responseCache
	ttls          TTLs
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	logger        *slog.Logger
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL overrides the API root (useful for tests and proxies).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithCacheTTLs configures response caching. The default disables caching.
func WithCacheTTLs(ttls TTLs) Option {
	return func(c *Client) { c.ttls = ttls }
}

// WithLogger logs upstream requests at debug, and rate limiting and
// connection failures at warn. Credentials are never logged.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithClock overrides time and sleeping, for tests.
func WithClock(now func() time.Time, sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
		if sleep != nil {
			c.sleep = sleep
		}
	}
}

// NewClient creates a client. When apiSecret is non-empty, requests use HTTP
// Basic authentication with key:secret; otherwise the raw API key is sent in
// the Authorization header.
func NewClient(apiKey, apiSecret, environment string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: API key is required", ErrInvalidRequest)
	}
	switch environment {
	case "demo", "live":
	default:
		return nil, fmt.Errorf("%w: unknown environment %q", ErrInvalidRequest, environment)
	}
	auth := apiKey
	if apiSecret != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(apiKey+":"+apiSecret))
	}
	c := &Client{
		baseURL:       BaseURL(environment),
		authorization: auth,
		httpClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:    time.Now,
		sleep:  sleepContext,
		logger: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.cache = newResponseCache(c.now)
	return c, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait before retry: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

// do performs a request and decodes a JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	if method != http.MethodGet {
		// Conservatively invalidate even on failure: the mutation may have
		// been applied upstream before the error surfaced.
		defer c.cache.invalidate()
	}

	ttl := c.ttls.forPath(path)
	cacheable := method == http.MethodGet && ttl > 0
	metadata := strings.HasPrefix(path, "/equity/metadata/")
	key := method + " " + target
	if cacheable {
		if e, ok := c.cache.get(key, ttl, metadata); ok {
			c.logger.DebugContext(ctx, "trading212 cache hit", "path", path,
				"age_s", c.now().Sub(e.retrievedAt).Seconds())
			observe(ctx, CacheObservation{CacheHit: true, RetrievedAt: e.retrievedAt.UTC(), TTLSeconds: ttl.Seconds()})
			return decode(e.body, out)
		}
	}

	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encode %s %s request: %w", method, path, err)
		}
	}

	generation := c.cache.currentGeneration()
	respBody, err := c.send(ctx, method, path, target, payload)
	if err != nil {
		return err
	}
	retrievedAt := c.now()
	if len(respBody) > 0 && !json.Valid(respBody) {
		return fmt.Errorf("%w: %s %s returned invalid JSON", ErrInvalidResponse, method, path)
	}
	if cacheable {
		c.cache.put(key, respBody, retrievedAt, generation)
	}
	obsTTL := 0.0
	if method == http.MethodGet {
		obsTTL = ttl.Seconds()
	}
	observe(ctx, CacheObservation{CacheHit: false, RetrievedAt: retrievedAt.UTC(), TTLSeconds: obsTTL})
	if err := decode(respBody, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

func decode(body []byte, out any) error {
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	return nil
}

// send executes the HTTP exchange. Idempotent GETs are retried on transport
// errors, 429 and 5xx responses within a fixed time budget; mutations are
// never retried because their outcome would be ambiguous.
func (c *Client) send(ctx context.Context, method, path, target string, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestBudget)
	defer cancel()
	deadline := c.now().Add(requestBudget)

	for attempt := 0; ; attempt++ {
		start := c.now()
		body, status, apiErr, err := c.attempt(ctx, method, target, path, payload)
		c.logAttempt(ctx, method, path, attempt+1, c.now().Sub(start), status, apiErr, err)
		retryable := method == http.MethodGet && attempt < maxAttempts-1
		var delay time.Duration
		switch {
		case err == nil && apiErr == nil:
			return body, nil
		case err != nil:
			if ctxErr := context.Cause(ctx); ctxErr != nil && errors.Is(err, ctxErr) {
				return nil, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
			}
			if !retryable {
				if method != http.MethodGet {
					return nil, fmt.Errorf("%w: %s %s: connection failed and the outcome is unknown; do not retry automatically: %w",
						ErrUnavailable, method, path, err)
				}
				return nil, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
			}
			delay = backoff(attempt)
		default:
			if !retryable || !isRetryableStatus(apiErr.StatusCode) {
				return nil, apiErr
			}
			delay = apiErr.RetryAfter
			if delay <= 0 {
				delay = backoff(attempt)
			}
		}
		if c.now().Add(delay).After(deadline) {
			if apiErr != nil {
				return nil, apiErr
			}
			return nil, fmt.Errorf("%w: %s %s: retry would exceed the request budget", ErrUnavailable, method, path)
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
		}
	}
}

// logAttempt records one HTTP exchange with the API. It deliberately omits
// the query string, headers and bodies.
func (c *Client) logAttempt(ctx context.Context, method, path string, attempt int, d time.Duration,
	status int, apiErr *APIError, err error,
) {
	if err != nil {
		c.logger.WarnContext(ctx, "trading212 request failed", "method", method, "path", path,
			"attempt", attempt, "error", err.Error())
		return
	}
	c.logger.DebugContext(ctx, "trading212 request", "method", method, "path", path, "status", status,
		"attempt", attempt, "duration_ms", float64(d.Microseconds())/1000)
	if apiErr != nil && apiErr.StatusCode == http.StatusTooManyRequests {
		c.logger.WarnContext(ctx, "trading212 rate limited", "method", method, "path", path,
			"retry_after_s", apiErr.RetryAfter.Seconds())
	}
}

func (c *Client) attempt(ctx context.Context, method, target, path string, payload []byte) ([]byte, int, *APIError, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", c.authorization)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, resp.StatusCode, nil, nil
	}
	return nil, resp.StatusCode, &APIError{
		StatusCode: resp.StatusCode,
		Method:     method,
		Path:       path,
		Code:       errorCode(body),
		RetryAfter: retryAfter(resp.Header, c.now()),
	}, nil
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

// retryAfter honours Retry-After (seconds or HTTP date) and the
// x-ratelimit-reset header (Unix seconds) that Trading 212 sends.
func retryAfter(h http.Header, now time.Time) time.Duration {
	if v := h.Get(headerRetryAfter); v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(secs) && !math.IsInf(secs, 0) {
			return max(0, time.Duration(secs*float64(time.Second)))
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(0, t.Sub(now))
		}
	}
	if v := h.Get(headerRateLimitRst); v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(secs) && !math.IsInf(secs, 0) {
			reset := time.Unix(0, int64(secs*float64(time.Second)))
			return max(0, reset.Sub(now))
		}
	}
	return 0
}

// errorCode extracts a short machine-readable code from an error body without
// echoing arbitrary upstream text back to the caller.
func errorCode(body []byte) string {
	var e struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &e) != nil || len(e.Code) > 64 {
		return ""
	}
	for _, r := range e.Code {
		if !isCodeRune(r) {
			return ""
		}
	}
	return e.Code
}

func isCodeRune(r rune) bool {
	switch {
	case r == '_', r == '-', r == '.':
		return true
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	default:
		return false
	}
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultPageLimit
	}
	return min(limit, maxPageLimit)
}
