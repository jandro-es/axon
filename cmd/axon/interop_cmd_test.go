package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeInteropConfig writes a config whose active profile enables a community
// Obsidian MCP backend (FR-54).
func writeInteropConfig(t *testing.T, dir string) string {
	t.Helper()
	base, err := os.ReadFile(writeTempConfig(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(base) + `    interop:
      obsidian_mcp:
        enabled: true
        command: npx
        args: ["-y", "obsidian-mcp", "/vault"]
`
	path := filepath.Join(dir, "interop.config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMCPInstallDesktopRegistersInteropBackend(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeInteropConfig(t, dir)
	desktop := filepath.Join(dir, "claude_desktop_config.json")
	t.Setenv("AXON_DESKTOP_CONFIG", desktop)

	out, err := run(t, "mcp", "install", "--client", "desktop",
		"--config", cfgPath, "--env", filepath.Join(dir, "none.env"))
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Obsidian MCP backend") {
		t.Errorf("interop not reported:\n%s", out)
	}
	data, _ := os.ReadFile(desktop)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if _, ok := servers["axon"]; !ok {
		t.Error("axon entry missing")
	}
	obs, ok := servers["obsidian"].(map[string]any)
	if !ok {
		t.Fatalf("obsidian entry missing: %#v", servers)
	}
	if obs["command"] != "npx" {
		t.Errorf("obsidian command = %v", obs["command"])
	}
}

func TestMCPInstallPrintIncludesInterop(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeInteropConfig(t, dir)
	out, err := run(t, "mcp", "install", "--client", "desktop", "--print",
		"--config", cfgPath, "--env", filepath.Join(dir, "none.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"axon"`) || !strings.Contains(out, `"obsidian"`) {
		t.Errorf("--print should include both axon and obsidian:\n%s", out)
	}
}
