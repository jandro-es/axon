package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
)

func TestProfilesCommandShowsIsolation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	out, err := run(t, "profiles", "--json", "--config", cfgPath)
	if err != nil {
		t.Fatalf("profiles: %v\n%s", err, out)
	}
	var views []map[string]any
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("profiles --json not valid JSON: %v\n%s", err, out)
	}
	if len(views) == 0 {
		t.Fatal("no profiles listed")
	}
	// No secret values, only the env: reference.
	if strings.Contains(out, "sk-ant") {
		t.Error("profiles output leaked a secret value")
	}
}

func TestServicePrintGeneratesUnit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	out, err := run(t, "service", "print", "--config", cfgPath)
	if err != nil {
		t.Fatalf("service print: %v\n%s", err, out)
	}
	// Whatever the host OS, the output must reference the daemon start + profile.
	if !strings.Contains(out, "start") || !strings.Contains(out, "personal") {
		t.Errorf("service unit missing start/profile:\n%s", out)
	}
	if !strings.Contains(out, "install path:") {
		t.Errorf("service print missing install hint:\n%s", out)
	}
}

func TestExportProducesBundle(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	outDir := filepath.Join(dir, "bundle")
	out, err := run(t, "export", "--out", outDir, "--config", cfgPath)
	if err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}
	for _, f := range []string{"manifest.json", "core-context.md", "activity.json"} {
		if _, err := os.Stat(filepath.Join(outDir, f)); err != nil {
			t.Errorf("export missing %s: %v", f, err)
		}
	}
	// The manifest is valid, self-describing JSON.
	raw, _ := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest not valid JSON: %v", err)
	}
	if m["axon_export_version"] == nil || m["schema_version"] == nil || m["stats"] == nil {
		t.Errorf("manifest not self-describing: %v", m)
	}
}

// TestS8AllAutomationsOff verifies a fresh install with NO automations still
// initialises, schedules nothing, and supports manual search (S8/FR-07).
func TestS8AllAutomationsOff(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir) // its automations map is empty

	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init with no automations failed: %v", err)
	}
	// Nothing is scheduled.
	cfg, _ := config.Load(cfgPath)
	_, profile, _ := cfg.ResolveProfile("")
	if n := len(automations.Schedulables(profile)); n != 0 {
		t.Errorf("expected 0 schedulable automations, got %d", n)
	}
	// Manual search still works (returns cleanly, even if empty).
	if _, err := run(t, "search", "anything", "--config", cfgPath); err != nil {
		t.Errorf("manual search should work with automations off: %v", err)
	}
	// Status still works.
	if _, err := run(t, "status", "--config", cfgPath); err != nil {
		t.Errorf("status should work with automations off: %v", err)
	}
}
