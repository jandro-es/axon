package main

import (
	"strings"
	"testing"
)

// TestRelatedCLIUnknownPathErrors: a path not in the index is a clean "not
// found" error (typo detection), needs no model/embedder — exits non-zero.
// Asserting the message rules out a false green from an unregistered command.
func TestRelatedCLIUnknownPathErrors(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := run(t, "related", "no-such-note.md", "--json", "--config", cfgPath)
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown note path")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want a 'not found' error, got: %v", err)
	}
}
