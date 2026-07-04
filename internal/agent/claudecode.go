package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrRunBudgetExceeded reports an agentic run killed by the streaming budget
// enforcer (ADR-017). The returned Response carries the real usage
// accumulated up to the kill, which the token manager MUST ledger.
var ErrRunBudgetExceeded = errors.New("agentic run exceeded its token budget")

const (
	defaultAgenticTurns   = 8
	maxAgenticTurns       = 32
	defaultAgenticTimeout = 10 * time.Minute
)

// ClaudeCode is the default agent adapter: it shells out to the Claude Code CLI
// in headless print mode (`claude -p`), authenticated by the profile's
// subscription/enterprise login. Flags and the JSON output shape are isolated
// here so a CLI change is a one-file fix (docs/06 §4). Verified against the
// Claude Code headless docs AND live CLI behavior: --print, --output-format
// json, --model, --append-system-prompt, --max-turns, --tools,
// --setting-sources, --strict-mcp-config. NOT --bare: it disables credential
// lookup (the OAuth token is never read), killing headless auth.
//
// Cardinal rule: this is reached only via the token manager (Component 07).
type ClaudeCode struct {
	bin            string
	configDir      string // CLAUDE_CONFIG_DIR (profile-isolated auth)
	oauthToken     string // resolved CLAUDE_CODE_OAUTH_TOKEN (may be empty: rely on `claude login`)
	tokenErr       error  // why oauthToken is empty when the config named one (see Run)
	authMode       string
	timeout        time.Duration
	agenticTimeout time.Duration
	mcpCommand     string
	mcpArgs        []string

	// run executes the command; injectable so tests don't spawn the real CLI.
	run func(ctx context.Context, bin string, args, env []string, stdin string) (stdout []byte, stderr []byte, err error)
	// runStream executes the command feeding stdout lines to onLine; when
	// onLine returns true the process group is killed (agentic budget kill).
	runStream func(ctx context.Context, bin string, args, env []string, stdin string, onLine func(line []byte) (stop bool)) (stderr []byte, err error)
}

// ClaudeCodeOptions configures the adapter.
type ClaudeCodeOptions struct {
	Bin        string // default "claude"
	ConfigDir  string
	OAuthToken string
	// OAuthTokenErr carries the secret-resolution failure when the config
	// referenced an oauth token that could not be resolved (e.g. env var unset
	// because no .env was loaded). It is surfaced in run failures so an auth
	// error explains itself instead of dead-ending at "not logged in".
	OAuthTokenErr error
	AuthMode      string
	Timeout       time.Duration // default 120s
	// MCPCommand/MCPArgs launch the AXON MCP server for agentic runs
	// (the axon binary + ["mcp", "--config", …, "--profile", …]); the
	// per-call read-only tool filter is appended as --tools <csv>.
	MCPCommand string
	MCPArgs    []string
	// AgenticTimeout bounds a tool-using run (default 10m; one-shot keeps Timeout).
	AgenticTimeout time.Duration
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
	agenticTimeout := opts.AgenticTimeout
	if agenticTimeout == 0 {
		agenticTimeout = defaultAgenticTimeout
	}
	return &ClaudeCode{
		bin:            bin,
		configDir:      opts.ConfigDir,
		oauthToken:     opts.OAuthToken,
		tokenErr:       opts.OAuthTokenErr,
		authMode:       opts.AuthMode,
		timeout:        timeout,
		agenticTimeout: agenticTimeout,
		mcpCommand:     opts.MCPCommand,
		mcpArgs:        opts.MCPArgs,
		run:            execClaude,
		runStream:      execClaudeStream,
	}
}

// AuthMode reports the configured auth mode.
func (c *ClaudeCode) AuthMode() string {
	if c.authMode == "" {
		return "subscription"
	}
	return c.authMode
}

