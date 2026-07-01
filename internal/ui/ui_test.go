package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// A non-*os.File writer is never a terminal, so styling must be disabled and
// every helper must return its input unchanged.
func TestStylerDisabledForNonTTY(t *testing.T) {
	// A stray FORCE_COLOR in the ambient env would force colour on even for a
	// non-terminal, so neutralise it for a deterministic result.
	os.Unsetenv("FORCE_COLOR")

	s := For(&bytes.Buffer{})
	if s.Enabled() {
		t.Fatal("styling should be disabled for a bytes.Buffer")
	}
	if got := s.Red("boom"); got != "boom" {
		t.Errorf("Red on disabled styler = %q, want plain text", got)
	}
	if got := s.Bold(s.Green("ok")); got != "ok" {
		t.Errorf("nested styles on disabled styler = %q, want plain text", got)
	}
	if got := s.Divider(4); got != "────" {
		t.Errorf("Divider = %q, want plain rule", got)
	}
}

// An enabled styler wraps text in ANSI sequences and always resets.
func TestStylerEnabledWraps(t *testing.T) {
	s := Styler{on: true}
	got := s.Red("x")
	if !strings.HasPrefix(got, "\033[") || !strings.HasSuffix(got, reset) {
		t.Errorf("Red = %q, want ANSI-wrapped with reset", got)
	}
	if s.Red("") != "" {
		t.Error("empty string should never be wrapped")
	}
}

// NO_COLOR disables styling even for a real terminal-like file.
func TestColorEnabledRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled(os.Stdout) {
		t.Error("NO_COLOR must disable colour")
	}
}

func TestHint(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string // substring the hint must contain, or "" for no hint
	}{
		{"missing config", fmt.Errorf("read config %q: %w", "/x/config.yaml", os.ErrNotExist), "axon init"},
		{"undefined profile", errors.New(`profile "work" is not defined in profiles`), "axon profiles"},
		{"validation", errors.New("config validation failed: Field required"), "axon config validate"},
		{"ollama", errors.New("post http://localhost:11434: connection refused"), "ollama serve"},
		{"stray api key", errors.New("ANTHROPIC_API_KEY is set"), "API billing"},
		{"unknown", errors.New("some unrelated failure"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Hint(tc.err)
			if tc.want == "" {
				if got != "" {
					t.Errorf("Hint = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("Hint = %q, want to contain %q", got, tc.want)
			}
		})
	}
}

// FprintError renders the message and, when known, an actionable hint.
func TestFprintError(t *testing.T) {
	var buf bytes.Buffer
	FprintError(&buf, fmt.Errorf("read config %q: %w", "/x/config.yaml", os.ErrNotExist))
	out := buf.String()
	if !strings.Contains(out, "Error:") || !strings.Contains(out, "/x/config.yaml") {
		t.Errorf("missing error message:\n%s", out)
	}
	if !strings.Contains(out, "axon init") {
		t.Errorf("missing hint:\n%s", out)
	}

	// A nil error prints nothing.
	buf.Reset()
	FprintError(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("nil error should print nothing, got %q", buf.String())
	}
}
