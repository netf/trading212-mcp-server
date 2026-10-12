package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	}
	if cfg != want {
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
	s := Config{APIKey: "super-secret-key", APISecret: "super-secret"}.String()
	if strings.Contains(s, "super-secret") {
		t.Fatalf("String leaked credentials: %s", s)
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