// Run executes one headless turn (or an agentic tool-using run when
// req.Tools is set) and returns the parsed result + usage.
func (c *ClaudeCode) Run(ctx context.Context, req Request) (*Response, error) {
	if len(req.Tools) > 0 {
		return c.runAgentic(ctx, req)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := c.buildArgs(req)
	env := c.buildEnv()

	stdout, stderr, err := c.run(ctx, c.bin, args, env, req.Prompt)
	if err != nil {
		runErr := fmt.Errorf("claude -p %q: %w: %s", req.Operation, err, failureOutput(stdout, stderr))
		// If the config named an oauth token that never resolved, the failure is
		// almost certainly that — say so, instead of dead-ending at the CLI's
		// "not logged in".
		if c.oauthToken == "" && c.tokenErr != nil {
			return nil, fmt.Errorf("%w (oauth token unresolved: %v — is the secret in ~/.axon/.env, and was --env passed / does the service unit include it?)", runErr, c.tokenErr)
		}
		return nil, runErr
	}
	return parseClaudeJSON(stdout, req.Model)
}

// buildArgs assembles the headless argv. The prompt itself is passed on stdin
// (avoids arg-length limits), so -p takes no positional prompt here.
func (c *ClaudeCode) buildArgs(req Request) []string {
	if len(req.Tools) > 0 {
		return c.buildAgenticArgs(req)
	}
	args := []string{
		"--print",
		"--output-format", "json",
		"--max-turns", "1",
		"--tools", "", // pure text generation: no tool use
		// Determinism WITHOUT --bare: --bare skips credential lookup entirely
		// (CLAUDE_CODE_OAUTH_TOKEN ignored → "Not logged in" on every headless
		// run), so isolation is assembled from the surgical flags instead:
		// no settings/hooks from any source, no external MCP servers.
		"--setting-sources", "",
		"--strict-mcp-config",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}
	return args
}

// buildAgenticArgs assembles the tool-using argv (ADR-017): stream-json for
// per-turn usage (--verbose is mandatory with it in print mode), no built-in
// tools, only the strict inline MCP config, an explicit per-call allowlist,
// no session persistence, no settings/hooks. With --tools "" there are no
// built-in tools for hooks to guard; the allowlisted read-only MCP set is the
// entire callable surface.
func (c *ClaudeCode) buildAgenticArgs(req Request) []string {
	turns := req.MaxTurns
	if turns <= 0 {
		turns = defaultAgenticTurns
	}
	if turns > maxAgenticTurns {
		turns = maxAgenticTurns
	}
	allowed := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		allowed[i] = "mcp__axon__" + t
	}
	mcpCfg, _ := c.buildMCPConfig(req.Tools, req.DryRunTools) // runAgentic validates before calling
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", strconv.Itoa(turns),
		"--tools", "", // no built-in tools: the MCP allowlist is the whole surface
		"--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config", mcpCfg,
		"--allowedTools", strings.Join(allowed, ","),
		"--no-session-persistence",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}
	return args
}

// buildMCPConfig renders the inline --mcp-config JSON: the AXON binary
// serving ONLY the call's tools (server-side enforcement, FR-86).
func (c *ClaudeCode) buildMCPConfig(tools []string, dryRun bool) (string, error) {
	if c.mcpCommand == "" {
		return "", fmt.Errorf("agentic run requested but no MCP command wired (ClaudeCodeOptions.MCPCommand)")
	}
	args := append(append([]string{}, c.mcpArgs...), "--tools", strings.Join(tools, ","))
	if dryRun {
		args = append(args, "--dry-run")
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"axon": map[string]any{"command": c.mcpCommand, "args": args},
		},
	}
	raw, err := json.Marshal(cfg)
	return string(raw), err
}

// errKilledByBudget is what execClaudeStream reports after an onLine-requested
// kill; runAgentic maps it onto ErrRunBudgetExceeded with context.
var errKilledByBudget = errors.New("killed: run budget exceeded")

