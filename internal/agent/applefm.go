package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

// AppleFM generates text via Apple's on-device Foundation Models system
// model, reached through a small Swift helper subprocess (compiled by
// `axon init`; see applefm_setup.go). JSON over stdin/stdout keeps the Go
// binary pure Go — no cgo (ADR-013 pattern, ADR-015). Classify tier only:
// the model's context window is small and shared with the response.
// Safe for concurrent use: each call spawns its own process.
type AppleFM struct {
	helper  string
	timeout time.Duration
	goos    string // runtime.GOOS; overridable in tests

	// run executes the helper; injectable so tests don't need the binary.
	run func(ctx context.Context, bin string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

// NewAppleFM constructs the adapter. helperPath is the compiled Swift helper
// (config models.apple_helper, default config.DefaultAppleLMHelperPath()).
func NewAppleFM(helperPath string) *AppleFM {
	return &AppleFM{
		helper:  helperPath,
		timeout: 120 * time.Second,
		goos:    runtime.GOOS,
		run:     execAppleLMHelper,
	}
}

// AuthMode reports "local": no subscription, no API key, no cost.
func (a *AppleFM) AuthMode() string { return "local" }

type appleFMRequest struct {
	System    string          `json:"system,omitempty"`
	Prompt    string          `json:"prompt"`
	MaxTokens int             `json:"max_tokens"`
	Schema    json.RawMessage `json:"schema,omitempty"`
}

type appleFMResponse struct {
	Text string `json:"text"`
}

// appleFMMaxResponseTokens bounds the helper's generation; classify-tier
// outputs are short by design.
const appleFMMaxResponseTokens = 1024

// Run executes one generation through the helper.
func (a *AppleFM) Run(ctx context.Context, req Request) (*Response, error) {
	if a.goos != "darwin" {
		return nil, fmt.Errorf("apple foundation models: requires macOS (running on %s) — use ollama:<model> or a Claude model for this tier", a.goos)
	}
	body, err := json.Marshal(appleFMRequest{
		System: req.System, Prompt: req.Prompt,
		MaxTokens: appleFMMaxResponseTokens, Schema: req.OutputSchema,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	stdout, stderr, err := a.run(ctx, a.helper, nil, body)
	if err != nil {
		return nil, fmt.Errorf("apple lm helper %s: %w: %s", a.helper, err, appleFMOutput(stdout, stderr))
	}
	var out appleFMResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return nil, fmt.Errorf("apple lm: decode helper response: %w", err)
	}
	// The framework reports no token usage; zero counts make the token
	// manager fall back to its heuristic estimate (docs/07 §3).
	return &Response{Text: out.Text, Model: config.AppleFoundationModel}, nil
}

// CheckAvailability reports whether the on-device model can serve requests
// (used by configure convergence and doctor). Cheap: no generation happens.
func (a *AppleFM) CheckAvailability(ctx context.Context) error {
	if a.goos != "darwin" {
		return fmt.Errorf("apple foundation models: requires macOS (running on %s)", a.goos)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, stderr, err := a.run(ctx, a.helper, []string{"--check-availability"}, nil)
	if err != nil {
		return fmt.Errorf("apple lm availability: %w: %s", err, appleFMOutput(stdout, stderr))
	}
	return nil
}

// appleFMOutput assembles helper output for a failure message: stderr first,
// then stdout — capped, because the message is persisted (ledger, events).
func appleFMOutput(stdout, stderr []byte) string {
	const capPerStream = 1024
	trunc := func(b []byte) string {
		s := strings.TrimSpace(string(b))
		if len(s) > capPerStream {
			s = s[:capPerStream] + "… (truncated)"
		}
		return s
	}
	parts := make([]string, 0, 2)
	if s := trunc(stderr); s != "" {
		parts = append(parts, s)
	}
	if s := trunc(stdout); s != "" {
		parts = append(parts, "stdout: "+s)
	}
	return strings.Join(parts, "; ")
}

// execAppleLMHelper is the real subprocess executor (WaitDelay guard as in
// execClaude / the embeddings helper executor).
func execAppleLMHelper(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// compile-time assertion that *AppleFM satisfies Agent.
var _ Agent = (*AppleFM)(nil)
