package trading212

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Sentinel errors. Error messages never contain credentials or raw upstream
// bodies, so they are safe to surface to MCP clients.
var (
	ErrAuthentication  = errors.New("trading 212 rejected the API credentials")
	ErrPermission      = errors.New("trading 212 API key is missing the permission scope for this endpoint")
	ErrRateLimited     = errors.New("trading 212 rate limit reached")
	ErrInvalidResponse = errors.New("trading 212 returned an unexpected response")
	ErrNotFound        = errors.New("trading 212 resource not found")
	ErrInvalidRequest  = errors.New("invalid request")
	ErrUnavailable     = errors.New("trading 212 request failed")
	ErrTimeout         = errors.New("trading 212 request timed out")
)

// APIError is a non-2xx response from Trading 212. It unwraps to one of the
// sentinel errors so callers can use errors.Is.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	// Code is the machine-readable error code from the response body, if any
	// (for example "InsufficientFreeForStocksBuy").
	Code string
	// RetryAfter is the server-advised wait for 429 responses, if known.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("%s (%s %s: HTTP %d", e.Unwrap(), e.Method, e.Path, e.StatusCode)
	if e.Code != "" {
		msg += ", code " + e.Code
	}
	msg += ")"
	if e.StatusCode == http.StatusTooManyRequests && e.RetryAfter > 0 {
		msg += fmt.Sprintf("; retry after %s", e.RetryAfter.Round(time.Second))
	}
	if e.Method != http.MethodGet && (e.StatusCode == http.StatusRequestTimeout || e.StatusCode >= 500) {
		// Trading 212 documents order placement as non-idempotent.
		msg += "; the outcome is unknown, check orders/pies before retrying"
	}
	return msg
}

func (e *APIError) Unwrap() error {
	switch {
	case e.StatusCode == http.StatusUnauthorized:
		return ErrAuthentication
	case e.StatusCode == http.StatusForbidden:
		return ErrPermission
	case e.StatusCode == http.StatusRequestTimeout:
		return ErrTimeout
	case e.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case e.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case e.StatusCode >= 400 && e.StatusCode < 500:
		return ErrInvalidRequest
	default:
		return ErrUnavailable
	}
}
