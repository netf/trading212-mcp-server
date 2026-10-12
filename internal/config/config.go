// Package config loads and validates the server configuration from the
// process environment and an optional .env file.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
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
}

// String never includes credentials.
func (c Config) String() string {
	return fmt.Sprintf("Config{Environment:%s Transport:%s HTTPAddr:%s AccountTTL:%s HistoryTTL:%s MetadataTTL:%s}",
		c.Environment, c.Transport, c.HTTPAddr, c.AccountTTL, c.HistoryTTL, c.MetadataTTL)
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
