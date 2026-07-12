package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/clients"
	"github.com/jandro-es/axon/internal/config"
)

// withStubs swaps the package-level lookPath/lookupEnv indirections for the
// duration of a test.
func withStubs(t *testing.T, env map[string]string, binaries map[string]bool) {
	t.Helper()
	// Keep the Claude Desktop check hermetic: point it at an absent temp file so
	// tests never read the developer's real ~/…/claude_desktop_config.json.
	t.Setenv("AXON_DESKTOP_CONFIG", filepath.Join(t.TempDir(), "absent-desktop.json"))
	origLook, origEnv := lookPath, lookupEnv
	t.Cleanup(func() { lookPath, lookupEnv = origLook, origEnv })

	lookupEnv = func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
	lookPath = func(bin string) (string, error) {
		if binaries[bin] {
			return "/usr/local/bin/" + bin, nil
		}
		return "", errors.New("not found")
	}
}

func cfgWithAuth(mode string) *config.Config {
	return &config.Config{
		ActiveProfile: "personal",
		Profiles: map[string]config.Profile{
			"personal": {Claude: config.ClaudeConfig{AuthMode: mode}},
		},
	}
}

func findCheck(r DoctorReport, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func cfgWithIngestion(ing config.IngestionConfig) *config.Config {
	return &config.Config{
		ActiveProfile: "personal",
		Profiles: map[string]config.Profile{
			"personal": {Claude: config.ClaudeConfig{AuthMode: "subscription"}, Ingestion: ing},
		},
	}
}

func TestDoctorVisionOff(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(cfgWithIngestion(config.IngestionConfig{}), "personal")
	c, ok := findCheck(r, "vision")
	if !ok || c.Status != StatusOK || !strings.Contains(c.Detail, "off") {
		t.Fatalf("vision off check = %+v ok=%v", c, ok)
	}
}

func TestDoctorVisionAppleWarns(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(cfgWithIngestion(config.IngestionConfig{Vision: "apple"}), "personal")
	c, ok := findCheck(r, "vision")
	if !ok || c.Status != StatusWarn || !strings.Contains(c.Detail, "macOS 27") {
		t.Fatalf("vision apple check = %+v ok=%v", c, ok)
	}
}

func TestDoctorMediaCheckPresent(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(cfgWithIngestion(config.IngestionConfig{}), "personal")
	if _, ok := findCheck(r, "media"); !ok {
		t.Fatal("media check missing")
	}
}

func TestDoctorStrayAPIKeyWarnsUnderSubscription(t *testing.T) {
	for _, mode := range []string{"subscription", "enterprise"} {
		t.Run(mode, func(t *testing.T) {
			withStubs(t, map[string]string{"ANTHROPIC_API_KEY": "sk-ant-x"}, nil)
			r := Doctor(cfgWithAuth(mode), "personal")
			c, ok := findCheck(r, "anthropic-api-key")
			if !ok {
				t.Fatal("missing anthropic-api-key check")
			}
			if c.Status != StatusWarn {
				t.Errorf("status = %q, want warn", c.Status)
			}
		})
	}
}

func TestDoctorAPIKeyOKUnderApiKeyMode(t *testing.T) {
	withStubs(t, map[string]string{"ANTHROPIC_API_KEY": "sk-ant-x"}, nil)
	r := Doctor(cfgWithAuth("api_key"), "personal")
	c, _ := findCheck(r, "anthropic-api-key")
	if c.Status != StatusOK {
		t.Errorf("api_key mode with key set: status = %q, want ok", c.Status)
	}
}

func TestDoctorNoKeyIsOK(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(cfgWithAuth("subscription"), "personal")
	c, _ := findCheck(r, "anthropic-api-key")
	if c.Status != StatusOK {
		t.Errorf("no key: status = %q, want ok", c.Status)
	}
}

func TestDoctorNilConfigFailsConfigCheckNotPanic(t *testing.T) {
	withStubs(t, map[string]string{}, map[string]bool{"claude": true})
	r := Doctor(nil, "personal")
	c, ok := findCheck(r, "config")
	if !ok || c.Status != StatusFail {
		t.Errorf("nil config: config check = %+v, want fail", c)
	}
	if !r.HasFailure() {
		t.Error("HasFailure() = false, want true for nil config")
	}
}

func TestDoctorClaudeDesktopCheck(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "claude_desktop_config.json")
	t.Setenv("AXON_DESKTOP_CONFIG", cfgPath)

	cfg := &config.Config{
		ActiveProfile: "personal",
		Profiles: map[string]config.Profile{
			"personal": {
				VaultPath: filepath.Join(dir, "vault"),
				Claude:    config.ClaudeConfig{AuthMode: "subscription"},
				Dashboard: config.DashboardConfig{Host: "127.0.0.1", Port: 0},
			},
		},
	}

	// Not configured → informational OK.
	if c, _ := findCheck(Doctor(cfg, "personal"), "client:claude-desktop"); c.Status != StatusOK {
		t.Errorf("absent desktop: status = %q, want ok", c.Status)
	}

	// Registered for the active profile → OK with the reduced-guarantee note.
	if _, err := clients.InstallDesktop(cfgPath, clients.Params{Profile: "personal", Binary: "/b/axon", ConfigPath: "/c.yaml"}); err != nil {
		t.Fatal(err)
	}
	c, _ := findCheck(Doctor(cfg, "personal"), "client:claude-desktop")
	if c.Status != StatusOK || !strings.Contains(c.Detail, "tools only") {
		t.Errorf("registered desktop: %+v", c)
	}

	// Registered for a different profile → warn.
	if _, err := clients.InstallDesktop(cfgPath, clients.Params{Profile: "work", Binary: "/b/axon", ConfigPath: "/c.yaml"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := findCheck(Doctor(cfg, "personal"), "client:claude-desktop"); c.Status != StatusWarn {
		t.Errorf("profile-mismatch desktop: status = %q, want warn", c.Status)
	}
}

func TestDoctorClaudeCodeWiringCheck(t *testing.T) {
	t.Setenv("AXON_DESKTOP_CONFIG", filepath.Join(t.TempDir(), "absent.json"))
	dir := t.TempDir()
	cfg := &config.Config{
		ActiveProfile: "personal",
		Profiles: map[string]config.Profile{
			"personal": {VaultPath: dir, Claude: config.ClaudeConfig{AuthMode: "subscription"}, Dashboard: config.DashboardConfig{Host: "127.0.0.1", Port: 0}},
		},
	}
	// No .claude wiring yet → warn.
	if c, _ := findCheck(Doctor(cfg, "personal"), "client:claude-code"); c.Status != StatusWarn {
		t.Errorf("unwired code: status = %q, want warn", c.Status)
	}
	// Create the marker → ok.
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, _ := findCheck(Doctor(cfg, "personal"), "client:claude-code"); c.Status != StatusOK {
		t.Errorf("wired code: status = %q, want ok", c.Status)
	}
}

func TestDoctorBinaryChecks(t *testing.T) {
	withStubs(t, map[string]string{}, map[string]bool{"claude": true}) // ollama missing
	r := Doctor(cfgWithAuth("subscription"), "personal")

	claude, _ := findCheck(r, "claude-cli")
	if claude.Status != StatusOK {
		t.Errorf("claude-cli status = %q, want ok", claude.Status)
	}
	ollama, _ := findCheck(r, "ollama")
	if ollama.Status != StatusWarn {
		t.Errorf("ollama status = %q, want warn (missing)", ollama.Status)
	}
	// Missing optional binaries are warnings, not failures.
	if r.HasFailure() {
		t.Error("missing optional binary should not be a hard failure")
	}
}

func TestDoctorResearchOff(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(&config.Config{
		ActiveProfile: "personal",
		Profiles: map[string]config.Profile{
			"personal": {Claude: config.ClaudeConfig{AuthMode: "subscription"}},
		},
	}, "personal")
	c, ok := findCheck(r, "research")
	if !ok || c.Status != StatusOK || !strings.Contains(c.Detail, "off") {
		t.Fatalf("research off check = %+v ok=%v", c, ok)
	}
}

func TestDoctorResearchEnabled(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(&config.Config{
		ActiveProfile: "personal",
		Profiles: map[string]config.Profile{
			"personal": {
				Claude:   config.ClaudeConfig{AuthMode: "subscription"},
				Research: config.ResearchConfig{Enabled: true},
			},
		},
	}, "personal")
	c, _ := findCheck(r, "research")
	if c.Status != StatusOK || !strings.Contains(c.Detail, "8") {
		t.Fatalf("research enabled check = %+v (want caps in detail)", c)
	}
}

func TestServiceUnitPathCheck(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeUnit := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	plistWithPath := writeUnit("with-path.plist",
		"<dict>\n  <key>PATH</key>\n  <string>"+binDir+":/usr/bin</string>\n</dict>\n")
	plistNoPath := writeUnit("no-path.plist",
		"<dict>\n  <key>AXON_HOME</key>\n  <string>/home/u/.axon</string>\n</dict>\n")
	plistBadPath := writeUnit("bad-path.plist",
		"<dict>\n  <key>PATH</key>\n  <string>/usr/bin:/bin</string>\n</dict>\n")
	systemdWithPath := writeUnit("with-path.service",
		"[Service]\nEnvironment=PATH="+binDir+":/usr/bin\n")

	for _, tc := range []struct {
		name, kind, path string
		want             CheckStatus
		detail           string
	}{
		{"launchd PATH resolves claude", "launchd", plistWithPath, StatusOK, "claude"},
		{"launchd unit without PATH warns", "launchd", plistNoPath, StatusWarn, "PATH"},
		{"launchd PATH missing claude dir warns", "launchd", plistBadPath, StatusWarn, "axon service install"},
		{"systemd PATH resolves claude", "systemd", systemdWithPath, StatusOK, "claude"},
		{"absent unit is skipped ok", "launchd", filepath.Join(dir, "absent.plist"), StatusOK, "no OS service unit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := serviceUnitPathCheck(tc.kind, tc.path)
			if c.Status != tc.want || !strings.Contains(c.Detail, tc.detail) {
				t.Errorf("serviceUnitPathCheck(%s, %s) = %+v, want status %s containing %q",
					tc.kind, tc.path, c, tc.want, tc.detail)
			}
		})
	}
}

func TestDoctorIncludesServicePathCheck(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(cfgWithAuth("subscription"), "personal")
	if _, ok := findCheck(r, "service-path"); !ok {
		t.Fatal("doctor report missing service-path check")
	}
}
