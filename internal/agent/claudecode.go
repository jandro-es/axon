package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ClaudeCode is the default agent adapter: it shells out to the Claude Code CLI
// in headless print mode (`claude -p`), authenticated by the profile's
// subscription/enterprise login. Flags and the JSON output shape are isolated
// here so a CLI change is a one-file fix (docs/06 §4). Verified against the
// Claude Code headless docs: --print, --output-format json, --model,
// --append-system-prompt, --max-turns, --tools, --bare.
//
// Cardinal rule: this is reached only via the token manager (Component 07).
type ClaudeCode struct {
	bin        string
	configDir  string // CLAUDE_CONFIG_DIR (profile-isolated auth)
	oauthToken string // resolved CLAUDE_CODE_OAUTH_TOKEN (may be empty: rely on `claude login`)
	authMode   string
	timeout    time.Duration

	// run executes the command; injectable so tests don't spawn the real CLI.
	run func(ctx context.Context, bin string, args, env []string, stdin string) (stdout []byte, stderr []byte, err error)
}

// ClaudeCodeOptions configures the adapter.
type ClaudeCodeOptions struct {
	Bin        string // default "claude"
	ConfigDir  string
	OAuthToken string
	AuthMode   string
	Timeout    time.Duration // default 120s
}

// NewClaudeCode builds the adapter with the real subprocess executor.
func NewClaudeCode(opts ClaudeCodeOptions) *ClaudeCode {
	bin := opts.Bin
	if bin == "" {
		bin = "claude"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &ClaudeCode{
		bin:        bin,
		configDir:  opts.ConfigDir,
		oauthToken: opts.OAuthToken,
		authMode:   opts.AuthMode,
		timeout:    timeout,
		run:        execClaude,
	}
}

// AuthMode reports the configured auth mode.
func (c *ClaudeCode) AuthMode() string {
	if c.authMode == "" {
		return "subscription"
	}
	return c.authMode
}

// Run executes one headless turn and returns the parsed result + usage.
func (c *ClaudeCode) Run(ctx context.Context, req Request) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := c.buildArgs(req)
	env := c.buildEnv()

	stdout, stderr, err := c.run(ctx, c.bin, args, env, req.Prompt)
	if err != nil {
		return nil, fmt.Errorf("claude -p %q: %w: %s", req.Operation, err, strings.TrimSpace(string(stderr)))
	}
	return parseClaudeJSON(stdout, req.Model)
}

// buildArgs assembles the headless argv. The prompt itself is passed on stdin
// (avoids arg-length limits), so -p takes no positional prompt here.
func (c *ClaudeCode) buildArgs(req Request) []string {
	args := []string{
		"--print",
		"--output-format", "json",
		"--max-turns", "1",
		"--tools", "", // pure text generation: no tool use
		"--bare", // ignore local hooks/skills/MCP/CLAUDE.md for determinism
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}
	return args
}

// buildEnv returns the child environment: inherit the parent, isolate the
// profile's CLAUDE_CONFIG_DIR, supply the OAuth token, and — critically — strip
// ANTHROPIC_API_KEY in non-api_key modes so Claude Code stays on the
// subscription/enterprise path rather than API billing.
func (c *ClaudeCode) buildEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if c.authMode != "api_key" && strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	if c.configDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+c.configDir)
	}
	if c.oauthToken != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+c.oauthToken)
	}
	return env
}

// claudeJSON mirrors the `claude -p --output-format json` schema (the fields we
// consume). Subscription/enterprise reports usage but total_cost_usd is 0 there;
// cost is computed by the token manager only in api_key mode.
type claudeJSON struct {
	Result     string `json:"result"`
	SessionID  string `json:"session_id"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// parseClaudeJSON maps the CLI JSON onto a Response. Falls back to treating the
// whole output as text if it is not the expected JSON object.
func parseClaudeJSON(stdout []byte, model string) (*Response, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("claude -p: empty output")
	}
	var cj claudeJSON
	if err := json.Unmarshal(trimmed, &cj); err != nil {
		return nil, fmt.Errorf("claude -p: parse JSON: %w", err)
	}
	return &Response{
		Text:  cj.Result,
		Model: model,
		Usage: Usage{
			InputTokens:  cj.Usage.InputTokens,
			OutputTokens: cj.Usage.OutputTokens,
			CacheRead:    cj.Usage.CacheReadInputTokens,
			CacheWrite:   cj.Usage.CacheCreationInputTokens,
		},
	}, nil
}

// execClaude is the real subprocess executor.
func execClaude(ctx context.Context, bin string, args, env []string, stdin string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Two guards so a context deadline actually ends the call promptly:
	//  - killProcessGroup (unix) puts the child in its own process group and
	//    kills the whole group on cancel, so helpers the CLI spawned die too
	//    rather than lingering as orphans;
	//  - WaitDelay bounds Wait even if something unkillable still holds the
	//    stdout/stderr pipes after the kill — without it, Wait blocks until
	//    every pipe holder exits (observed on Linux: a grandchild kept the
	//    call alive for its full runtime past the deadline).
	killProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// compile-time assertion that *ClaudeCode satisfies Agent.
var _ Agent = (*ClaudeCode)(nil)
