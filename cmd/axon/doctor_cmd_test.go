package main

import (
	"encoding/json"
	"strings"
	"testing"
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
