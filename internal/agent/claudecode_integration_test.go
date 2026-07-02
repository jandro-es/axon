package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClaudeCodeAuthIsAttemptedHeadless drives the REAL claude CLI through the
// adapter's actual argv/env with a deliberately bogus OAuth token. The CLI must
// respond with a 401 (credentials were consulted and rejected) — NOT with
// "Not logged in", which is what --bare produced: bare mode skips credential
// lookup entirely, silently breaking every headless automation. Spends no
// tokens (auth is rejected before any model call). Skipped without the CLI.
func TestClaudeCodeAuthIsAttemptedHeadless(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}
	if testing.Short() {
		t.Skip("skipping real CLI invocation in -short mode")
	}
	cfg := t.TempDir()
	// Isolate from any real session/keychain state as much as possible.
	os.Unsetenv("ANTHROPIC_API_KEY")
	c := NewClaudeCode(ClaudeCodeOptions{
		ConfigDir:  cfg,
		OAuthToken: "sk-ant-oat01-bogus-integration-test",
		AuthMode:   "enterprise",
		Timeout:    45 * time.Second,
	})
	_, err := c.Run(context.Background(), Request{Operation: "auth-probe", Prompt: "say ok"})
	if err == nil {
		t.Fatal("expected an auth failure with a bogus token")
	}
	msg := err.Error()
	if strings.Contains(msg, "Not logged in") {
		t.Errorf("CLI never consulted the OAuth token (the --bare regression): %s", msg)
	}
	if !strings.Contains(msg, "401") {
		t.Errorf("expected a 401 bearer-token rejection, got: %s", msg)
	}
}
