package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `service status` lets a GUI client render a "start at login" toggle without
// stat-ing plists or shelling to launchctl itself.
func TestServiceStatusReportsInstalledState(t *testing.T) {
	dir := t.TempDir()
	// The unit path is derived from the home directory, so without isolating
	// HOME this test would inspect (and could clobber) the developer's real
	// LaunchAgents — and would fail on any machine with AXON installed.
	t.Setenv("HOME", dir)
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	decode := func(t *testing.T, out string) serviceStatusJSON {
		t.Helper()
		var status serviceStatusJSON
		if err := json.Unmarshal([]byte(out), &status); err != nil {
			t.Fatalf("service status --json is not valid JSON: %v\n%s", err, out)
		}
		return status
	}

	// Before install: not installed, but the path is still reported so the
	// caller can say where it *would* go.
	out, err := run(t, "service", "status", "--json", "--config", cfgPath)
	if err != nil {
		t.Fatalf("service status: %v\n%s", err, out)
	}
	before := decode(t, out)
	if before.Installed {
		t.Errorf("expected no unit installed before `service install`:\n%s", out)
	}
	if before.Path == "" || before.Kind == "" {
		t.Errorf("service status must report kind and path even when absent:\n%s", out)
	}
	if before.Profile == "" {
		t.Errorf("service status missing profile:\n%s", out)
	}

	// Installing into the real LaunchAgents dir would touch the developer's
	// machine, so assert the transition by creating the unit file directly —
	// `installed` is defined as "the unit file exists".
	if !strings.HasPrefix(before.Path, dir) {
		t.Fatalf("unit path %q escaped the isolated HOME %q", before.Path, dir)
	}
	if err := os.MkdirAll(filepath.Dir(before.Path), 0o755); err != nil {
		t.Fatalf("cannot create unit dir: %v", err)
	}
	if err := os.WriteFile(before.Path, []byte("test unit"), 0o644); err != nil {
		t.Fatalf("cannot write unit: %v", err)
	}

	out, err = run(t, "service", "status", "--json", "--config", cfgPath)
	if err != nil {
		t.Fatalf("service status after install: %v\n%s", err, out)
	}
	if !decode(t, out).Installed {
		t.Errorf("expected installed=true once the unit exists:\n%s", out)
	}

	// The human renderer stays canonical and must not leak into the JSON.
	plain, err := run(t, "service", "status", "--config", cfgPath)
	if err != nil {
		t.Fatalf("service status (human): %v\n%s", err, plain)
	}
	if !strings.Contains(plain, "unit installed") {
		t.Errorf("human service status unexpected:\n%s", plain)
	}
	if strings.Contains(out, "unit installed") {
		t.Errorf("service status --json leaked human output:\n%s", out)
	}
}
