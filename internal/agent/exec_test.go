package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests exercise the REAL subprocess executor (execClaude) — the only
// path to Claude in production — using /bin/sh as a stand-in binary, covering
// the runtime edges the fake executor cannot: timeout kill, stderr capture,
// stdin delivery.

func requireSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
}

func TestExecClaudeKillsOnContextTimeout(t *testing.T) {
	requireSh(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// The `sleep 30 &` grandchild inherits the stdout/stderr pipes: without
	// the process-group kill + WaitDelay, Wait blocks on it for the full 30s
	// even after the direct child is dead (the exact failure seen in CI).
	start := time.Now()
	_, _, err := execClaude(ctx, "/bin/sh", []string{"-c", "sleep 30 & sleep 30"}, nil, "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the context deadline kills the child")
	}
	// Prompt = well under the 30s sleeps; 10s allows for the 5s WaitDelay
	// fallback plus slow CI runners.
	if elapsed > 10*time.Second {
		t.Fatalf("child was not killed promptly: took %v", elapsed)
	}
}

func TestExecClaudeCapturesStderrOnFailure(t *testing.T) {
	requireSh(t)
	_, stderr, err := execClaude(context.Background(),
		"/bin/sh", []string{"-c", "echo 'auth failed: run claude login' >&2; exit 3"}, nil, "")
	if err == nil {
		t.Fatal("expected a non-zero exit to error")
	}
	if !strings.Contains(string(stderr), "auth failed") {
		t.Errorf("stderr not captured: %q", stderr)
	}
}

func TestExecClaudePassesPromptOnStdin(t *testing.T) {
	requireSh(t)
	stdout, _, err := execClaude(context.Background(),
		"/bin/sh", []string{"-c", "cat"}, nil, "the prompt body")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != "the prompt body" {
		t.Errorf("stdin not delivered: stdout = %q", stdout)
	}
}

// TestRunSurfacesStderrInError: when the CLI fails, the user-facing error must
// carry the CLI's own explanation (e.g. "please run claude login"), not just
// "exit status 1".
func TestRunSurfacesStderrInError(t *testing.T) {
	requireSh(t)
	c := NewClaudeCode(ClaudeCodeOptions{Bin: "/bin/sh"})
	c.run = func(ctx context.Context, bin string, args, env []string, stdin string) ([]byte, []byte, error) {
		return execClaude(ctx, bin, []string{"-c", "echo 'not logged in' >&2; exit 1"}, env, stdin)
	}
	_, err := c.Run(context.Background(), Request{Operation: "test", Model: "m", Prompt: "p"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error does not surface stderr: %v", err)
	}
}

// TestRunTimeoutHonorsAdapterDeadline: the adapter applies its own timeout on
// top of the caller's context, so a hung `claude -p` cannot stall an
// automation past the adapter budget.
func TestRunTimeoutHonorsAdapterDeadline(t *testing.T) {
	c := NewClaudeCode(ClaudeCodeOptions{Timeout: 50 * time.Millisecond})
	c.run = func(ctx context.Context, bin string, args, env []string, stdin string) ([]byte, []byte, error) {
		<-ctx.Done() // simulate a hung subprocess honoring the kill
		return nil, nil, ctx.Err()
	}
	start := time.Now()
	_, err := c.Run(context.Background(), Request{Operation: "test", Model: "m", Prompt: "p"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Run did not honor the adapter timeout: took %v", time.Since(start))
	}
}
