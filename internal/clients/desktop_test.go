package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleParams() Params {
	return Params{
		Profile: "personal", Binary: "/usr/local/bin/axon",
		ConfigPath: "/home/me/axon.config.yaml", ConfigDir: "/home/me/.axon/claude",
		AxonHome: "/home/me/.axon",
	}
}

func readConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("config not valid JSON: %v\n%s", err, data)
	}
	return m
}

func servers(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	s, ok := m["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or wrong type: %#v", m["mcpServers"])
	}
	return s
}

func TestInstallDesktopCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "claude_desktop_config.json")
	res, err := InstallDesktop(path, sampleParams())
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "created" {
		t.Errorf("action = %q, want created", res.Action)
	}
	s := servers(t, readConfig(t, path))
	axon, ok := s[ServerName].(map[string]any)
	if !ok {
		t.Fatalf("axon entry missing: %#v", s)
	}
	if axon["command"] != "/usr/local/bin/axon" {
		t.Errorf("command = %v", axon["command"])
	}
}

func TestInstallDesktopPreservesOtherServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	seed := `{
  "globalShortcut": "Cmd+Space",
  "mcpServers": {
    "other": { "command": "/opt/other", "args": ["serve"] }
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InstallDesktop(path, sampleParams())
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "added" {
		t.Errorf("action = %q, want added", res.Action)
	}
	m := readConfig(t, path)
	if m["globalShortcut"] != "Cmd+Space" {
		t.Error("unknown top-level key not preserved")
	}
	s := servers(t, m)
	if _, ok := s["other"]; !ok {
		t.Error("existing 'other' server was clobbered")
	}
	if _, ok := s[ServerName]; !ok {
		t.Error("axon server not added")
	}
}

func TestInstallDesktopIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if _, err := InstallDesktop(path, sampleParams()); err != nil {
		t.Fatal(err)
	}
	res, err := InstallDesktop(path, sampleParams())
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("second install action = %q, want unchanged", res.Action)
	}
}

func TestInstallDesktopUpdatesChangedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if _, err := InstallDesktop(path, sampleParams()); err != nil {
		t.Fatal(err)
	}
	p2 := sampleParams()
	p2.Profile = "work"
	res, err := InstallDesktop(path, p2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "updated" {
		t.Errorf("action = %q, want updated", res.Action)
	}
	if got := DetectMust(t, path).Profile; got != "work" {
		t.Errorf("profile after update = %q, want work", got)
	}
}

func TestInstallDesktopRefusesInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallDesktop(path, sampleParams()); err == nil {
		t.Error("expected refusal to overwrite invalid JSON")
	}
	// The bad file must be left untouched.
	data, _ := os.ReadFile(path)
	if string(data) != "{ not valid json" {
		t.Error("invalid config was modified")
	}
}

func TestDetectDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	// Absent file → not present.
	st, err := DetectDesktop(path)
	if err != nil || st.Present || st.Registered {
		t.Fatalf("absent: %+v, %v", st, err)
	}
	if _, err := InstallDesktop(path, sampleParams()); err != nil {
		t.Fatal(err)
	}
	st, err = DetectDesktop(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Present || !st.Registered || st.Profile != "personal" {
		t.Errorf("after install: %+v", st)
	}
}

func TestPrintJSON(t *testing.T) {
	out := sampleParams().PrintJSON()
	if !strings.Contains(out, `"mcpServers"`) || !strings.Contains(out, `"axon"`) || !strings.Contains(out, "--profile") {
		t.Errorf("print output missing expected fields:\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Errorf("print output is not valid JSON: %v", err)
	}
}

// DetectMust is a test helper that fails on error.
func DetectMust(t *testing.T, path string) ClientStatus {
	t.Helper()
	st, err := DetectDesktop(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
