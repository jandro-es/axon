package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
)

func TestDoctorCommandJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	// The human renderer stays canonical and must keep working.
	out, err := run(t, "doctor", "--config", cfgPath)
	if err != nil && !strings.Contains(out, "axon doctor") {
		t.Fatalf("doctor: %v\n%s", err, out)
	}

	// --json emits a decodable report regardless of the overall verdict; a
	// failing check is reported through Status, not by refusing to emit.
	out, _ = run(t, "doctor", "--json", "--config", cfgPath)

	var rep struct {
		Profile string `json:"profile"`
		Status  string `json:"status"`
		Checks  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json is not valid JSON: %v\n%s", err, out)
	}
	if rep.Profile == "" {
		t.Errorf("doctor --json missing profile:\n%s", out)
	}
	if rep.Status != "ok" && rep.Status != "fail" {
		t.Errorf("doctor --json status %q not in {ok,fail}:\n%s", rep.Status, out)
	}
	if len(rep.Checks) == 0 {
		t.Fatalf("doctor --json emitted no checks:\n%s", out)
	}
	for _, c := range rep.Checks {
		if c.Name == "" {
			t.Errorf("doctor --json check with empty name:\n%s", out)
		}
		switch c.Status {
		case "ok", "warn", "fail":
		default:
			t.Errorf("doctor --json check %q status %q not in {ok,warn,fail}", c.Name, c.Status)
		}
	}

	// A JSON run must not leak the styled human report onto stdout.
	if strings.Contains(out, "axon doctor") {
		t.Errorf("doctor --json leaked human output:\n%s", out)
	}
}

// FR-206: the CLI and the daemon must assemble the SAME report. Both build
// their extras from this one helper, so this test is the regression that stops
// the two drifting apart again.
func TestSelfCheckExtrasNamesTheTwoCLIOnlyChecks(t *testing.T) {
	cfg := &config.Config{
		ActiveProfile: "personal",
		Profiles:      map[string]config.Profile{"personal": {}},
	}
	extras := selfCheckExtras(cfg, "personal")
	var names []string
	for _, c := range extras {
		names = append(names, c.Name)
	}
	want := []string{"update-available", "recipes"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}

	// A nil config still yields the update check — doctor must work when the
	// config failed to load.
	if got := selfCheckExtras(nil, "personal"); len(got) != 1 || got[0].Name != "update-available" {
		t.Fatalf("nil cfg: want just update-available, got %+v", got)
	}

	// The full report ends with exactly these, in this order.
	report := core.Doctor(cfg, "personal", selfCheckExtras(cfg, "personal")...)
	tail := report.Checks[len(report.Checks)-2:]
	if tail[0].Name != "update-available" || tail[1].Name != "recipes" {
		t.Fatalf("report tail wrong: %+v", tail)
	}
}
