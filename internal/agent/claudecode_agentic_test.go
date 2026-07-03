package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func agenticAdapter() *ClaudeCode {
	return NewClaudeCode(ClaudeCodeOptions{
		MCPCommand: "/usr/local/bin/axon",
		MCPArgs:    []string{"mcp", "--config", "/cfg/config.yaml", "--profile", "personal"},
	})
}

func TestBuildArgsOneShotUnchanged(t *testing.T) {
	c := agenticAdapter()
	got := c.buildArgs(Request{Model: "m", System: "s"})
	want := []string{
		"--print", "--output-format", "json", "--max-turns", "1",
		"--tools", "", "--setting-sources", "", "--strict-mcp-config",
		"--model", "m", "--append-system-prompt", "s",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("one-shot argv changed:\n got %q\nwant %q", got, want)
	}
}

func TestBuildArgsAgenticShape(t *testing.T) {
	c := agenticAdapter()
	got := c.buildArgs(Request{
		Model: "m", System: "s",
		Tools: []string{"vault_search", "vault_read"}, MaxTurns: 6,
	})
	joined := strings.Join(got, " ") + " "
	for _, must := range []string{
		"--output-format stream-json ",
		"--verbose ", // stream-json in print mode requires it (verified live)
		"--max-turns 6 ",
		"--tools  ", // still empty: no built-in tools
		"--strict-mcp-config ",
		"--mcp-config ",
		"--allowedTools mcp__axon__vault_search,mcp__axon__vault_read ",
		"--no-session-persistence ",
		"--setting-sources  ",
	} {
		if !strings.Contains(joined, must) {
			t.Errorf("agentic argv missing %q:\n%q", must, got)
		}
	}
	if strings.Contains(joined, "--output-format json ") {
		t.Error("agentic run must not use plain json output")
	}
}

func TestBuildArgsAgenticTurnClamp(t *testing.T) {
	c := agenticAdapter()
	for _, tt := range []struct {
		in   int
		want string
	}{{0, "8"}, {-3, "8"}, {100, "32"}, {5, "5"}} {
		got := strings.Join(c.buildArgs(Request{Tools: []string{"vault_read"}, MaxTurns: tt.in}), " ") + " "
		if !strings.Contains(got, "--max-turns "+tt.want+" ") {
			t.Errorf("MaxTurns %d: argv %q, want --max-turns %s", tt.in, got, tt.want)
		}
	}
}

func TestBuildMCPConfig(t *testing.T) {
	c := agenticAdapter()
	raw, err := c.buildMCPConfig([]string{"vault_search", "tokens_status"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("mcp config not valid JSON: %v\n%s", err, raw)
	}
	ax, ok := cfg.MCPServers["axon"]
	if !ok || ax.Command != "/usr/local/bin/axon" {
		t.Fatalf("axon server entry wrong: %+v", cfg.MCPServers)
	}
	wantArgs := []string{"mcp", "--config", "/cfg/config.yaml", "--profile", "personal", "--tools", "vault_search,tokens_status"}
	if !slices.Equal(ax.Args, wantArgs) {
		t.Fatalf("args = %q, want %q", ax.Args, wantArgs)
	}
}

func TestAgenticRunRequiresMCPCommand(t *testing.T) {
	c := NewClaudeCode(ClaudeCodeOptions{}) // no MCPCommand wired
	_, err := c.Run(t.Context(), Request{Tools: []string{"vault_read"}, Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "MCP") {
		t.Fatalf("err = %v, want missing-MCP-wiring error", err)
	}
}

func streamLines(lines ...string) func(ctx context.Context, bin string, args, env []string, stdin string, onLine func([]byte) bool) ([]byte, error) {
	return func(ctx context.Context, bin string, args, env []string, stdin string, onLine func([]byte) bool) ([]byte, error) {
		for _, l := range lines {
			if onLine([]byte(l)) {
				return nil, errKilledByBudget // what execClaudeStream returns after a kill
			}
		}
		return nil, nil
	}
}

func TestAgenticRunAccumulatesAndCompletes(t *testing.T) {
	c := agenticAdapter()
	c.runStream = streamLines(
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":100,"output_tokens":20}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":300,"output_tokens":50,"cache_read_input_tokens":40}}}`,
		`{"type":"result","result":"the digest","num_turns":2,"usage":{"input_tokens":400,"output_tokens":70,"cache_read_input_tokens":40,"cache_creation_input_tokens":10}}`,
	)
	resp, err := c.Run(t.Context(), Request{
		Model: "m", Prompt: "p", Tools: []string{"vault_read"}, MaxTurns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "the digest" || resp.Turns != 2 {
		t.Fatalf("resp = %+v, want result text + 2 turns", resp)
	}
	// Final result event's cumulative usage wins.
	if resp.Usage.InputTokens != 400 || resp.Usage.OutputTokens != 70 ||
		resp.Usage.CacheRead != 40 || resp.Usage.CacheWrite != 10 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestAgenticRunBudgetKill(t *testing.T) {
	c := agenticAdapter()
	c.runStream = streamLines(
		`{"type":"assistant","message":{"usage":{"input_tokens":900,"output_tokens":50}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":2000,"output_tokens":100}}}`,
		`{"type":"result","result":"never reached","num_turns":9,"usage":{}}`,
	)
	resp, err := c.Run(t.Context(), Request{
		Prompt: "p", Tools: []string{"vault_read"}, RunBudgetTokens: 1000,
	})
	if !errors.Is(err, ErrRunBudgetExceeded) {
		t.Fatalf("err = %v, want ErrRunBudgetExceeded", err)
	}
	if resp == nil {
		t.Fatal("killed run must return accumulated usage")
	}
	// Both turns accumulated: 900+50+2000+100 = 3050.
	if got := resp.Usage.InputTokens + resp.Usage.OutputTokens; got != 3050 {
		t.Fatalf("accumulated tokens = %d, want 3050", got)
	}
	if resp.Text != "" {
		t.Fatalf("killed run has no result text, got %q", resp.Text)
	}
}

func TestAgenticRunStreamErrorSurfacesPartialUsage(t *testing.T) {
	c := agenticAdapter()
	c.runStream = func(ctx context.Context, bin string, args, env []string, stdin string, onLine func([]byte) bool) ([]byte, error) {
		onLine([]byte(`{"type":"assistant","message":{"usage":{"input_tokens":150,"output_tokens":30}}}`))
		return []byte("max turns exceeded"), errors.New("exit status 1")
	}
	resp, err := c.Run(t.Context(), Request{Prompt: "p", Tools: []string{"vault_read"}})
	if err == nil || errors.Is(err, ErrRunBudgetExceeded) {
		t.Fatalf("err = %v, want plain run error", err)
	}
	if resp == nil || resp.Usage.InputTokens != 150 {
		t.Fatalf("partial usage must ride along the error, got %+v", resp)
	}
}