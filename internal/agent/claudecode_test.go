package agent

import (
	"context"
	"strings"
	"testing"
)

func TestClaudeCodeParsesResultAndUsage(t *testing.T) {
	sample := `{
	  "result": "Here is the summary.",
	  "session_id": "abc-123",
	  "stop_reason": "end_turn",
	  "usage": {"input_tokens": 1200, "output_tokens": 340, "cache_creation_input_tokens": 10, "cache_read_input_tokens": 500},
	  "total_cost_usd": 0
	}`
	resp, err := parseClaudeJSON([]byte(sample), "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Here is the summary." {
		t.Errorf("text = %q", resp.Text)
	}
	if resp.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", resp.Model)
	}
	if resp.Usage.InputTokens != 1200 || resp.Usage.OutputTokens != 340 ||
		resp.Usage.CacheRead != 500 || resp.Usage.CacheWrite != 10 {
		t.Errorf("usage mismatch: %+v", resp.Usage)
	}
}

func TestClaudeCodeParseRejectsGarbage(t *testing.T) {
	if _, err := parseClaudeJSON([]byte("not json"), "m"); err == nil {
		t.Error("expected parse error for non-JSON")
	}
	if _, err := parseClaudeJSON([]byte("   "), "m"); err == nil {
		t.Error("expected error for empty output")
	}
}

func TestClaudeCodeBuildArgs(t *testing.T) {
	c := NewClaudeCode(ClaudeCodeOptions{})
	args := c.buildArgs(Request{Model: "claude-haiku-4-5", System: "be concise"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--print", "--output-format json", "--max-turns 1", "--tools", "--bare", "--model claude-haiku-4-5", "--append-system-prompt be concise"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q; got %q", want, joined)
		}
	}
}

func TestClaudeCodeStripsAPIKeyInSubscriptionMode(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-be-stripped")
	c := NewClaudeCode(ClaudeCodeOptions{AuthMode: "subscription", ConfigDir: "/tmp/cfg", OAuthToken: "tok"})
	env := c.buildEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY leaked into subscription-mode env")
		}
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CLAUDE_CONFIG_DIR=/tmp/cfg") {
		t.Error("CLAUDE_CONFIG_DIR not set")
	}
	if !strings.Contains(joined, "CLAUDE_CODE_OAUTH_TOKEN=tok") {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN not set")
	}
}

func TestClaudeCodeKeepsAPIKeyInAPIKeyMode(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-keep")
	c := NewClaudeCode(ClaudeCodeOptions{AuthMode: "api_key"})
	if !strings.Contains(strings.Join(c.buildEnv(), "\n"), "ANTHROPIC_API_KEY=sk-ant-keep") {
		t.Error("api_key mode should keep ANTHROPIC_API_KEY")
	}
}

func TestClaudeCodeRunUsesInjectedExecutor(t *testing.T) {
	c := NewClaudeCode(ClaudeCodeOptions{})
	var gotStdin string
	var gotArgs []string
	c.run = func(ctx context.Context, bin string, args, env []string, stdin string) ([]byte, []byte, error) {
		gotArgs = args
		gotStdin = stdin
		return []byte(`{"result":"ok","usage":{"input_tokens":5,"output_tokens":3}}`), nil, nil
	}
	resp, err := c.Run(context.Background(), Request{Operation: "test", Model: "m", System: "sys", Prompt: "the prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || resp.Usage.InputTokens != 5 {
		t.Errorf("unexpected response %+v", resp)
	}
	if gotStdin != "the prompt" {
		t.Errorf("prompt not passed on stdin: %q", gotStdin)
	}
	if strings.Join(gotArgs, " ") == "" {
		t.Error("no args built")
	}
}
