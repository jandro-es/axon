package main

import (
	"strings"
	"testing"
)

func TestStatusCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, err := run(t, "status", "--config", cfgPath)
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "day :") || !strings.Contains(out, "week:") {
		t.Errorf("status missing day/week windows:\n%s", out)
	}
	if !strings.Contains(out, "remaining") {
		t.Errorf("status missing remaining budget:\n%s", out)
	}

	out, err = run(t, "status", "--json", "--config", cfgPath)
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if !strings.Contains(out, `"Day"`) || !strings.Contains(out, `"GuardPaused"`) {
		t.Errorf("status --json shape unexpected:\n%s", out)
	}
}
