package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunKnowledgeReindexSkipsSecondTime(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	// First run reindexes (vault changed since the empty baseline).
	out, err := run(t, "run", "knowledge-reindex", "--config", cfgPath)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "knowledge-reindex") {
		t.Errorf("unexpected output:\n%s", out)
	}

	// Second run: nothing changed → skip, no model call (S3).
	out, err = run(t, "run", "knowledge-reindex", "--config", cfgPath)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipped") {
		t.Errorf("second reindex should skip:\n%s", out)
	}
}

func TestRunContextExportWritesSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := run(t, "run", "context-export", "--config", cfgPath)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "exported snapshot") {
		t.Errorf("expected export confirmation:\n%s", out)
	}
}

func TestRunUnknownAutomationErrors(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, "run", "does-not-exist", "--config", cfgPath); err == nil {
		t.Error("expected error for unknown automation")
	}
}

func TestRunRespectsPolicyAllowlist(t *testing.T) {
	dir := t.TempDir()
	// Start from the standard config, then restrict the allow-list.
	base := writeTempConfig(t, dir) // writes axon.config.yaml and returns its path
	raw, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	restricted := strings.Replace(string(raw), `allowed_automations: ["*"]`, `allowed_automations: ["heartbeat"]`, 1)
	cfgPath := filepath.Join(dir, "restricted.yaml")
	if err := os.WriteFile(cfgPath, []byte(restricted), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, "run", "compaction", "--config", cfgPath); err == nil {
		t.Error("expected policy refusal for a non-allowed automation")
	}
}
