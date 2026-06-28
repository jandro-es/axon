package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func exampleConfig(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "axon.config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// run executes the root command with args, capturing stdout/stderr.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestConfigValidateOnExampleConfig(t *testing.T) {
	out, err := run(t, "config", "validate", "--config", exampleConfig(t))
	if err != nil {
		t.Fatalf("config validate failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK:") || !strings.Contains(out, `active profile "personal"`) {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestConfigValidateRejectsMissingFile(t *testing.T) {
	if _, err := run(t, "config", "validate", "--config", "does-not-exist.yaml"); err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestDoctorRunsAndReportsStatus(t *testing.T) {
	out, err := run(t, "doctor", "--config", exampleConfig(t))
	if err != nil {
		t.Fatalf("doctor returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "axon doctor") || !strings.Contains(out, "anthropic-api-key") {
		t.Errorf("doctor output missing expected sections: %q", out)
	}
	if !strings.Contains(out, "status:") {
		t.Errorf("doctor did not print an overall status: %q", out)
	}
}

func TestVersionCommand(t *testing.T) {
	out, err := run(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "axon ") {
		t.Errorf("version output = %q", out)
	}
}

func TestNoStubCommandsRemain(t *testing.T) {
	// Every CLI command is now implemented; the stub list must be empty.
	if cmds := newStubCmds(&globalFlags{}); len(cmds) != 0 {
		names := make([]string, 0, len(cmds))
		for _, c := range cmds {
			names = append(names, c.Name())
		}
		t.Errorf("expected no stub commands, got %v", names)
	}
}
