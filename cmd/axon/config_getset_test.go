package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigGet(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	cases := map[string]string{
		"models.synthesis":    "o",
		"active_profile":      "personal",
		"limits.daily_tokens": "1000000",
	}
	for key, want := range cases {
		out, err := run(t, "config", "get", key, "--config", cfgPath)
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		if strings.TrimSpace(out) != want {
			t.Errorf("get %s = %q, want %q", key, strings.TrimSpace(out), want)
		}
	}

	if _, err := run(t, "config", "get", "limits.nope", "--config", cfgPath); err == nil {
		t.Error("get of a missing key should error")
	}
}

func TestConfigSetPreservesAndValidates(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	out, err := run(t, "config", "set", "limits.daily_tokens", "2222222", "--config", cfgPath)
	if err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}
	got, err := run(t, "config", "get", "limits.daily_tokens", "--config", cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != "2222222" {
		t.Errorf("after set, get = %q, want 2222222", strings.TrimSpace(got))
	}
	// The file must still validate.
	if _, err := run(t, "config", "validate", "--config", cfgPath); err != nil {
		t.Errorf("config invalid after set: %v", err)
	}
}

func TestConfigSetRejectsInvalidAndMissingKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	before, _ := os.ReadFile(cfgPath)

	// Setting version to an invalid value must be refused and leave the file intact.
	if _, err := run(t, "config", "set", "version", "2", "--config", cfgPath); err == nil {
		t.Error("setting version=2 should be refused (validation eq=1)")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("refused set must not modify the file")
	}

	// Setting a non-existent key is refused.
	if _, err := run(t, "config", "set", "limits.brand_new", "5", "--config", cfgPath); err == nil {
		t.Error("setting a non-existent key should error")
	}
}

func TestStopWithoutRunningDaemon(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	// No pidfile → clear error, not a panic.
	if _, err := run(t, "stop", "--config", cfgPath, "--env", filepath.Join(dir, "none.env")); err == nil {
		t.Error("stop with no daemon should error")
	}

	// A stale pidfile (pid that isn't us / not alive) is cleaned up.
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// pid 0 is never a real, signalable process for `stop`.
	if err := os.WriteFile(filepath.Join(dataDir, "axon.pid"), []byte("2147483647\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "stop", "--config", cfgPath, "--env", filepath.Join(dir, "none.env"))
	if err != nil {
		t.Fatalf("stop with stale pidfile: %v\n%s", err, out)
	}
	if !strings.Contains(out, "stale pidfile") {
		t.Errorf("expected stale-pidfile cleanup message:\n%s", out)
	}
}
