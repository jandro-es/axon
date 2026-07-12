package service

import (
	"fmt"
	"strings"
	"testing"
)

func testParams() Params {
	return Params{
		Profile:    "work",
		Binary:     "/usr/local/bin/axon",
		ConfigPath: "/home/u/.axon/config.yaml",
		EnvPath:    "/home/u/.axon/.env",
		ConfigDir:  "/home/u/.axon/profiles/work/claude",
		AxonHome:   "/home/u/.axon",
		LogDir:     "/home/u/.axon/profiles/work/logs",
		HomeDir:    "/home/u",
	}
}

func TestForOSDispatch(t *testing.T) {
	for _, tc := range []struct{ goos, kind string }{
		{"darwin", "launchd"}, {"linux", "systemd"}, {"windows", "windows"},
	} {
		u, err := ForOS(tc.goos, testParams())
		if err != nil {
			t.Fatalf("ForOS(%s): %v", tc.goos, err)
		}
		if u.Kind != tc.kind {
			t.Errorf("ForOS(%s).Kind = %q, want %q", tc.goos, u.Kind, tc.kind)
		}
	}
	if _, err := ForOS("plan9", testParams()); err == nil {
		t.Error("expected error for unsupported OS")
	}
}

func TestUnitsAreProfileScopedAndIsolated(t *testing.T) {
	for _, u := range []Unit{LaunchdUnit(testParams()), SystemdUnit(testParams()), WindowsTask(testParams())} {
		// Profile-scoped identity.
		if !strings.Contains(u.Label, "work") && !strings.Contains(u.Content, "work") {
			t.Errorf("%s unit not profile-scoped: label=%q", u.Kind, u.Label)
		}
		// Runs the daemon with the right config + profile + secrets file.
		for _, want := range []string{"start", "config.yaml", "work", "--env", "/home/u/.axon/.env"} {
			if !strings.Contains(u.Content, want) {
				t.Errorf("%s unit missing %q", u.Kind, want)
			}
		}
		// Carries the profile-isolating environment (the launchd/systemd ones).
		if u.Kind != "windows" {
			if !strings.Contains(u.Content, "AXON_HOME") || !strings.Contains(u.Content, "CLAUDE_CONFIG_DIR") {
				t.Errorf("%s unit missing isolation env (AXON_HOME/CLAUDE_CONFIG_DIR)", u.Kind)
			}
			if !strings.Contains(u.Content, "/home/u/.axon/profiles/work/claude") {
				t.Errorf("%s unit missing the profile's config dir", u.Kind)
			}
		}
		// Install path + lifecycle hints present.
		if u.Path == "" || u.EnableCmd == "" || u.StartCmd == "" || u.StopCmd == "" {
			t.Errorf("%s unit missing path/lifecycle hints: %+v", u.Kind, u)
		}
	}
}

func TestUnitsAreDeterministic(t *testing.T) {
	// Generated twice, byte-identical (no map-order nondeterminism).
	if a, b := LaunchdUnit(testParams()).Content, LaunchdUnit(testParams()).Content; a != b {
		t.Error("launchd unit is not deterministic")
	}
	if a, b := SystemdUnit(testParams()).Content, SystemdUnit(testParams()).Content; a != b {
		t.Error("systemd unit is not deterministic")
	}
}

func TestEnvFlagIsOptional(t *testing.T) {
	p := testParams()
	p.EnvPath = ""
	if c := LaunchdUnit(p).Content; strings.Contains(c, "--env") {
		t.Errorf("expected no --env flag when EnvPath is empty:\n%s", c)
	}
	p.EnvPath = "/home/u/.axon/.env"
	if c := LaunchdUnit(p).Content; !strings.Contains(c, "--env") {
		t.Error("expected --env flag when EnvPath is set")
	}
}

func TestUnitsCarryDaemonPathEnv(t *testing.T) {
	p := testParams()
	p.PathEnv = "/home/u/.local/bin:/usr/local/bin:/usr/bin:/bin"

	l := LaunchdUnit(p).Content
	if !strings.Contains(l, "<key>PATH</key>") || !strings.Contains(l, "<string>/home/u/.local/bin:/usr/local/bin:/usr/bin:/bin</string>") {
		t.Errorf("launchd unit missing PATH env:\n%s", l)
	}
	s := SystemdUnit(p).Content
	if !strings.Contains(s, "Environment=PATH=/home/u/.local/bin:/usr/local/bin:/usr/bin:/bin") {
		t.Errorf("systemd unit missing PATH env:\n%s", s)
	}

	p.PathEnv = ""
	if c := LaunchdUnit(p).Content; strings.Contains(c, "<key>PATH</key>") {
		t.Errorf("expected no PATH key when PathEnv is empty:\n%s", c)
	}
}

func TestDaemonPathEnv(t *testing.T) {
	look := func(tools map[string]string) func(string) (string, error) {
		return func(name string) (string, error) {
			if p, ok := tools[name]; ok {
				return p, nil
			}
			return "", fmt.Errorf("%s not found", name)
		}
	}

	// Tool dirs come first (dedup'd), then the standard system dirs.
	got := DaemonPathEnv(look(map[string]string{
		"claude": "/home/u/.local/bin/claude",
		"yt-dlp": "/opt/homebrew/bin/yt-dlp",
		"ollama": "/opt/homebrew/bin/ollama",
	}))
	want := "/home/u/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if got != want {
		t.Errorf("DaemonPathEnv = %q, want %q", got, want)
	}

	// No tools resolvable → still a sane system PATH.
	got = DaemonPathEnv(look(nil))
	want = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if got != want {
		t.Errorf("DaemonPathEnv (no tools) = %q, want %q", got, want)
	}

	// A tool already under a system dir adds nothing twice.
	got = DaemonPathEnv(look(map[string]string{"claude": "/usr/local/bin/claude"}))
	want = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if got != want {
		t.Errorf("DaemonPathEnv (system-dir tool) = %q, want %q", got, want)
	}
}

func TestXMLEscaping(t *testing.T) {
	p := testParams()
	p.Binary = `/path/with & <special> "chars"`
	u := LaunchdUnit(p)
	if strings.Contains(u.Content, "& <") || strings.Contains(u.Content, `"chars"`) {
		t.Errorf("special chars not XML-escaped in plist:\n%s", u.Content)
	}
	if !strings.Contains(u.Content, "&amp;") {
		t.Error("expected &amp; escaping")
	}
}
