package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Apple embeds via Apple's on-device NLContextualEmbedding, reached through a
// small Swift helper subprocess (compiled by `axon init`; see apple_setup.go).
// JSON over stdin/stdout keeps the Go binary pure Go — no cgo (ADR-011).
// Safe for concurrent use: each call spawns its own process.
type Apple struct {
	helper  string
	model   string
	dim     int
	timeout time.Duration
	goos    string // runtime.GOOS; overridable in tests

	// run executes the helper; injectable so tests don't need the binary.
	run func(ctx context.Context, bin string, stdin []byte) (stdout, stderr []byte, err error)
}

// NewApple constructs the provider. helperPath is the compiled Swift helper
// (config embeddings.helper, default config.DefaultAppleHelperPath()).
func NewApple(helperPath, model string, dim int) *Apple {
	return &Apple{
		helper:  helperPath,
		model:   model,
		dim:     dim,
		timeout: 120 * time.Second,
		goos:    runtime.GOOS,
		run:     execAppleHelper,
	}
}

// Model reports the configured embedding model identifier.
func (a *Apple) Model() string { return a.model }

// Dim reports the configured/expected vector dimension.
func (a *Apple) Dim() int { return a.dim }

type appleRequest struct {
	Texts []string `json:"texts"`
}

type appleResponse struct {
	Model   string      `json:"model"`
	Dim     int         `json:"dim"`
	Vectors [][]float32 `json:"vectors"`
}

// Embed returns one vector per input text via one helper invocation.
func (a *Apple) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if a.goos != "darwin" {
		return nil, fmt.Errorf("apple embeddings: provider requires macOS (running on %s) — set embeddings.provider: ollama", a.goos)
	}
	body, err := json.Marshal(appleRequest{Texts: texts})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	stdout, stderr, err := a.run(ctx, a.helper, body)
	if err != nil {
		return nil, fmt.Errorf("apple embed helper %s: %w: %s", a.helper, err, subprocessOutput(stdout, stderr))
	}
	var out appleResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return nil, fmt.Errorf("apple embed: decode helper response: %w", err)
	}
	if len(out.Vectors) != len(texts) {
		return nil, fmt.Errorf("apple embed: got %d vectors for %d inputs", len(out.Vectors), len(texts))
	}
	for i, v := range out.Vectors {
		if a.dim > 0 && len(v) != a.dim {
			return nil, fmt.Errorf("apple embed: vector %d has dim %d, config expects %d (helper reports dim %d — fix embeddings.dim, then run `axon reindex --embeddings`)",
				i, len(v), a.dim, out.Dim)
		}
	}
	return out.Vectors, nil
}

// Healthcheck embeds a probe string, verifying the helper runs and the live
// dimension matches the configured dim.
func (a *Apple) Healthcheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	vecs, err := a.Embed(ctx, []string{"ok"})
	if err != nil {
		return err
	}
	if len(vecs) != 1 {
		return fmt.Errorf("apple healthcheck: unexpected response")
	}
	return nil
}

// subprocessOutput assembles helper output for a failure message: stderr
// first, then stdout — capped, because the message is persisted (ledger,
// events, runs.error).
func subprocessOutput(stdout, stderr []byte) string {
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

// execAppleHelper is the real subprocess executor.
func execAppleHelper(ctx context.Context, bin string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Bound Wait even if something holds the pipes after a kill (same guard as
	// the Claude adapter; see internal/agent/claudecode.go execClaude).
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// compile-time assertion that *Apple satisfies Provider.
var _ Provider = (*Apple)(nil)
