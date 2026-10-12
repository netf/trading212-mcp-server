package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Test tokens are long enough to pass validation and distinctive enough to
// spot if they ever leak into output.
const (
	tokenA = "tokenAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	tokenB = "tokenBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func lookupFrom(m map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{"TRADING212_API_KEY": "key"}), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		APIKey:      "key",
		Environment: EnvironmentDemo,
		Transport:   TransportStdio,
		HTTPAddr:    DefaultHTTPAddr,
		AccountTTL:  DefaultAccountTTL,
		HistoryTTL:  DefaultHistoryTTL,
		MetadataTTL: DefaultMetadataTTL,
		LogLevel:    slog.LevelInfo,
		LogFormat:   LogFormatText,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadOverridesAndDotEnvPrecedence(t *testing.T) {
	env := map[string]string{
		"TRADING212_API_KEY":     "env-key",
		"ENVIRONMENT":            "LIVE",
		"TRANSPORT":              "streamable-http",
		"TRADING212_ACCOUNT_TTL": "0",
		"TRADING212_HISTORY_TTL": "1.5",
		"MCP_AUTH_TOKENS":        "grok:" + tokenA,
	}
	dotenv := map[string]string{
		"TRADING212_API_KEY":      "file-key",
		"TRADING212_API_SECRET":   "file-secret",
		"TRADING212_METADATA_TTL": "60",
		"TRADING212_HTTP_ADDR":    "127.0.0.1:9999",
	}
	cfg, err := Load(lookupFrom(env), dotenv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "env-key" || cfg.APISecret != "file-secret" {
		t.Errorf("credentials: got key=%q secret=%q", cfg.APIKey, cfg.APISecret)
	}
	if cfg.Environment != EnvironmentLive || cfg.Transport != TransportStreamableHTTP {
		t.Errorf("env/transport: got %q/%q", cfg.Environment, cfg.Transport)
	}
	if cfg.AccountTTL != 0 || cfg.HistoryTTL != 1500*time.Millisecond || cfg.MetadataTTL != time.Minute {
		t.Errorf("ttls: got %v %v %v", cfg.AccountTTL, cfg.HistoryTTL, cfg.MetadataTTL)
	}
	if cfg.HTTPAddr != "127.0.0.1:9999" {
		t.Errorf("addr: got %q", cfg.HTTPAddr)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"missing key":     {map[string]string{}, "TRADING212_API_KEY"},
		"blank key":       {map[string]string{"TRADING212_API_KEY": "   "}, "TRADING212_API_KEY"},
		"bad environment": {map[string]string{"TRADING212_API_KEY": "k", "ENVIRONMENT": "prod"}, "ENVIRONMENT"},
		"bad transport":   {map[string]string{"TRADING212_API_KEY": "k", "TRANSPORT": "ws"}, "TRANSPORT"},
		"negative ttl":    {map[string]string{"TRADING212_API_KEY": "k", "TRADING212_ACCOUNT_TTL": "-1"}, "TRADING212_ACCOUNT_TTL"},
		"nan ttl":         {map[string]string{"TRADING212_API_KEY": "k", "TRADING212_HISTORY_TTL": "NaN"}, "TRADING212_HISTORY_TTL"},
		"text ttl":        {map[string]string{"TRADING212_API_KEY": "k", "TRADING212_METADATA_TTL": "soon"}, "TRADING212_METADATA_TTL"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(lookupFrom(tc.env), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error mentioning %s", err, tc.want)
			}
		})
	}
}

func TestStringHidesSecrets(t *testing.T) {
	s := Config{
		APIKey:     "super-secret-key",
		APISecret:  "super-secret",
		AuthTokens: []AuthToken{{Name: "grok", Secret: tokenA}},
	}.String()
	if strings.Contains(s, "super-secret") || strings.Contains(s, tokenA) {
		t.Fatalf("String leaked credentials: %s", s)
	}
	if !strings.Contains(s, "grok") {
		t.Errorf("String should name auth clients: %s", s)
	}
}

func TestAuthTokenStringHidesSecret(t *testing.T) {
	tok := AuthToken{Name: "grok", Secret: tokenA}
	for _, s := range []string{tok.String(), fmt.Sprint(tok), fmt.Sprintf("%v %+v", tok, tok)} {
		if strings.Contains(s, tokenA) {
			t.Fatalf("leaked secret: %s", s)
		}
	}
}

func TestLoadAuthTokens(t *testing.T) {
	env := map[string]string{
		"TRADING212_API_KEY": "k",
		"TRANSPORT":          "streamable-http",
		"MCP_AUTH_TOKENS":    " grok:" + tokenA + " , laptop.home:" + tokenB + " ",
	}
	cfg, err := Load(lookupFrom(env), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []AuthToken{{Name: "grok", Secret: tokenA}, {Name: "laptop.home", Secret: tokenB}}
	if !reflect.DeepEqual(cfg.AuthTokens, want) {
		t.Fatalf("got %v, want %v", cfg.AuthTokens, want)
	}
}

func TestLoadLogging(t *testing.T) {
	env := map[string]string{"TRADING212_API_KEY": "k", "LOG_LEVEL": "DEBUG", "LOG_FORMAT": "JSON"}
	cfg, err := Load(lookupFrom(env), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != LogFormatJSON {
		t.Fatalf("got level=%v format=%q", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestLoadAuthDisabledOnLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8000", "localhost:8000", "[::1]:8000"} {
		env := map[string]string{
			"TRADING212_API_KEY":   "k",
			"TRANSPORT":            "sse",
			"MCP_AUTH_DISABLED":    "true",
			"TRADING212_HTTP_ADDR": addr,
		}
		cfg, err := Load(lookupFrom(env), nil)
		if err != nil {
			t.Fatalf("%s: Load: %v", addr, err)
		}
		if !cfg.AuthDisabled || len(cfg.AuthTokens) != 0 {
			t.Fatalf("%s: got %+v", addr, cfg)
		}
	}
}

func TestLoadAuthErrors(t *testing.T) {
	http := func(extra map[string]string) map[string]string {
		m := map[string]string{"TRADING212_API_KEY": "k", "TRANSPORT": "streamable-http"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"http without tokens":   {http(nil), "MCP_AUTH_TOKENS must be set"},
		"sse without tokens":    {map[string]string{"TRADING212_API_KEY": "k", "TRANSPORT": "sse"}, "MCP_AUTH_TOKENS must be set"},
		"missing name":          {http(map[string]string{"MCP_AUTH_TOKENS": ":" + tokenA}), "entry 1: name"},
		"missing separator":     {http(map[string]string{"MCP_AUTH_TOKENS": tokenA}), "entry 1: expected NAME:TOKEN"},
		"empty entry":           {http(map[string]string{"MCP_AUTH_TOKENS": "grok:" + tokenA + ",,"}), "entry 2: expected NAME:TOKEN"},
		"bad name":              {http(map[string]string{"MCP_AUTH_TOKENS": "gr ok:" + tokenA}), "entry 1: name"},
		"short token":           {http(map[string]string{"MCP_AUTH_TOKENS": "grok:short"}), "at least 32"},
		"bad token characters":  {http(map[string]string{"MCP_AUTH_TOKENS": "grok:" + tokenA + "\"" + tokenB}), "may only contain"},
		"duplicate name":        {http(map[string]string{"MCP_AUTH_TOKENS": "grok:" + tokenA + ",grok:" + tokenB}), `duplicate name "grok"`},
		"duplicate token":       {http(map[string]string{"MCP_AUTH_TOKENS": "a:" + tokenA + ",b:" + tokenA}), "reuses the token"},
		"bad disabled flag":     {http(map[string]string{"MCP_AUTH_DISABLED": "maybe"}), "MCP_AUTH_DISABLED"},
		"disabled with tokens":  {http(map[string]string{"MCP_AUTH_DISABLED": "true", "MCP_AUTH_TOKENS": "grok:" + tokenA}), "not both"},
		"disabled on all hosts": {http(map[string]string{"MCP_AUTH_DISABLED": "true", "TRADING212_HTTP_ADDR": ":8000"}), "loopback"},
		"disabled on public ip": {http(map[string]string{"MCP_AUTH_DISABLED": "true", "TRADING212_HTTP_ADDR": "0.0.0.0:8000"}), "loopback"},
		"bad log level":         {map[string]string{"TRADING212_API_KEY": "k", "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		"bad log format":        {map[string]string{"TRADING212_API_KEY": "k", "LOG_FORMAT": "xml"}, "LOG_FORMAT"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(lookupFrom(tc.env), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error mentioning %q", err, tc.want)
			}
			for _, secret := range []string{tokenA, tokenB} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked a token: %v", err)
				}
			}
		})
	}
}

func TestParseDotEnv(t *testing.T) {
	in := `
# comment
TRADING212_API_KEY=abc
export ENVIRONMENT="live"
TRANSPORT='sse'
TRADING212_ACCOUNT_TTL=30 # inline comment
EMPTY=
`
	got, err := ParseDotEnv(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseDotEnv: %v", err)
	}
	want := map[string]string{
		"TRADING212_API_KEY":     "abc",
		"ENVIRONMENT":            "live",
		"TRANSPORT":              "sse",
		"TRADING212_ACCOUNT_TTL": "30",
		"EMPTY":                  "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}

	if _, err := ParseDotEnv(strings.NewReader("not a pair")); err == nil {
		t.Error("expected error for malformed line")
	}
}

func TestReadDotEnv(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadDotEnv(filepath.Join(dir, "missing.env"))
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: got %v, %v", got, err)
	}
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadDotEnv(path)
	if err != nil || got["A"] != "1" {
		t.Fatalf("got %v, %v", got, err)
	}
}
