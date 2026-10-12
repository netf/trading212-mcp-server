package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
