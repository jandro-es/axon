package service

import (
	"strings"
	"testing"
)

func testParams() Params {
	return Params{
		Profile:    "work",
		Binary:     "/usr/local/bin/axon",
		ConfigPath: "/home/u/axon.config.yaml",
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
		// Runs the daemon with the right config + profile.
		for _, want := range []string{"start", "axon.config.yaml", "work"} {
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
