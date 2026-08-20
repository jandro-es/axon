package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/clients"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/service"
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

func TestDoctorVisionAppleFollowsFMState(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	orig := detectFM
	defer func() { detectFM = orig }()

	// fm unavailable → warn, OCR-only fallback named (the pre-M3 contract).
	detectFM = func(context.Context) FMStatus {
		return FMStatus{State: FMStateAbsent, Detail: "fm CLI not found on PATH"}
	}
	r := Doctor(cfgWithIngestion(config.IngestionConfig{Vision: "apple"}), "personal")
	c, ok := findCheck(r, "vision")
	if !ok || c.Status != StatusWarn || !strings.Contains(c.Detail, "OCR-only") {
		t.Fatalf("vision apple (fm absent) = %+v ok=%v", c, ok)
	}

	// fm ready → ok (FR-196).
	detectFM = func(context.Context) FMStatus { return FMStatus{State: FMStateReady} }
	r = Doctor(cfgWithIngestion(config.IngestionConfig{Vision: "apple"}), "personal")
	if c, ok = findCheck(r, "vision"); !ok || c.Status != StatusOK {
		t.Fatalf("vision apple (fm ready) = %+v ok=%v", c, ok)
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
	// The reload half of every Fix, as internal/service generates it.
	reloadCmd := service.LaunchdUnit(service.Params{Profile: "personal", HomeDir: dir}).ReloadCmd
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
		{"launchd PATH missing claude dir warns", "launchd", plistBadPath, StatusWarn, "cannot resolve claude"},
		{"systemd PATH resolves claude", "systemd", systemdWithPath, StatusOK, "claude"},
		{"absent unit is skipped ok", "launchd", filepath.Join(dir, "absent.plist"), StatusOK, "no OS service unit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Port 0 = no daemon to consult, so these exercise the
			// unit-file fallback.
			c := serviceUnitPathCheck(tc.kind, tc.path, reloadCmd, "127.0.0.1", 0)
			if c.Status != tc.want || !strings.Contains(c.Detail, tc.detail) {
				t.Errorf("serviceUnitPathCheck(%s, %s) = %+v, want status %s containing %q",
					tc.kind, tc.path, c, tc.want, tc.detail)
			}
			// Detail says what is wrong; Fix says what to do about it. Every
			// warning here is actionable, so it must carry a command.
			if tc.want == StatusWarn && !strings.Contains(c.Fix, "axon service install") {
				t.Errorf("warning %q carries no actionable Fix: %+v", tc.name, c)
			}
			if tc.want == StatusOK && c.Fix != "" {
				t.Errorf("passing check should carry no Fix: %+v", c)
			}
		})
	}
}