// runAgentic executes a tool-using run with streaming per-turn budget
// enforcement (ADR-017, FR-85): usage accumulates as turns complete; the
// subprocess is killed the moment the cap is crossed, and the accumulated
// REAL usage rides back with ErrRunBudgetExceeded so the chokepoint can
// ledger actual spend, not an estimate.
func (c *ClaudeCode) runAgentic(ctx context.Context, req Request) (*Response, error) {
	if _, err := c.buildMCPConfig(req.Tools, req.DryRunTools); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.agenticTimeout)
	defer cancel()

	var acc Usage
	var final *Response
	onLine := func(line []byte) bool {
		f, _ := accumulateStreamEvent(line, &acc)
		if f != nil {
			final = f
		}
		return req.RunBudgetTokens > 0 &&
			acc.InputTokens+acc.OutputTokens > req.RunBudgetTokens
	}

	stderr, err := c.runStream(ctx, c.bin, c.buildArgs(req), c.buildEnv(), req.Prompt, onLine)
	partial := &Response{Model: req.Model, Usage: acc}
	if errors.Is(err, errKilledByBudget) {
		return partial, fmt.Errorf("%w after %d tokens (cap %d)", ErrRunBudgetExceeded,
			acc.InputTokens+acc.OutputTokens, req.RunBudgetTokens)
	}
	if err != nil {
		// Turn-limit exhaustion or any other failure: surface with the
		// partial usage so the ledger records real spend.
		return partial, fmt.Errorf("claude -p agentic %q: %w: %s", req.Operation, err, failureOutput(nil, stderr))
	}
	if final == nil {
		return partial, fmt.Errorf("claude -p agentic %q: stream ended without a result event", req.Operation)
	}
	final.Model = req.Model
	return final, nil
}

// streamEvent mirrors the stream-json lines we consume (verified against the
// live CLI; a schema change is a one-function fix, like parseClaudeJSON).
type streamEvent struct {
	Type     string       `json:"type"`
	Result   string       `json:"result"`
	NumTurns int          `json:"num_turns"`
	Usage    *claudeUsage `json:"usage"`
	Message  *struct {
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u *claudeUsage) toUsage() Usage {
	return Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CacheRead: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens}
}

// accumulateStreamEvent folds one stream-json line into the running total and
// returns the final Response when the line is the terminal result event.
func accumulateStreamEvent(line []byte, acc *Usage) (*Response, bool) {
	var ev streamEvent
	if err := json.Unmarshal(bytes.TrimSpace(line), &ev); err != nil {
		return nil, false // partial/non-JSON line: ignore
	}
	switch ev.Type {
	case "assistant":
		if ev.Message != nil && ev.Message.Usage != nil {
			u := ev.Message.Usage.toUsage()
			acc.InputTokens += u.InputTokens
			acc.OutputTokens += u.OutputTokens
			acc.CacheRead += u.CacheRead
			acc.CacheWrite += u.CacheWrite
			return nil, true
		}
	case "result":
		resp := &Response{Text: ev.Result, Turns: ev.NumTurns, Usage: *acc}
		if ev.Usage != nil {
			resp.Usage = ev.Usage.toUsage() // cumulative from the CLI wins
		}
		return resp, false
	}
	return nil, false
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

// failureOutput assembles the subprocess output for a failure message. Claude
// Code prints many failures (auth, model access) to STDOUT in --print mode, so
// stderr alone is often blank — include both, capped because the message is
// persisted (runs.error, token events).
func failureOutput(stdout, stderr []byte) string {
	const capPerStream = 1024
	parts := make([]string, 0, 2)
	if s := truncate(strings.TrimSpace(string(stderr)), capPerStream); s != "" {
		parts = append(parts, s)
	}
	if s := truncate(strings.TrimSpace(string(stdout)), capPerStream); s != "" {
		parts = append(parts, "stdout: "+s)
	}
	return strings.Join(parts, "; ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
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

// execClaudeStream runs the CLI and feeds stdout to onLine per line; when
// onLine returns true it kills the process group (via the Cancel installed by
// killProcessGroup) and reports errKilledByBudget.
func execClaudeStream(ctx context.Context, bin string, args, env []string, stdin string, onLine func([]byte) bool) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	killProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return stderr.Bytes(), err
	}
	killed := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // result events can be large
	for sc.Scan() {
		if onLine(sc.Bytes()) {
			killed = true
			if cmd.Cancel != nil {
				_ = cmd.Cancel()
			}
			break
		}
	}
	waitErr := cmd.Wait()
	if killed {
		return stderr.Bytes(), errKilledByBudget
	}
	if serr := sc.Err(); serr != nil && waitErr == nil {
		waitErr = serr
	}
	return stderr.Bytes(), waitErr
}

// compile-time assertion that *ClaudeCode satisfies Agent.
var _ Agent = (*ClaudeCode)(nil)
