// Package config loads and validates the server configuration from the
// process environment and an optional .env file.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment selects which Trading 212 API host is used.
type Environment string

const (
	EnvironmentDemo Environment = "demo"
	EnvironmentLive Environment = "live"
)

// Transport selects how the MCP server talks to its client.
type Transport string

const (
	TransportStdio          Transport = "stdio"
	TransportSSE            Transport = "sse"
	TransportStreamableHTTP Transport = "streamable-http"
)

// LogFormat selects how log records are rendered.
type LogFormat string

const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

// MinAuthTokenLength is the shortest bearer token accepted, so tokens carry
// enough entropy that guessing one is infeasible.
const MinAuthTokenLength = 32

const (
	DefaultHTTPAddr    = "127.0.0.1:8000"
	DefaultAccountTTL  = 15 * time.Second
	DefaultHistoryTTL  = 300 * time.Second
	DefaultMetadataTTL = 3600 * time.Second
)

// Config is the validated startup configuration.
type Config struct {
	APIKey      string
	APISecret   string
	Environment Environment
	Transport   Transport
	HTTPAddr    string
	AccountTTL  time.Duration
	HistoryTTL  time.Duration
	MetadataTTL time.Duration
	// AuthTokens are the bearer tokens accepted by the HTTP transports.
	AuthTokens []AuthToken
	// AuthDisabled explicitly allows the HTTP transports to run without
	// authentication on a loopback address.
	AuthDisabled bool
	LogLevel     slog.Level
	LogFormat    LogFormat
}

// AuthToken is a named bearer token. The name identifies the client in logs.
type AuthToken struct {
	Name   string
	Secret string
}

// String never includes the secret.
func (t AuthToken) String() string { return t.Name + ":[REDACTED]" }

// GoString never includes the secret.
func (t AuthToken) GoString() string { return t.String() }

// HTTP reports whether the transport serves over HTTP.
func (t Transport) HTTP() bool { return t == TransportSSE || t == TransportStreamableHTTP }

// AuthClientNames lists the names of the configured auth tokens.
func (c Config) AuthClientNames() []string {
	names := make([]string, 0, len(c.AuthTokens))
	for _, t := range c.AuthTokens {
		names = append(names, t.Name)
	}
	return names
}

// String never includes credentials.
func (c Config) String() string {
	return fmt.Sprintf("Config{Environment:%s Transport:%s HTTPAddr:%s AccountTTL:%s HistoryTTL:%s MetadataTTL:%s "+
		"AuthClients:%v AuthDisabled:%t LogLevel:%s LogFormat:%s}",
		c.Environment, c.Transport, c.HTTPAddr, c.AccountTTL, c.HistoryTTL, c.MetadataTTL,
		c.AuthClientNames(), c.AuthDisabled, c.LogLevel, c.LogFormat)
}

