package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPInstallDesktopWritesProfileScopedEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	desktop := filepath.Join(dir, "claude_desktop_config.json")
	t.Setenv("AXON_DESKTOP_CONFIG", desktop)

	out, err := run(t, "mcp", "install", "--client", "desktop",
		"--config", cfgPath, "--env", filepath.Join(dir, "none.env"))
	if err != nil {
		t.Fatalf("mcp install desktop failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Claude Desktop") || !strings.Contains(out, "tools only") {
		t.Errorf("summary missing desktop note:\n%s", out)
	}
	data, err := os.ReadFile(desktop)
	if err != nil {
		t.Fatalf("desktop config not written: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("desktop config not valid JSON: %v", err)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	axon, ok := servers["axon"].(map[string]any)
	if !ok {
		t.Fatalf("axon entry missing: %#v", m)
	}
	args, _ := axon["args"].([]any)
	joined := strings.Join(toStrings(args), " ")
	if !strings.Contains(joined, "--profile personal") || !strings.Contains(joined, "mcp") {
		t.Errorf("entry args not profile-scoped: %v", args)
	}
}

func TestMCPInstallDesktopPreservesExistingAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	desktop := filepath.Join(dir, "claude_desktop_config.json")
	if err := os.WriteFile(desktop, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AXON_DESKTOP_CONFIG", desktop)
	env := filepath.Join(dir, "none.env")

	if _, err := run(t, "mcp", "install", "--client", "desktop", "--config", cfgPath, "--env", env); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "mcp", "install", "--client", "desktop", "--config", cfgPath, "--env", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("second install not idempotent:\n%s", out)
	}
	data, _ := os.ReadFile(desktop)
	if !strings.Contains(string(data), `"other"`) {
		t.Error("existing 'other' server was clobbered")
	}
}

func TestMCPInstallPrintWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	desktop := filepath.Join(dir, "claude_desktop_config.json")
	t.Setenv("AXON_DESKTOP_CONFIG", desktop)

	out, err := run(t, "mcp", "install", "--client", "desktop", "--print",
		"--config", cfgPath, "--env", filepath.Join(dir, "none.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"mcpServers"`) || !strings.Contains(out, `"axon"`) {
		t.Errorf("--print did not emit the entry:\n%s", out)
	}
	if _, err := os.Stat(desktop); !os.IsNotExist(err) {
		t.Error("--print must not write the desktop config")
	}
}

func TestMCPInstallCodeGeneratesWiring(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	out, err := run(t, "mcp", "install", "--client", "code",
		"--config", cfgPath, "--env", filepath.Join(dir, "none.env"))
	if err != nil {
		t.Fatalf("mcp install code failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault", ".claude", ".mcp.json")); err != nil {
		t.Errorf("code wiring not generated: %v", err)
	}
}

func TestMCPInstallRejectsUnknownClient(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "mcp", "install", "--client", "emacs",
		"--config", cfgPath, "--env", filepath.Join(dir, "none.env")); err == nil {
		t.Error("expected error for unknown client")
	}
}

func toStrings(a []any) []string {
	out := make([]string, 0, len(a))
	for _, v := range a {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
