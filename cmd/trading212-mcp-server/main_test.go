package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netf/trading212-mcp-server/internal/config"
)

func TestRunVersion(t *testing.T) {
	var stderr bytes.Buffer
	if err := run([]string{"-version"}, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stderr.String()) != version {
		t.Errorf("got %q", stderr.String())
	}
}

func TestRunRequiresAPIKey(t *testing.T) {
	t.Setenv("TRADING212_API_KEY", "")
	var stderr bytes.Buffer
	err := run([]string{"-env-file", filepath.Join(t.TempDir(), "none.env")}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "TRADING212_API_KEY") {
		t.Fatalf("got %v", err)
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	var stderr bytes.Buffer
	if err := run([]string{"-nope"}, &stderr); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunRefusesUnauthenticatedHTTP(t *testing.T) {
	t.Setenv("TRADING212_API_KEY", "k")
	t.Setenv("TRANSPORT", "streamable-http")
	t.Setenv("MCP_AUTH_TOKENS", "")
	var stderr bytes.Buffer
	err := run([]string{"-env-file", filepath.Join(t.TempDir(), "none.env")}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKENS must be set") {
		t.Fatalf("got %v", err)
	}
}

func TestNewLoggerFormats(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, slog.LevelInfo, config.LogFormatJSON).Info("hello", "k", "v")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["msg"] != "hello" || rec["k"] != "v" {
		t.Fatalf("json: %q (%v)", buf.String(), err)
	}

	buf.Reset()
	l := newLogger(&buf, slog.LevelWarn, config.LogFormatText)
	l.Info("hidden")
	l.Warn("shown")
	if out := buf.String(); strings.Contains(out, "hidden") || !strings.Contains(out, "msg=shown") {
		t.Fatalf("text: %q", out)
	}
}

func TestStartupLogNamesClientsNotTokens(t *testing.T) {
	const secret = "abcdefghijklmnopqrstuvwxyz0123456789"
	var buf bytes.Buffer
	logStartup(newLogger(&buf, slog.LevelDebug, config.LogFormatText), config.Config{
		APIKey:      "api-key-should-not-appear",
		Environment: config.EnvironmentLive,
		Transport:   config.TransportStreamableHTTP,
		HTTPAddr:    "127.0.0.1:8000",
		AuthTokens:  []config.AuthToken{{Name: "grok", Secret: secret}},
	})
	out := buf.String()
	if strings.Contains(out, secret) || strings.Contains(out, "api-key-should-not-appear") {
		t.Fatalf("startup log leaked a secret: %s", out)
	}
	for _, want := range []string{"auth_clients=[grok]", "environment=live", "serving a live account over HTTP"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}