// LookupFunc matches the signature of os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// Load builds a Config from lookup. Values from dotenv are used only for keys
// that lookup does not define, so real environment variables always win.
func Load(lookup LookupFunc, dotenv map[string]string) (Config, error) {
	get := func(key, def string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		if v, ok := dotenv[key]; ok && v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		APIKey:      strings.TrimSpace(get("TRADING212_API_KEY", "")),
		APISecret:   strings.TrimSpace(get("TRADING212_API_SECRET", "")),
		Environment: Environment(strings.ToLower(get("ENVIRONMENT", string(EnvironmentDemo)))),
		Transport:   Transport(strings.ToLower(get("TRANSPORT", string(TransportStdio)))),
		HTTPAddr:    get("TRADING212_HTTP_ADDR", DefaultHTTPAddr),
		LogFormat:   LogFormat(strings.ToLower(get("LOG_FORMAT", string(LogFormatText)))),
	}

	var errs []error
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("TRADING212_API_KEY must be set"))
	}
	switch cfg.Environment {
	case EnvironmentDemo, EnvironmentLive:
	default:
		errs = append(errs, fmt.Errorf("ENVIRONMENT must be %q or %q, got %q", EnvironmentDemo, EnvironmentLive, cfg.Environment))
	}
	switch cfg.Transport {
	case TransportStdio, TransportSSE, TransportStreamableHTTP:
	default:
		errs = append(errs, fmt.Errorf("TRANSPORT must be %q, %q or %q, got %q",
			TransportStdio, TransportSSE, TransportStreamableHTTP, cfg.Transport))
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, errors.New("LOG_LEVEL must be debug, info, warn or error"))
	}
	switch cfg.LogFormat {
	case LogFormatText, LogFormatJSON:
	default:
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be %q or %q, got %q", LogFormatText, LogFormatJSON, cfg.LogFormat))
	}

	if raw := get("MCP_AUTH_TOKENS", ""); raw != "" {
		tokens, err := parseAuthTokens(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("MCP_AUTH_TOKENS: %w", err))
		}
		cfg.AuthTokens = tokens
	}
	if raw := get("MCP_AUTH_DISABLED", ""); raw != "" {
		disabled, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("MCP_AUTH_DISABLED must be true or false, got %q", raw))
		}
		cfg.AuthDisabled = disabled
	}
	if err := cfg.validateAuth(); err != nil {
		errs = append(errs, err)
	}

	ttls := []struct {
		key string
		def time.Duration
		dst *time.Duration
	}{
		{"TRADING212_ACCOUNT_TTL", DefaultAccountTTL, &cfg.AccountTTL},
		{"TRADING212_HISTORY_TTL", DefaultHistoryTTL, &cfg.HistoryTTL},
		{"TRADING212_METADATA_TTL", DefaultMetadataTTL, &cfg.MetadataTTL},
	}
	for _, t := range ttls {
		d, err := parseSeconds(get(t.key, ""), t.def)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.key, err))
			continue
		}
		*t.dst = d
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// validateAuth fails closed: the HTTP transports need tokens unless
// authentication is explicitly disabled on a loopback-only address. Loopback
// alone is not treated as trusted, because tunnels such as cloudflared
// forward public traffic from 127.0.0.1.
func (c Config) validateAuth() error {
	if c.AuthDisabled {
		if len(c.AuthTokens) > 0 {
			return errors.New("set MCP_AUTH_TOKENS or MCP_AUTH_DISABLED, not both")
		}
		if c.Transport.HTTP() && !isLoopbackAddr(c.HTTPAddr) {
			return fmt.Errorf("MCP_AUTH_DISABLED is only allowed when TRADING212_HTTP_ADDR is a loopback address, got %q", c.HTTPAddr)
		}
		return nil
	}
	if c.Transport.HTTP() && len(c.AuthTokens) == 0 {
		return fmt.Errorf("MCP_AUTH_TOKENS must be set for the %s transport "+
			"(or set MCP_AUTH_DISABLED=true to serve without authentication on a loopback address)", c.Transport)
	}
	return nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseAuthTokens parses comma-separated NAME:TOKEN pairs. Errors identify
// entries by position and never echo token values.
func parseAuthTokens(raw string) ([]AuthToken, error) {
	entries := strings.Split(raw, ",")
	tokens := make([]AuthToken, 0, len(entries))
	names := map[string]bool{}
	secrets := map[string]string{}
	for i, entry := range entries {
		n := i + 1
		name, secret, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			return nil, fmt.Errorf("entry %d: expected NAME:TOKEN", n)
		}
		if !validTokenName(name) {
			return nil, fmt.Errorf("entry %d: name must be 1-64 letters, digits, '.', '_' or '-'", n)
		}
		if len(secret) < MinAuthTokenLength {
			return nil, fmt.Errorf("entry %d (%s): token must be at least %d characters; generate one with `openssl rand -hex 32`",
				n, name, MinAuthTokenLength)
		}
		if !validTokenSecret(secret) {
			return nil, fmt.Errorf("entry %d (%s): token may only contain letters, digits and -._~+/=", n, name)
		}
		if names[name] {
			return nil, fmt.Errorf("entry %d: duplicate name %q", n, name)
		}
		if other, dup := secrets[secret]; dup {
			return nil, fmt.Errorf("entry %d (%s): reuses the token of %q; give each client its own token", n, name, other)
		}
		names[name] = true
		secrets[secret] = name
		tokens = append(tokens, AuthToken{Name: name, Secret: secret})
	}
	return tokens, nil
}

func validTokenName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !isAlphanumeric(r) && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// validTokenSecret accepts the RFC 6750 b64token alphabet, which is what a
// client can send in an Authorization: Bearer header.
func validTokenSecret(secret string) bool {
	for _, r := range secret {
		if !isAlphanumeric(r) && !strings.ContainsRune("-._~+/=", r) {
			return false
		}
	}
	return true
}

func isAlphanumeric(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
}

func parseSeconds(raw string, def time.Duration) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a number of seconds, got %q", raw)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, fmt.Errorf("must be a finite, non-negative number of seconds, got %q", raw)
	}
	return time.Duration(f * float64(time.Second)), nil
}

// ReadDotEnv reads KEY=VALUE pairs from path. A missing file is not an error.
func ReadDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	values, err := ParseDotEnv(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return values, nil
}

// ParseDotEnv parses a minimal .env format: blank lines and # comments are
// skipped, an optional "export " prefix is allowed, and values may be wrapped
// in single or double quotes.
func ParseDotEnv(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", lineNo)
		}
		value = strings.TrimSpace(value)
		if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
			value = value[1 : n-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return values, nil
}
