package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestAgenticEndToEnd drives a real tool-using agentic run: the claude CLI
// spawns the axon binary's MCP server (--tools vault_search) against a
// scratch vault, and the streaming adapter parses the run. Requires the
// claude CLI + an authenticated login and SPENDS REAL TOKENS, so it is
// env-gated: set AXON_E2E_AGENTIC=1, AXON_E2E_BINARY=<built axon binary>,
// AXON_E2E_CONFIG=<scratch config.yaml>. Skipped in CI and short mode.
func TestAgenticEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}
	if os.Getenv("AXON_E2E_AGENTIC") == "" {
		t.Skip("set AXON_E2E_AGENTIC=1 to run (spends real tokens)")
	}
	binary, cfg := os.Getenv("AXON_E2E_BINARY"), os.Getenv("AXON_E2E_CONFIG")
	if binary == "" || cfg == "" {
		t.Skip("set AXON_E2E_BINARY and AXON_E2E_CONFIG to a scratch setup")
	}

	c := NewClaudeCode(ClaudeCodeOptions{
		MCPCommand: binary,
		MCPArgs:    []string{"mcp", "--config", cfg, "--profile", os.Getenv("AXON_E2E_PROFILE")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	resp, err := c.Run(ctx, Request{
		Model:  "claude-haiku-4-5",
		System: "Use the vault_search tool once, then answer in one short sentence.",
		Prompt: "Search the vault for 'README' and tell me one path you found.",
		Tools:  []string{"vault_search"}, MaxTurns: 3,
	})
	if err != nil {
		t.Fatalf("agentic e2e: %v", err)
	}
	// A real tool round trip takes ≥2 turns; 1 turn means the model answered
	// (or hallucinated tool syntax) without the MCP tool being available.
	if resp.Turns < 2 || strings.TrimSpace(resp.Text) == "" {
		t.Fatalf("resp = %+v (turns %d), want a genuine tool-using answer (≥2 turns)", resp, resp.Turns)
	}
	if resp.Usage.InputTokens+resp.Usage.OutputTokens == 0 {
		t.Fatal("no usage accumulated")
	}
	t.Logf("agentic e2e: %d turns, %d in / %d out tokens, answer: %q",
		resp.Turns, resp.Usage.InputTokens, resp.Usage.OutputTokens, strings.TrimSpace(resp.Text))
}
