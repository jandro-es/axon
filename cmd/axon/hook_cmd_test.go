package main

import (
	"bytes"
	"strings"
	"testing"
)

// runStdin executes the root command with args and a stdin payload.
func runStdin(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestHookSessionStartCLI(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := runStdin(t, `{"hook_event_name":"SessionStart"}`, "hook", "SessionStart", "--config", cfgPath)
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if !strings.Contains(out, "additionalContext") || !strings.Contains(out, "Budget:") {
		t.Errorf("SessionStart did not inject status:\n%s", out)
	}
}

func TestHookPreToolUseDenyCLI(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, _ := runStdin(t,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm Daily/2026-06-28.md"}}`,
		"hook", "PreToolUse", "--config", cfgPath)
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected a deny decision for rm:\n%s", out)
	}
}