// A unit file corrected on disk does not reach a launchd/systemd job still
// running the definition it was loaded with. Reading only the file therefore
// passed the exact machine whose automations were failing every run with
// `exec: "claude": executable file not found`. When the daemon is up, its own
// answer wins.
func TestServiceUnitPathCheckPrefersTheRunningDaemon(t *testing.T) {
	dir := t.TempDir()
	// The reload half of every Fix, as internal/service generates it.
	reloadCmd := service.LaunchdUnit(service.Params{Profile: "personal", HomeDir: dir}).ReloadCmd
	binDir := filepath.Join(dir, "local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A unit whose PATH *would* resolve claude on a fresh load — the file-only
	// check reports this one green no matter what the daemon actually got.
	goodUnit := filepath.Join(dir, "good.plist")
	if err := os.WriteFile(goodUnit,
		[]byte("<dict>\n  <key>PATH</key>\n  <string>"+binDir+":/usr/bin</string>\n</dict>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	daemon := func(t *testing.T, body string) (string, int) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return splitHostPort(t, srv.Listener.Addr().String())
	}

	t.Run("stale loaded job warns even though the unit file is correct", func(t *testing.T) {
		host, port := daemon(t, `{"status":"ok","profile":"personal","claude_path":""}`)
		c := serviceUnitPathCheck("launchd", goodUnit, reloadCmd, host, port)
		if c.Status != StatusWarn {
			t.Fatalf("daemon that cannot resolve claude = %+v, want warn", c)
		}
		if !strings.Contains(c.Detail, "stale") {
			t.Errorf("detail must explain the file/job divergence, got %q", c.Detail)
		}
		if c.Fix == "" {
			t.Error("a stale loaded job is actionable and needs a Fix")
		}
	})

	t.Run("a daemon that resolves claude passes and names what it found", func(t *testing.T) {
		host, port := daemon(t, `{"status":"ok","profile":"personal","claude_path":"/opt/tools/claude"}`)
		c := serviceUnitPathCheck("launchd", goodUnit, reloadCmd, host, port)
		if c.Status != StatusOK {
			t.Fatalf("healthy daemon = %+v, want ok", c)
		}
		if !strings.Contains(c.Detail, "/opt/tools/claude") {
			t.Errorf("detail should report the daemon's own resolution, got %q", c.Detail)
		}
	})

	// A daemon predating the field reports nothing rather than "". Treating
	// absent as empty would warn on every healthy older daemon.
	t.Run("a daemon too old to report falls back to the unit file", func(t *testing.T) {
		host, port := daemon(t, `{"status":"ok","profile":"personal"}`)
		c := serviceUnitPathCheck("launchd", goodUnit, reloadCmd, host, port)
		if c.Status != StatusOK || !strings.Contains(c.Detail, "service unit PATH") {
			t.Errorf("old daemon = %+v, want the unit-file verdict", c)
		}
	})

	t.Run("a foreign listener on the port falls back to the unit file", func(t *testing.T) {
		host, port := daemon(t, `{"hello":"not axon"}`)
		c := serviceUnitPathCheck("launchd", goodUnit, reloadCmd, host, port)
		if c.Status != StatusOK || !strings.Contains(c.Detail, "service unit PATH") {
			t.Errorf("foreign listener = %+v, want the unit-file verdict", c)
		}
	})
}

// The remedy has to RELOAD the unit, not restart the daemon. `launchctl
// kickstart -k` and a bare `systemctl --user restart` both re-run the process
// under the definition already loaded, so the previous fix rewrote a correct
// unit file and changed nothing the daemon could see. Asserted against the
// command a real unit generates, since that is what the user actually receives.
func TestServiceReinstallFixReloadsTheUnit(t *testing.T) {
	unit, err := service.ForOS("", service.Params{Profile: "personal", HomeDir: t.TempDir()})
	if err != nil {
		t.Skipf("no service unit on %s: %v", runtime.GOOS, err)
	}
	fix := serviceReinstallFix(unit.ReloadCmd)

	if !strings.Contains(fix, "axon service install") {
		t.Errorf("fix must regenerate the unit first: %q", fix)
	}
	if strings.Contains(fix, "kickstart") {
		t.Errorf("kickstart restarts without re-reading the unit: %q", fix)
	}

	switch runtime.GOOS {
	case "darwin":
		for _, want := range []string{"bootout", "bootstrap"} {
			if !strings.Contains(fix, want) {
				t.Errorf("launchd fix missing %q: %q", want, fix)
			}
		}
	case "linux":
		if !strings.Contains(fix, "daemon-reload") {
			t.Errorf("systemd reads edited units only after daemon-reload: %q", fix)
		}
	}

	// A platform with no unit must not emit a dangling "&&".
	if bare := serviceReinstallFix(""); bare != "axon service install" {
		t.Errorf("empty reload command = %q, want the bare install", bare)
	}
}

func TestDoctorIncludesServicePathCheck(t *testing.T) {
	withStubs(t, map[string]string{}, nil)
	r := Doctor(cfgWithAuth("subscription"), "personal")
	if _, ok := findCheck(r, "service-path"); !ok {
		t.Fatal("doctor report missing service-path check")
	}
}

// Every external tool the daemon shells out to must tell the user how to
// install it when it is missing. A warning that names a missing binary without
// naming the command to get it makes the reader go and search for it.
func TestMissingBinaryChecksCarryAnInstallCommand(t *testing.T) {
	original := lookPath
	t.Cleanup(func() { lookPath = original })
	lookPath = func(string) (string, error) { return "", errors.New("not found") }

	for _, bin := range []string{"ollama", "claude", "yt-dlp"} {
		c := binaryCheck(bin, bin, "found", bin+" not found on PATH")
		if c.Status != StatusWarn {
			t.Fatalf("binaryCheck(%s) status = %s, want warn", bin, c.Status)
		}
		if c.Fix == "" {
			t.Errorf("missing %s carries no install command", bin)
		}
	}

	// A binary with no generic install story must not invent one.
	if got := installHint("some-unknown-tool"); got != "" {
		t.Errorf("installHint for an unknown tool = %q, want empty", got)
	}
}

// The dashboard port being busy is only a problem if something OTHER than
// AXON has it. On a machine where the daemon is running — the normal, healthy
// case — the old check warned about the very state it wanted, and offered no
// fix because there is nothing to fix.
func TestPortFreeCheck(t *testing.T) {
	t.Run("a free port passes", func(t *testing.T) {
		c := portFreeCheck("127.0.0.1", freePort(t))
		if c.Status != StatusOK || !strings.Contains(c.Detail, "free") {
			t.Errorf("free port = %+v, want ok", c)
		}
	})

	t.Run("no port configured warns", func(t *testing.T) {
		if c := portFreeCheck("127.0.0.1", 0); c.Status != StatusWarn {
			t.Errorf("port 0 = %+v, want warn", c)
		}
	})

	t.Run("AXON's own daemon on the port passes and names the profile", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok","profile":"personal","version":"1.3.4"}`))
		}))
		defer srv.Close()

		host, port := splitHostPort(t, srv.Listener.Addr().String())
		c := portFreeCheck(host, port)
		if c.Status != StatusOK {
			t.Fatalf("own daemon on port = %+v, want ok", c)
		}
		if !strings.Contains(c.Detail, "personal") {
			t.Errorf("detail should name the serving profile: %q", c.Detail)
		}
		if c.Fix != "" {
			t.Errorf("nothing to fix when it is our own daemon: %q", c.Fix)
		}
	})

	t.Run("a foreign listener warns and says what to do", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not axon", http.StatusNotFound)
		}))
		defer srv.Close()

		host, port := splitHostPort(t, srv.Listener.Addr().String())
		c := portFreeCheck(host, port)
		if c.Status != StatusWarn {
			t.Fatalf("foreign listener = %+v, want warn", c)
		}
		if c.Fix == "" {
			t.Errorf("a genuine port clash is actionable and needs a Fix: %+v", c)
		}
	})

	t.Run("a listener that accepts but never answers still warns", func(t *testing.T) {
		// A raw TCP listener that never speaks HTTP: the probe must time out
		// and warn rather than hang doctor.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skipf("cannot listen: %v", err)
		}
		defer func() { _ = ln.Close() }()

		host, port := splitHostPort(t, ln.Addr().String())
		start := time.Now()
		c := portFreeCheck(host, port)
		if c.Status != StatusWarn {
			t.Errorf("silent listener = %+v, want warn", c)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("probe took %v — doctor must not hang on a dead socket", elapsed)
		}
	})
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port := splitHostPort(t, ln.Addr().String())
	_ = ln.Close()
	return port
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return host, port
}
