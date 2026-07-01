package main

import (
	"bytes"
	"strings"
	"testing"
)

// Every build must be identifiable: buildVersion never returns empty fields,
// even without -ldflags (it falls back to Go's embedded build info).
func TestBuildVersionNeverEmpty(t *testing.T) {
	v, c, d := buildVersion()
	if v == "" || c == "" || d == "" {
		t.Fatalf("buildVersion returned an empty field: version=%q commit=%q date=%q", v, c, d)
	}
}

// -ldflags values (set by the Makefile) take precedence over build-info fallback.
func TestBuildVersionPrefersLdflags(t *testing.T) {
	oldV, oldC, oldD := version, commit, date
	t.Cleanup(func() { version, commit, date = oldV, oldC, oldD })

	version, commit, date = "v9.9.9", "deadbeef", "2026-01-02T03:04:05Z"
	v, c, d := buildVersion()
	if v != "v9.9.9" || c != "deadbeef" || d != "2026-01-02T03:04:05Z" {
		t.Errorf("ldflags not honoured: got version=%q commit=%q date=%q", v, c, d)
	}
}

// `axon version` and `axon version --short` print the resolved version.
func TestVersionCommandOutput(t *testing.T) {
	oldV := version
	t.Cleanup(func() { version = oldV })
	version = "v1.2.3"

	var buf bytes.Buffer
	cmd := newVersionCmd()
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "axon") || !strings.Contains(out, "v1.2.3") {
		t.Errorf("version output missing name/version:\n%s", out)
	}
	if !strings.Contains(out, "go:") {
		t.Errorf("version output missing build metadata:\n%s", out)
	}

	buf.Reset()
	short := newVersionCmd()
	short.SetArgs([]string{"--short"})
	short.SetOut(&buf)
	if err := short.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "v1.2.3" {
		t.Errorf("--short should print only the version, got %q", buf.String())
	}
}
