# Local Model Routing (Ollama + Apple Foundation Models) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route the `classify`/`routine` model tiers to local providers (Ollama chat, Apple Foundation Models) through the token-manager chokepoint, budget-exempt but fully ledgered, with a configurable Claude fall-forward.

**Architecture:** Provider-prefixed model strings in the existing `models.*` config fields resolve to `(provider, model)` pairs; a small `agent.Router` inside the tokens chokepoint dispatches to the right adapter. The Apple adapter reuses ADR-013's compiled-at-init Swift helper pattern verbatim. Spec: `docs/superpowers/specs/2026-07-03-local-model-routing-design.md`; ADR-015; FR-77…FR-80.

**Tech Stack:** Go 1.26, `net/http` (Ollama `/api/chat`), Swift + FoundationModels framework (macOS 26), existing test fakes (`agent.Fake`, injectable subprocess executors, `httptest`).

## Global Constraints

- Go 1.26+, pure Go, no cgo; `gofmt`/`go vet`/`golangci-lint` clean; table-driven tests; errors wrapped with `%w`; `context.Context` through every I/O call.
- Cardinal rule 1 (generalized by ADR-015): **no generative call — Claude or local — bypasses the token manager.** `tokens` stays the only importer of `agent`.
- `internal/config` may be imported by anyone; leaf packages (`agent`, `embeddings`, `db`, …) never import each other.
- Local calls: ledgered, `cost_usd` null, **no** `AddBudgetUsage`, never defer/deny/downgrade (FR-78).
- Fallback: `models.local_fallback: claude | fail`, default `claude`; one local retry first (FR-79).
- `apple` is valid on `classify` only; no local provider on `synthesis` (spec Decision 4).
- Every task ends with `go test ./...` green and a commit on `feature/local-model-routing`.
- Work happens on the existing branch `feature/local-model-routing`.

---

### Task 1: Config — `ModelRef` parsing, new `ModelsConfig` fields, validation

**Files:**
- Modify: `internal/config/types.go` (ModelsConfig, ~line 160)
- Modify: `internal/config/paths.go` (constants, after line 62)
- Modify: `internal/config/load.go` (`Config.Validate`, line 44)
- Create: `internal/config/models.go`
- Test: `internal/config/models_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `config.ModelRef{Provider, Model string}`, `config.ParseModelRef(s string) ModelRef`, provider constants `config.ProviderClaude/ProviderOllama/ProviderApple` (values `"claude"`, `"ollama"`, `"apple"`), `config.AppleFoundationModel = "apple-foundation-v1"`, `config.DefaultAppleLMHelperPath() string`, `ModelsConfig` fields `OllamaHost`, `LocalFallback`, `AppleHelper`, method `(ModelsConfig) Fallback() string`, and `validateLocalRouting(m ModelsConfig) error` wired into `Config.Validate`.

- [ ] **Step 1: Write the failing test**

`internal/config/models_test.go`:

```go
package config

import "testing"

func TestParseModelRef(t *testing.T) {
	tests := []struct {
		in       string
		provider string
		model    string
	}{
		{"claude-haiku-4-5", ProviderClaude, "claude-haiku-4-5"},
		{"ollama:qwen3:8b", ProviderOllama, "qwen3:8b"},
		{"apple", ProviderApple, AppleFoundationModel},
		{"", ProviderClaude, ""},
	}
	for _, tt := range tests {
		got := ParseModelRef(tt.in)
		if got.Provider != tt.provider || got.Model != tt.model {
			t.Errorf("ParseModelRef(%q) = %+v, want {%s %s}", tt.in, got, tt.provider, tt.model)
		}
	}
}

func TestValidateLocalRouting(t *testing.T) {
	base := ModelsConfig{Classify: "claude-haiku-4-5", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8"}
	tests := []struct {
		name    string
		mutate  func(*ModelsConfig)
		wantErr bool
	}{
		{"all claude", func(m *ModelsConfig) {}, false},
		{"ollama classify", func(m *ModelsConfig) { m.Classify = "ollama:qwen3:8b" }, false},
		{"apple classify", func(m *ModelsConfig) { m.Classify = "apple" }, false},
		{"apple routine rejected", func(m *ModelsConfig) { m.Routine = "apple" }, true},
		{"local synthesis rejected", func(m *ModelsConfig) { m.Synthesis = "ollama:qwen3:8b" }, true},
		{"empty ollama model rejected", func(m *ModelsConfig) { m.Classify = "ollama:" }, true},
		{"bad fallback rejected", func(m *ModelsConfig) { m.LocalFallback = "retry" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			tt.mutate(&m)
			err := validateLocalRouting(m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateLocalRouting: err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestModelsFallbackDefault(t *testing.T) {
	if got := (ModelsConfig{}).Fallback(); got != "claude" {
		t.Fatalf("default fallback = %q, want claude", got)
	}
	if got := (ModelsConfig{LocalFallback: "fail"}).Fallback(); got != "fail" {
		t.Fatalf("fallback = %q, want fail", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestParseModelRef|TestValidateLocalRouting|TestModelsFallbackDefault' -v`
Expected: FAIL — `undefined: ParseModelRef`, `undefined: validateLocalRouting`.

- [ ] **Step 3: Implement**

`internal/config/models.go` (new file):

```go
package config

import (
	"fmt"
	"strings"
)

// Model providers (ADR-015). Claude is the default; local providers are
// selected by prefixing the tier's model string.
const (
	ProviderClaude = "claude"
	ProviderOllama = "ollama"
	ProviderApple  = "apple"
)

// ModelRef is a parsed models.* tier value: which adapter serves it and the
// concrete model string that adapter receives.
type ModelRef struct {
	Provider string
	Model    string
}

// ParseModelRef resolves a tier string to its provider: "ollama:<model>" →
// Ollama, "apple" → the on-device Foundation Models system model, anything
// else → a Claude model string exactly as before (backward compatible).
func ParseModelRef(s string) ModelRef {
	if s == ProviderApple {
		return ModelRef{Provider: ProviderApple, Model: AppleFoundationModel}
	}
	if rest, ok := strings.CutPrefix(s, ProviderOllama+":"); ok {
		return ModelRef{Provider: ProviderOllama, Model: rest}
	}
	return ModelRef{Provider: ProviderClaude, Model: s}
}

// Fallback returns the local-failure policy, defaulting to "claude"
// (fall forward through the normal budget path — FR-79).
func (m ModelsConfig) Fallback() string {
	if m.LocalFallback == "" {
		return "claude"
	}
	return m.LocalFallback
}

// validateLocalRouting applies the ADR-015 cross-field rules that struct tags
// can't express. Empty tier strings are skipped (profiles are partial
// overrides); struct-tag `required` covers the top-level config.
func validateLocalRouting(m ModelsConfig) error {
	if m.Synthesis != "" && ParseModelRef(m.Synthesis).Provider != ProviderClaude {
		return fmt.Errorf("models.synthesis must be a Claude model (got %q): local providers are classify/routine only", m.Synthesis)
	}
	if m.Routine != "" && ParseModelRef(m.Routine).Provider == ProviderApple {
		return fmt.Errorf("models.routine cannot be %q: the Apple on-device model's context window limits it to the classify tier", m.Routine)
	}
	for _, tier := range []string{m.Classify, m.Routine} {
		if ref := ParseModelRef(tier); ref.Provider == ProviderOllama && ref.Model == "" {
			return fmt.Errorf("models tier %q names ollama with no model (use ollama:<model>, e.g. ollama:qwen3:8b)", tier)
		}
	}
	if m.LocalFallback != "" && m.LocalFallback != "claude" && m.LocalFallback != "fail" {
		return fmt.Errorf("models.local_fallback must be claude or fail (got %q)", m.LocalFallback)
	}
	return nil
}
```

`internal/config/types.go` — extend `ModelsConfig` (line 160), keeping the doc comment style:

```go
// ModelsConfig names the preferred model per operation class. Claude strings
// are passed to `claude -p --model`; "ollama:<model>" and "apple" route the
// tier to a local provider through the same token-manager chokepoint
// (ADR-015). synthesis is always Claude (validated).
type ModelsConfig struct {
	Classify  string `yaml:"classify" validate:"required"`
	Routine   string `yaml:"routine" validate:"required"`
	Synthesis string `yaml:"synthesis" validate:"required"`
	// OllamaHost is the Ollama server for local chat tiers (default
	// http://localhost:11434). Independent of embeddings.host.
	OllamaHost string `yaml:"ollama_host,omitempty"`
	// LocalFallback governs local-provider failures: "claude" (default)
	// falls forward through the normal budget path; "fail" surfaces the error.
	LocalFallback string `yaml:"local_fallback,omitempty"`
	// AppleHelper overrides the Foundation Models helper binary path.
	// Default: DefaultAppleLMHelperPath(). Ignored unless a tier is "apple".
	AppleHelper string `yaml:"apple_helper,omitempty"`
}
```

`internal/config/paths.go` — after the `AppleEmbeddingModel`/`AppleEmbeddingDim` block (line 62):

```go
// AppleFoundationModel identifies the on-device Foundation Models system
// model in ledger rows and ModelRefs (ADR-015). Versioned like the
// embeddings identifier so a future model change is visible in the ledger.
const AppleFoundationModel = "apple-foundation-v1"

// DefaultAppleLMHelperPath is the compiled Foundation Models helper.
// Machine-level (outside profile isolation), like the embeddings helper.
func DefaultAppleLMHelperPath() string {
	return filepath.Join(AxonHome(), "bin", "axon-apple-lm")
}
```

`internal/config/load.go` — extend `Config.Validate` (line 44). Add after the active-profile check, validating the top-level models and every profile override (check the actual `Config` struct field names in `types.go` — top-level `Models ModelsConfig` and `Profiles map[string]Profile` with `Profile.Models`):

```go
	if err := validateLocalRouting(c.Models); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	for name, p := range c.Profiles {
		if err := validateLocalRouting(p.Models); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: PASS (all, including pre-existing).

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): ModelRef parsing + local-routing fields and validation (FR-77, ADR-015)"
```

---

### Task 2: Agent — `Router` + `Request` extensions

**Files:**
- Modify: `internal/agent/agent.go`
- Create: `internal/agent/router.go`
- Test: `internal/agent/router_test.go`

**Interfaces:**
- Consumes: `config.ProviderClaude/Ollama/Apple`.
- Produces: `agent.Router{Claude, Ollama, Apple Agent}` with `Resolve(provider string) (Agent, error)`; `Request` gains `JSONOutput bool` and `OutputSchema json.RawMessage`.

- [ ] **Step 1: Write the failing test**

`internal/agent/router_test.go`:

```go
package agent

import "testing"

func TestRouterResolve(t *testing.T) {
	fake := NewFake()
	r := Router{Claude: fake}

	tests := []struct {
		provider string
		wantErr  bool
	}{
		{"claude", false},
		{"", false}, // empty = claude, defensive default
		{"ollama", true},
		{"apple", true},
		{"gemini", true},
	}
	for _, tt := range tests {
		got, err := r.Resolve(tt.provider)
		if (err != nil) != tt.wantErr {
			t.Errorf("Resolve(%q): err=%v, wantErr=%v", tt.provider, err, tt.wantErr)
		}
		if !tt.wantErr && got != Agent(fake) {
			t.Errorf("Resolve(%q) returned wrong adapter", tt.provider)
		}
	}

	r.Ollama = fake
	if _, err := r.Resolve("ollama"); err != nil {
		t.Fatalf("Resolve(ollama) with adapter set: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestRouterResolve -v`
Expected: FAIL — `undefined: Router`.

- [ ] **Step 3: Implement**

`internal/agent/router.go`:

```go
package agent

import (
	"fmt"

	"github.com/jandro-es/axon/internal/config"
)

// Router selects the adapter for a resolved model provider (ADR-015). A nil
// field means "not configured on this install"; Resolve surfaces that as an
// error so the token manager's fallback ladder can react. The zero value with
// only Claude set behaves exactly like the pre-ADR-015 single adapter.
type Router struct {
	Claude Agent
	Ollama Agent
	Apple  Agent
}

// Resolve returns the adapter for a provider, or an actionable error.
func (r Router) Resolve(provider string) (Agent, error) {
	switch provider {
	case "", config.ProviderClaude:
		if r.Claude == nil {
			return nil, fmt.Errorf("no claude adapter configured")
		}
		return r.Claude, nil
	case config.ProviderOllama:
		if r.Ollama == nil {
			return nil, fmt.Errorf("no ollama adapter configured (a models.* tier references ollama: but the router has no Ollama adapter)")
		}
		return r.Ollama, nil
	case config.ProviderApple:
		if r.Apple == nil {
			return nil, fmt.Errorf("no apple adapter configured (models.* references apple; darwin/arm64 with the helper compiled is required)")
		}
		return r.Apple, nil
	default:
		return nil, fmt.Errorf("unknown model provider %q", provider)
	}
}
```

`internal/agent/agent.go` — extend `Request` (line 26) and its doc:

```go
// Request is one unit of work sent to a model. Operation labels the call site
// (e.g. "ingest.enrich", "automation.daily-log") for ledgering. Model is the
// resolved model string (passed to `claude -p --model`, or the Ollama model
// tag, or the Apple model identifier).
type Request struct {
	Operation string
	Model     string
	System    string
	Prompt    string
	// JSONOutput hints JSON mode to providers that support it (Ollama
	// format:"json"). Claude adapters ignore it.
	JSONOutput bool
	// OutputSchema optionally constrains output via guided generation
	// (Apple Foundation Models). nil = plain text. Raw JSON Schema.
	OutputSchema json.RawMessage
}
```

Add `"encoding/json"` to the imports in `agent.go`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/router.go internal/agent/router_test.go
git commit -m "feat(agent): provider Router + JSON/schema request hints (ADR-015)"
```

---

### Task 3: Agent — Ollama chat adapter

**Files:**
- Create: `internal/agent/ollama.go`
- Test: `internal/agent/ollama_test.go`

**Interfaces:**
- Consumes: `agent.Request`/`Response`/`Usage` (Task 2's fields included).
- Produces: `agent.NewOllama(host string) *Ollama` (blank host → `http://localhost:11434`), implements `Agent` (`AuthMode() == "local"`), plus `(o *Ollama) Healthcheck(ctx context.Context, model string) error` (one-token chat round trip) for configure/doctor.

- [ ] **Step 1: Write the failing test**

`internal/agent/ollama_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaRun(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s, want /api/chat", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             "qwen3:8b",
			"message":           map[string]string{"role": "assistant", "content": `{"label":"02-Areas"}`},
			"done":              true,
			"prompt_eval_count": 42,
			"eval_count":        7,
		})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL)
	resp, err := o.Run(context.Background(), Request{
		Operation: "test", Model: "qwen3:8b",
		System: "You classify.", Prompt: "classify this",
		JSONOutput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"label":"02-Areas"}` {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v, want 42/7", resp.Usage)
	}
	if gotBody["format"] != "json" {
		t.Errorf("format = %v, want json (JSONOutput hint)", gotBody["format"])
	}
	if gotBody["stream"] != false {
		t.Errorf("stream = %v, want false", gotBody["stream"])
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(msgs))
	}
	if o.AuthMode() != "local" {
		t.Errorf("AuthMode = %q, want local", o.AuthMode())
	}
}

func TestOllamaRunServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	_, err := NewOllama(srv.URL).Run(context.Background(), Request{Model: "nope", Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err = %v, want model-not-found", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestOllama -v`
Expected: FAIL — `undefined: NewOllama`.

- [ ] **Step 3: Implement**

`internal/agent/ollama.go` (HTTP pattern mirrors `internal/embeddings/ollama.go`):

```go
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOllamaHost mirrors the embeddings default; models.ollama_host
// overrides it independently of embeddings.host.
const DefaultOllamaHost = "http://localhost:11434"

// Ollama is the local chat adapter (ADR-015): a models tier written as
// "ollama:<model>" is served by a local Ollama server's /api/chat. It is
// dispatched only by the token manager's router — never called directly —
// so every local call is ledgered (cardinal rule 1, generalized).
type Ollama struct {
	host       string
	httpClient *http.Client
}

// NewOllama constructs the adapter. A blank host falls back to the default.
func NewOllama(host string) *Ollama {
	if host == "" {
		host = DefaultOllamaHost
	}
	return &Ollama{
		host:       strings.TrimRight(host, "/"),
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// AuthMode reports "local": no subscription, no API key, no cost.
func (o *Ollama) AuthMode() string { return "local" }

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format,omitempty"`
	Options  map[string]any      `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Model           string            `json:"model"`
	Message         ollamaChatMessage `json:"message"`
	PromptEvalCount int               `json:"prompt_eval_count"`
	EvalCount       int               `json:"eval_count"`
	Error           string            `json:"error"`
}

// Run executes one chat turn against the local Ollama server.
func (o *Ollama) Run(ctx context.Context, req Request) (*Response, error) {
	msgs := make([]ollamaChatMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, ollamaChatMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, ollamaChatMessage{Role: "user", Content: req.Prompt})

	body := ollamaChatRequest{Model: req.Model, Messages: msgs, Stream: false}
	if req.JSONOutput {
		body.Format = "json"
	}
	raw, err := o.post(ctx, body)
	if err != nil {
		return nil, err
	}
	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama chat: decode: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama chat: %s", out.Error)
	}
	return &Response{
		Text:  out.Message.Content,
		Model: out.Model,
		Usage: Usage{InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount},
	}, nil
}

// Healthcheck verifies the server is reachable and the model is loadable with
// a single-token chat round trip (used by configure convergence and doctor).
func (o *Ollama) Healthcheck(ctx context.Context, model string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := o.post(ctx, ollamaChatRequest{
		Model:    model,
		Messages: []ollamaChatMessage{{Role: "user", Content: "ok"}},
		Stream:   false,
		Options:  map[string]any{"num_predict": 1},
	})
	if err != nil {
		return err
	}
	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("ollama healthcheck: decode: %w", err)
	}
	if out.Error != "" {
		return fmt.Errorf("ollama healthcheck: %s", out.Error)
	}
	return nil
}

func (o *Ollama) post(ctx context.Context, body ollamaChatRequest) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.host+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama chat request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama chat: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// compile-time assertion that *Ollama satisfies Agent.
var _ Agent = (*Ollama)(nil)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/ -run TestOllama -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/ollama.go internal/agent/ollama_test.go
git commit -m "feat(agent): Ollama chat adapter (FR-77)"
```

---

### Task 4: Agent — Apple Foundation Models Swift helper + compile-at-init setup

**Files:**
- Create: `internal/agent/applefm_helper.swift`
- Create: `internal/agent/applefm_setup.go`
- Test: `internal/agent/applefm_setup_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `agent.EnsureAppleLMHelper(ctx context.Context, helperPath string) (changed bool, err error)`, `agent.SwiftAvailable() bool`. Helper protocol (used by Task 5): stdin `{"system":string,"prompt":string,"max_tokens":int,"schema":object?}` → stdout `{"text":string}`; `--check-availability` exits 0/3; exit codes: 2 decode, 3 unavailable, 4 context overflow, 5 guardrail refusal, 6 generation error, 7 encode.

**Note:** this deliberately duplicates ~50 lines of `internal/embeddings/apple_setup.go` — `agent` and `embeddings` are leaf packages that must not import each other, and a shared package for two 50-line files isn't warranted (YAGNI; noted in ADR-015's spec).

- [ ] **Step 1: Write the failing test**

`internal/agent/applefm_setup_test.go` (mirrors `internal/embeddings`' fake-compile pattern):

```go
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAppleLMHelperIdempotent(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "axon-apple-lm")

	compiles := 0
	orig := appleLMCompile
	appleLMCompile = func(ctx context.Context, src, dst string) error {
		compiles++
		return os.WriteFile(dst, []byte("#!/bin/sh\n"), 0o755)
	}
	defer func() { appleLMCompile = orig }()

	changed, err := EnsureAppleLMHelper(context.Background(), helper)
	if err != nil || !changed {
		t.Fatalf("first ensure: changed=%v err=%v, want true/nil", changed, err)
	}
	changed, err = EnsureAppleLMHelper(context.Background(), helper)
	if err != nil || changed {
		t.Fatalf("second ensure: changed=%v err=%v, want false/nil (marker skip)", changed, err)
	}
	if compiles != 1 {
		t.Fatalf("compiled %d times, want 1", compiles)
	}
	if _, err := os.Stat(helper + ".src.sha256"); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestEnsureAppleLMHelper -v`
Expected: FAIL — `undefined: EnsureAppleLMHelper`.

- [ ] **Step 3: Write the Swift helper**

`internal/agent/applefm_helper.swift`. The FoundationModels calls (`SystemLanguageModel.default.availability`, `LanguageModelSession`, `GenerationOptions(maximumResponseTokens:)`, `DynamicGenerationSchema`, the `GenerationError` cases, and the GeneratedContent→JSON accessor) must be verified against current Apple docs at implementation time — the *protocol* (JSON shapes, exit codes) is the fixed contract:

```swift
// axon-apple-lm — AXON's Apple Foundation Models helper (ADR-015).
// Compiled by `axon init` from source embedded in the axon binary.
// Protocol: stdin {"system":..., "prompt":..., "max_tokens":..., "schema":...?}
//        → stdout {"text": ...}.
// --check-availability: exit 0 if the on-device model is available, 3 if not.
// Errors: message on stderr + non-zero exit (2 decode, 3 unavailable,
// 4 context overflow, 5 guardrail refusal, 6 generation error, 7 encode).
import Foundation
import FoundationModels

struct Request: Decodable {
    let system: String?
    let prompt: String
    let max_tokens: Int?
    let schema: SchemaSpec?
}
// Flat-object schema subset: string and [string] properties. Enough for the
// classify-tier callers (enrichment metadata, triage labels); anything richer
// falls back to plain text + Go-side validation.
struct SchemaSpec: Decodable {
    struct Property: Decodable { let type: String }
    let properties: [String: Property]?
}
struct Reply: Encodable { let text: String }

func fail(_ msg: String, code: Int32) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

let model = SystemLanguageModel.default

if CommandLine.arguments.contains("--check-availability") {
    switch model.availability {
    case .available:
        print("available")
        exit(0)
    case .unavailable(let reason):
        fail("on-device model unavailable: \(reason) — requires Apple Silicon, macOS 26+, Apple Intelligence enabled", code: 3)
    }
}

guard case .available = model.availability else {
    fail("on-device model unavailable — run `axon doctor` for details", code: 3)
}

let input = FileHandle.standardInput.readDataToEndOfFile()
let req: Request
do { req = try JSONDecoder().decode(Request.self, from: input) } catch {
    fail("decode request: \(error.localizedDescription)", code: 2)
}

let sem = DispatchSemaphore(value: 0)
var replyText: String?
var failure: (String, Int32)?

Task {
    defer { sem.signal() }
    do {
        let session = LanguageModelSession(instructions: req.system ?? "")
        var options = GenerationOptions()
        options.maximumResponseTokens = req.max_tokens ?? 1024

        if let props = req.schema?.properties, !props.isEmpty {
            // Guided generation from the flat schema subset.
            var fields: [DynamicGenerationSchema.Property] = []
            for (name, p) in props.sorted(by: { $0.key < $1.key }) {
                let child: DynamicGenerationSchema = p.type == "array"
                    ? DynamicGenerationSchema(arrayOf: DynamicGenerationSchema(type: String.self))
                    : DynamicGenerationSchema(type: String.self)
                fields.append(.init(name: name, schema: child))
            }
            let root = DynamicGenerationSchema(name: "Output", properties: fields)
            let schema = try GenerationSchema(root: root, dependencies: [])
            let resp = try await session.respond(to: req.prompt, schema: schema, options: options)
            replyText = resp.content.jsonString
        } else {
            let resp = try await session.respond(to: req.prompt, options: options)
            replyText = resp.content
        }
    } catch let e as LanguageModelSession.GenerationError {
        switch e {
        case .exceededContextWindowSize:
            failure = ("input exceeds the on-device context window", 4)
        case .guardrailViolation:
            failure = ("request declined by on-device guardrails", 5)
        default:
            failure = ("generation error: \(e.localizedDescription)", 6)
        }
    } catch {
        failure = ("generation error: \(error.localizedDescription)", 6)
    }
}
sem.wait()

if let (msg, code) = failure { fail(msg, code: code) }
guard let text = replyText,
      let out = try? JSONEncoder().encode(Reply(text: text)) else {
    fail("encode response", code: 7)
}
FileHandle.standardOutput.write(out)
```

- [ ] **Step 4: Write the setup file**

`internal/agent/applefm_setup.go`:

```go
package agent

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// appleLMHelperSource is the Swift helper, embedded so `axon init` can
// (re)build it from an installed binary with no repo checkout (ADR-013
// pattern, reused by ADR-015).
//
//go:embed applefm_helper.swift
var appleLMHelperSource []byte

// appleLMCompile compiles src → dst; a var so tests can fake the toolchain.
var appleLMCompile = swiftLMCompile

// SwiftAvailable reports whether the Swift compiler is on PATH (Xcode CLT).
func SwiftAvailable() bool {
	_, err := exec.LookPath("swiftc")
	return err == nil
}

// EnsureAppleLMHelper writes + compiles the embedded Swift helper to
// helperPath, idempotently: a SHA-256 marker beside the binary records the
// source it was built from, so re-runs skip compilation unless the embedded
// source changed. Returns changed=true when a (re)compile happened.
func EnsureAppleLMHelper(ctx context.Context, helperPath string) (bool, error) {
	sum := sha256.Sum256(appleLMHelperSource)
	want := hex.EncodeToString(sum[:])
	marker := helperPath + ".src.sha256"

	if have, err := os.ReadFile(marker); err == nil && string(have) == want {
		if st, err := os.Stat(helperPath); err == nil && st.Mode()&0o111 != 0 {
			return false, nil // up to date
		}
	}

	if err := os.MkdirAll(filepath.Dir(helperPath), 0o755); err != nil {
		return false, fmt.Errorf("apple lm helper: create dir: %w", err)
	}
	srcPath := helperPath + ".swift"
	if err := os.WriteFile(srcPath, appleLMHelperSource, 0o644); err != nil {
		return false, fmt.Errorf("apple lm helper: write source: %w", err)
	}
	if err := appleLMCompile(ctx, srcPath, helperPath); err != nil {
		return false, fmt.Errorf("apple lm helper: compile: %w", err)
	}
	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("apple lm helper: write marker: %w", err)
	}
	return true, nil
}

// swiftLMCompile is the real toolchain invocation (requires Xcode CLT).
func swiftLMCompile(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "swiftc", "-O", src, "-o", dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("swiftc: %w: %s", err, out)
	}
	return nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/agent/ -run TestEnsureAppleLMHelper -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/applefm_helper.swift internal/agent/applefm_setup.go internal/agent/applefm_setup_test.go
git commit -m "feat(agent): Apple Foundation Models Swift helper + compile-at-init setup (FR-80)"
```

---

### Task 5: Agent — Apple Foundation Models Go adapter

**Files:**
- Create: `internal/agent/applefm.go`
- Test: `internal/agent/applefm_test.go`

**Interfaces:**
- Consumes: helper protocol from Task 4; `config.AppleFoundationModel`; `Request.OutputSchema`.
- Produces: `agent.NewAppleFM(helperPath string) *AppleFM` implementing `Agent` (`AuthMode() == "local"`), plus `(a *AppleFM) CheckAvailability(ctx context.Context) error` for configure/doctor.

- [ ] **Step 1: Write the failing test**

`internal/agent/applefm_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAppleFMRun(t *testing.T) {
	a := NewAppleFM("/fake/axon-apple-lm")
	a.goos = "darwin"
	var gotStdin []byte
	a.run = func(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
		gotStdin = stdin
		return []byte(`{"text":"{\"title\":\"T\"}"}`), nil, nil
	}

	resp, err := a.Run(context.Background(), Request{
		Model: "apple-foundation-v1", System: "sys", Prompt: "classify",
		OutputSchema: json.RawMessage(`{"properties":{"title":{"type":"string"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"title":"T"}` {
		t.Errorf("Text = %q", resp.Text)
	}
	var req map[string]any
	_ = json.Unmarshal(gotStdin, &req)
	if req["prompt"] != "classify" || req["system"] != "sys" {
		t.Errorf("helper request = %v", req)
	}
	if req["schema"] == nil {
		t.Error("schema not forwarded to helper")
	}
	if a.AuthMode() != "local" {
		t.Errorf("AuthMode = %q", a.AuthMode())
	}
}

func TestAppleFMRunNonDarwin(t *testing.T) {
	a := NewAppleFM("/fake/helper")
	a.goos = "linux"
	if _, err := a.Run(context.Background(), Request{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "macOS") {
		t.Fatalf("err = %v, want macOS-only error", err)
	}
}

func TestAppleFMRunHelperFailure(t *testing.T) {
	a := NewAppleFM("/fake/helper")
	a.goos = "darwin"
	a.run = func(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
		return nil, []byte("input exceeds the on-device context window"), errors.New("exit status 4")
	}
	if _, err := a.Run(context.Background(), Request{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "context window") {
		t.Fatalf("err = %v, want stderr surfaced", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestAppleFM -v`
Expected: FAIL — `undefined: NewAppleFM`.

- [ ] **Step 3: Implement**

`internal/agent/applefm.go` (subprocess pattern mirrors `internal/embeddings/apple.go`, including the capped stderr/stdout failure text):

```go
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
// execClaude / execAppleHelper).
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/applefm.go internal/agent/applefm_test.go
git commit -m "feat(agent): Apple Foundation Models adapter (FR-80)"
```

---

### Task 6: Tokens — router injection, provider resolution, budget exemption

**Files:**
- Modify: `internal/tokens/manager.go`
- Test: `internal/tokens/local_test.go` (new)

**Interfaces:**
- Consumes: `agent.Router`, `config.ParseModelRef`, `config.ProviderClaude`.
- Produces: `tokens.NewWithRouter(db *sql.DB, router agent.Router, searcher *search.Searcher, bus *events.Bus, cfg Config) Manager`; existing `tokens.New(db, ag, …)` becomes a wrapper (`Router{Claude: ag}`) so **no existing call site changes**; `Authorization` gains `Provider string`.

- [ ] **Step 1: Write the failing test**

`internal/tokens/local_test.go`. Reuse the package's existing test scaffolding for an in-memory DB (see how `manager_test.go` opens the test DB — use the same helper; the snippet below assumes a `testDB(t)` helper exists there; if it has a different name, use that one):

```go
package tokens

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func localTestConfig() Config {
	return Config{
		Profile:  "test",
		AuthMode: "subscription",
		Models: config.ModelsConfig{
			Classify:  "ollama:qwen3:8b",
			Routine:   "claude-sonnet-4-6",
			Synthesis: "claude-opus-4-8",
		},
		Limits: config.LimitsConfig{DailyTokens: 100, WeeklyTokens: 100},
	}
}

func TestLocalCallBudgetExempt(t *testing.T) {
	d := testDB(t)
	fake := agent.NewFake()
	fake.Reply = "02-Areas"
	mgr := NewWithRouter(d, agent.Router{Claude: agent.NewFake(), Ollama: fake}, nil, nil, localTestConfig())

	// A prompt far larger than the 100-token day window: a Claude call would
	// be denied/deferred; a local call must proceed (FR-78).
	big := strings.Repeat("word ", 2000)
	res, err := mgr.Run(context.Background(), AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: big}},
	})
	if err != nil {
		t.Fatalf("local call: %v", err)
	}
	if res.Auth.Decision != DecisionProceed {
		t.Fatalf("decision = %s, want proceed", res.Auth.Decision)
	}
	if res.Auth.Provider != config.ProviderOllama {
		t.Fatalf("provider = %s, want ollama", res.Auth.Provider)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("ollama adapter calls = %d, want 1", fake.CallCount())
	}

	// Ledgered with the provider-identifying model string…
	rows, err := db.ListLedger(context.Background(), d, "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "ollama:qwen3:8b" {
		t.Fatalf("ledger rows = %+v, want one row with model ollama:qwen3:8b", rows)
	}
	// …but the budget windows untouched.
	st, err := mgr.Status(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if st.Day.Used != 0 || st.Week.Used != 0 {
		t.Fatalf("windows used = %d/%d, want 0/0 (budget-exempt)", st.Day.Used, st.Week.Used)
	}
}

func TestDowngradeSkipsLocalTiers(t *testing.T) {
	d := testDB(t)
	cfg := localTestConfig() // classify is ollama: → not a downgrade target
	claude := agent.NewFake()
	mgr := NewWithRouter(d, agent.Router{Claude: claude, Ollama: agent.NewFake()}, nil, nil, cfg)

	// Exhaust the day window so a routine (claude) call must downgrade.
	seedBudget(t, d, "test", 100)

	auth, err := mgr.Authorize(context.Background(), AgentCall{
		Operation: "test.routine", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// classify is local, so no Claude tier below routine exists → defer/deny,
	// never a downgrade into a local tier.
	if auth.Decision == DecisionDowngrade {
		t.Fatalf("downgraded into a local tier: %+v", auth)
	}
}
```

Also add (same file) the small seeding helper if the package doesn't already have one:

```go
func seedBudget(t *testing.T, d *sql.DB, profile string, used int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC()
	if err := db.AddBudgetUsage(ctx, tx, profile, "day", dayPeriod(ts), used, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.AddBudgetUsage(ctx, tx, profile, "week", weekPeriod(ts), used, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
```

(Add `"database/sql"` and `"time"` imports. If `db.ListLedger` has a different name/signature, use the actual read function from `internal/db` — check `internal/db/` for the ledger list/read repository used by the dashboard's `/api/usage`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tokens/ -run 'TestLocalCallBudgetExempt|TestDowngradeSkipsLocalTiers' -v`
Expected: FAIL — `undefined: NewWithRouter`.

- [ ] **Step 3: Implement in `internal/tokens/manager.go`**

3a. `Authorization` (line 54) gains a field:

```go
type Authorization struct {
	Decision Decision
	Model    string // resolved concrete model to use
	Provider string // "claude" | "ollama" | "apple" (ADR-015)
	EstInput int
	Reason   string
}
```

3b. `manager` struct: replace `agent agent.Agent` with `router agent.Router`; `New` becomes a wrapper and `NewWithRouter` the real constructor:

```go
// New builds a Manager around a single Claude adapter (the pre-ADR-015
// shape). ag may be nil for read-only callers.
func New(database *sql.DB, ag agent.Agent, searcher *search.Searcher, bus *events.Bus, cfg Config) Manager {
	return NewWithRouter(database, agent.Router{Claude: ag}, searcher, bus, cfg)
}

// NewWithRouter builds a Manager that dispatches per-tier to the router's
// adapters (ADR-015). Run requires the referenced adapters to be non-nil.
func NewWithRouter(database *sql.DB, router agent.Router, searcher *search.Searcher, bus *events.Bus, cfg Config) Manager {
	// …existing New body, storing router instead of agent…
}
```

3c. Provider resolution — add beside `resolveModel` (line 188):

```go
// resolveRef resolves a model key to (provider, concrete model) — ADR-015.
func (m *manager) resolveRef(key string) config.ModelRef {
	return config.ParseModelRef(m.resolveModel(key))
}

// downgradeClaudeKey returns the next cheaper tier whose provider is Claude,
// or "". Local tiers are skipped: they are budget-exempt already and never
// the target of a budget downgrade (FR-78).
func (m *manager) downgradeClaudeKey(key string) string {
	for k := downgradeKey(key); k != ""; k = downgradeKey(k) {
		if m.resolveRef(k).Provider == config.ProviderClaude {
			return k
		}
	}
	return ""
}
```

3d. `Authorize` (line 251): resolve the ref up front, short-circuit local, and use `downgradeClaudeKey`:

```go
func (m *manager) Authorize(ctx context.Context, call AgentCall) (Authorization, error) {
	ref := m.resolveRef(call.ModelKey)
	est := m.estimateInput(ctx, ref, call)
	auth := Authorization{Decision: DecisionProceed, Model: ref.Model, Provider: ref.Provider, EstInput: est}

	// Local providers are budget-exempt: no window checks, no defer/deny/
	// downgrade (FR-78). Failure handling is the Run fallback ladder's job.
	if ref.Provider != config.ProviderClaude {
		return auth, nil
	}
	// …rest of the existing body unchanged, except the two
	// `downgradeKey(call.ModelKey)` calls become
	// `m.downgradeClaudeKey(call.ModelKey)` and the two
	// `auth.Model = m.resolveModel(dk)` become
	// `auth.Model = m.resolveRef(dk).Model` (a Claude tier, so Model==string)…
}
```

3e. `estimateInput` (line 238) now takes the ref and resolves the adapter for exact counting:

```go
func (m *manager) estimateInput(ctx context.Context, ref config.ModelRef, call AgentCall) int {
	if m.cfg.AuthMode == "api_key" && ref.Provider == config.ProviderClaude {
		if ag, err := m.router.Resolve(ref.Provider); err == nil {
			if c, ok := ag.(tokenCounter); ok {
				if n, err := c.CountTokens(ctx, ref.Model, call.System, joinMessages(call.Messages)); err == nil {
					return n
				}
			}
		}
	}
	return m.estimateCall(call)
}
```

3f. `Run` (line 339): replace the `m.agent == nil` check and the direct call with router resolution (the local execution path itself is Task 7 — for now every provider goes through the same path):

```go
	ag, err := m.router.Resolve(auth.Provider)
	if err != nil {
		return res, fmt.Errorf("token manager: %w", err)
	}
	resp, err := ag.Run(ctx, agent.Request{
		Operation: call.Operation,
		Model:     auth.Model,
		System:    m.applyRedaction(call.System),
		Prompt:    m.applyRedaction(joinMessages(call.Messages)),
	})
```

3g. `record` (line 413): provider-identifying ledger model + budget-usage skip. Add the auth-aware model string and guard the two `AddBudgetUsage` calls:

```go
	// Ledger rows name the provider so local traffic is distinguishable
	// (FR-77): "ollama:<model>" / "apple-foundation-v1" / bare Claude string.
	ledgerModel := res.Model
	if auth.Provider == config.ProviderOllama {
		ledgerModel = config.ProviderOllama + ":" + res.Model
	}
```

Use `Model: ledgerModel` in the `db.InsertLedger` call, then:

```go
	// Local calls are budget-exempt (FR-78): ledgered above, but they never
	// consume the day/week windows that protect the Claude quota.
	if auth.Provider == config.ProviderClaude {
		if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "day", dayPeriod(ts), total, costVal); err != nil {
			return 0, err
		}
		if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "week", weekPeriod(ts), total, costVal); err != nil {
			return 0, err
		}
	}
```

- [ ] **Step 4: Run the full package + build**

Run: `go build ./... && go test ./internal/tokens/ ./internal/agent/ ./internal/config/ -v`
Expected: PASS — including every pre-existing tokens test (the `New` wrapper keeps them compiling unchanged).

- [ ] **Step 5: Run the whole suite**

Run: `go test ./...`
Expected: PASS (all other `tokens.New` call sites — cmd, mcp, hooks, dashboard, automations, ingestion tests — are untouched).

- [ ] **Step 6: Commit**

```bash
git add internal/tokens/
git commit -m "feat(tokens): route per-tier providers through the chokepoint, budget-exempt local calls (FR-77, FR-78)"
```

---

### Task 7: Tokens — `ValidateOutput` + local fallback ladder

**Files:**
- Modify: `internal/tokens/manager.go`
- Test: `internal/tokens/local_test.go` (extend)

**Interfaces:**
- Consumes: Task 6's provider plumbing; `ModelsConfig.Fallback()`.
- Produces: `AgentCall` gains `ValidateOutput func(string) error` and `OutputSchema json.RawMessage`; local failures retry once then fall forward to Claude or fail per `models.local_fallback`; Apple input cap `appleInputCapTokens = 3500`.

- [ ] **Step 1: Write the failing tests** (append to `internal/tokens/local_test.go`)

```go
func TestLocalFallForwardToClaude(t *testing.T) {
	d := testDB(t)
	broken := agent.NewFake()
	broken.Err = errors.New("connection refused")
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	mgr := NewWithRouter(d, agent.Router{Claude: claude, Ollama: broken}, nil, nil, localTestConfig())

	res, err := mgr.Run(context.Background(), AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("fall-forward should succeed: %v", err)
	}
	if res.Text != "from-claude" {
		t.Fatalf("Text = %q, want from-claude", res.Text)
	}
	if broken.CallCount() != 2 { // one attempt + one retry (FR-79)
		t.Fatalf("local attempts = %d, want 2", broken.CallCount())
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude calls = %d, want 1", claude.CallCount())
	}
	// The Claude fallback consumed budget as a normal call.
	st, _ := mgr.Status(context.Background(), "test")
	if st.Day.Used == 0 {
		t.Fatal("claude fallback should consume the day window")
	}
}

func TestLocalFailModeSurfacesError(t *testing.T) {
	d := testDB(t)
	cfg := localTestConfig()
	cfg.Models.LocalFallback = "fail"
	broken := agent.NewFake()
	broken.Err = errors.New("connection refused")
	claude := agent.NewFake()
	mgr := NewWithRouter(d, agent.Router{Claude: claude, Ollama: broken}, nil, nil, cfg)

	_, err := mgr.Run(context.Background(), AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("fail mode must surface the error")
	}
	if claude.CallCount() != 0 {
		t.Fatalf("claude calls = %d, want 0 in fail mode", claude.CallCount())
	}
	// A :failed ledger row exists (standard failure accounting).
	rows, _ := db.ListLedger(context.Background(), d, "test", 10)
	if len(rows) == 0 || !strings.HasSuffix(rows[0].Operation, ":failed") {
		t.Fatalf("rows = %+v, want a :failed row", rows)
	}
}

func TestLocalValidateOutputRetriesThenFallsForward(t *testing.T) {
	d := testDB(t)
	junk := agent.NewFake()
	junk.Reply = "not json"
	claude := agent.NewFake()
	claude.Reply = `{"ok":true}`
	mgr := NewWithRouter(d, agent.Router{Claude: claude, Ollama: junk}, nil, nil, localTestConfig())

	res, err := mgr.Run(context.Background(), AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
		ValidateOutput: func(s string) error {
			if !strings.HasPrefix(strings.TrimSpace(s), "{") {
				return errors.New("not a JSON object")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if junk.CallCount() != 2 || claude.CallCount() != 1 {
		t.Fatalf("calls local=%d claude=%d, want 2/1", junk.CallCount(), claude.CallCount())
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestAppleInputCapShortCircuits(t *testing.T) {
	d := testDB(t)
	cfg := localTestConfig()
	cfg.Models.Classify = "apple"
	// Generous windows: this test exercises the apple input cap, and the
	// Claude fallback for the oversized prompt must not itself be deferred.
	cfg.Limits = config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 1_000_000}
	apple := agent.NewFake()
	claude := agent.NewFake()
	claude.Reply = "ok"
	mgr := NewWithRouter(d, agent.Router{Claude: claude, Apple: apple}, nil, nil, cfg)

	big := strings.Repeat("word ", 8000) // ≫ appleInputCapTokens at ~4 chars/token
	_, err := mgr.Run(context.Background(), AgentCall{
		Operation: "test.apple", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: big}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if apple.CallCount() != 0 {
		t.Fatalf("apple calls = %d, want 0 (input cap short-circuit)", apple.CallCount())
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude calls = %d, want 1 (fallback)", claude.CallCount())
	}
}
```

(Add `"errors"` to imports.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tokens/ -run 'TestLocal|TestApple' -v`
Expected: FAIL — `unknown field ValidateOutput`, wrong call counts.

- [ ] **Step 3: Implement in `internal/tokens/manager.go`**

3a. Extend `AgentCall` (line 43):

```go
	// ValidateOutput, when set, is applied to every response text. For local
	// providers a failure triggers the retry/fallback ladder (FR-79); for
	// Claude it fails the call (ledgered conservatively under the :failed
	// operation label, like adapter errors).
	ValidateOutput func(string) error
	// OutputSchema optionally carries a JSON Schema for providers with
	// structured output (Apple guided generation; Ollama JSON mode).
	OutputSchema json.RawMessage
```

(Add `"encoding/json"` import.)

3b. Add constants + helpers:

```go
// appleInputCapTokens is a conservative pre-flight cap for the Apple
// on-device model, whose small context window is shared between prompt and
// response. Oversized inputs skip straight to the fallback ladder.
const appleInputCapTokens = 3500

// buildRequest assembles the (redacted) adapter request for a call.
func (m *manager) buildRequest(call AgentCall, auth Authorization) agent.Request {
	return agent.Request{
		Operation:    call.Operation,
		Model:        auth.Model,
		System:       m.applyRedaction(call.System),
		Prompt:       m.applyRedaction(joinMessages(call.Messages)),
		JSONOutput:   call.OutputSchema != nil,
		OutputSchema: call.OutputSchema,
	}
}

// fallbackClaudeKey returns the first tier at or above key whose provider is
// Claude. Synthesis is always Claude (config-validated), so this terminates.
func (m *manager) fallbackClaudeKey(key string) string {
	order := []string{"classify", "routine", "synthesis"}
	start := 0
	switch key {
	case "routine":
		start = 1
	case "synthesis":
		start = 2
	}
	for _, k := range order[start:] {
		if m.resolveRef(k).Provider == config.ProviderClaude {
			return k
		}
	}
	return "synthesis"
}

// recordFailure writes the conservative :failed ledger row for a call that
// consumed (or may have consumed) work without a usable result.
func (m *manager) recordFailure(ctx context.Context, call AgentCall, auth Authorization, res AgentResult) {
	failed := call
	failed.Operation = call.Operation + ":failed"
	failedRes := res
	failedRes.Usage.InputTokens = auth.EstInput
	if _, lerr := m.record(ctx, failed, auth, failedRes); lerr != nil {
		m.emit(events.LevelError, "token.error", call.Operation, auth,
			map[string]any{"error": "ledger write after failed call: " + lerr.Error()})
	}
}
```

3c. Restructure `Run`: after the deny/defer/downgrade switch, branch on provider. The Claude path is the existing body refactored to use `buildRequest` and `recordFailure`, plus validation; the local path is new:

```go
	if auth.Provider != config.ProviderClaude {
		return m.runLocal(ctx, call, auth)
	}
```

Claude path additions — after the usage-fallback block (line 395), before `record`:

```go
	if call.ValidateOutput != nil {
		if verr := call.ValidateOutput(res.Text); verr != nil {
			m.recordFailure(ctx, call, auth, res)
			m.emit(events.LevelError, "token.error", call.Operation, auth, map[string]any{"error": "output validation: " + verr.Error()})
			return res, fmt.Errorf("agent run %q: output validation: %w", call.Operation, verr)
		}
	}
```

3d. The local runner:

```go
// runLocal executes a call on a local provider: pre-flight input cap (apple),
// one attempt + one retry, output validation, then the configured fallback —
// fall forward to Claude through the normal budget path, or fail visibly
// (FR-79). Every outcome is ledgered (FR-78).
func (m *manager) runLocal(ctx context.Context, call AgentCall, auth Authorization) (AgentResult, error) {
	res := AgentResult{Auth: auth, Model: auth.Model}

	fallForward := func(cause error) (AgentResult, error) {
		m.recordFailure(ctx, call, auth, res)
		if m.cfg.Models.Fallback() == "fail" {
			m.emit(events.LevelError, "token.error", call.Operation, auth, map[string]any{"error": cause.Error()})
			return res, fmt.Errorf("local model %q failed (local_fallback: fail): %w", auth.Model, cause)
		}
		fb := call
		fb.ModelKey = m.fallbackClaudeKey(call.ModelKey)
		m.emit(events.LevelWarn, "token.local_fallback", call.Operation, auth,
			map[string]any{"error": cause.Error(), "fallback_tier": fb.ModelKey})
		return m.Run(ctx, fb) // resolves to Claude: normal budget-checked path
	}

	if auth.Provider == config.ProviderApple && auth.EstInput > appleInputCapTokens {
		return fallForward(fmt.Errorf("estimated input %d exceeds the on-device context cap %d", auth.EstInput, appleInputCapTokens))
	}
	ag, err := m.router.Resolve(auth.Provider)
	if err != nil {
		return fallForward(err)
	}

	req := m.buildRequest(call, auth)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ { // one attempt + one retry
		resp, err := ag.Run(ctx, req)
		if err != nil {
			lastErr = err
			continue
		}
		if call.ValidateOutput != nil {
			if verr := call.ValidateOutput(resp.Text); verr != nil {
				lastErr = fmt.Errorf("output validation: %w", verr)
				continue
			}
		}
		res.Text = resp.Text
		res.Usage = resp.Usage
		if resp.Model != "" {
			res.Model = resp.Model
		}
		if res.Usage.InputTokens+res.Usage.OutputTokens == 0 {
			res.Usage.InputTokens = auth.EstInput
			res.Usage.OutputTokens = HeuristicEstimator{}.Estimate(res.Text)
		}
		ledgerID, err := m.record(ctx, call, auth, res)
		if err != nil {
			return res, err
		}
		res.LedgerID = ledgerID
		return res, nil
	}
	return fallForward(lastErr)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tokens/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tokens/
git commit -m "feat(tokens): output validation + local retry/fallback ladder (FR-79)"
```

---

### Task 8: Wiring — `cmd/axon/deps.go` builds the router

**Files:**
- Modify: `cmd/axon/deps.go` (rename `agentAdapter` → `claudeAdapter`, add `agentRouter`, switch `buildServices` line 121 to `tokens.NewWithRouter`)

**Interfaces:**
- Consumes: `agent.Router`, `agent.NewOllama`, `agent.NewAppleFM`, `config.ParseModelRef`, `config.DefaultAppleLMHelperPath`, `tokens.NewWithRouter`.
- Produces: `(d *profileDeps) agentRouter() agent.Router` — the single composition point.

- [ ] **Step 1: Implement**

In `cmd/axon/deps.go`, rename the existing `agentAdapter` method to `claudeAdapter` (body unchanged), then add:

```go
// agentRouter composes the per-provider adapters for this profile (ADR-015).
// Claude is always present; local adapters are constructed only when a
// models.* tier references them. Construction is lazy (no network/subprocess),
// matching embeddingsProvider.
func (d *profileDeps) agentRouter() agent.Router {
	r := agent.Router{Claude: d.claudeAdapter()}
	models := d.profile.Models
	for _, tier := range []string{models.Classify, models.Routine, models.Synthesis} {
		switch config.ParseModelRef(tier).Provider {
		case config.ProviderOllama:
			if r.Ollama == nil {
				r.Ollama = agent.NewOllama(models.OllamaHost)
			}
		case config.ProviderApple:
			if r.Apple == nil {
				helper := models.AppleHelper
				if helper == "" {
					helper = config.DefaultAppleLMHelperPath()
				}
				r.Apple = agent.NewAppleFM(helper)
			}
		}
	}
	return r
}
```

And in `buildServices` (line 121):

```go
	mgr := tokens.NewWithRouter(d.db, d.agentRouter(), searcher, bus, managerConfig(d.name, d.profile, d.cfg))
```

- [ ] **Step 2: Build and run the full suite**

Run: `go build ./... && go test ./...`
Expected: PASS (the read-only `tokens.New(deps.db, nil, …)` call sites in `export_cmd.go`/`hook_cmd.go`/`status_cmd.go` are untouched and still valid).

- [ ] **Step 3: Commit**

```bash
git add cmd/axon/deps.go
git commit -m "feat(cmd): compose the provider router from config (ADR-015)"
```

---

### Task 9: Caller adoption — enrichment + inbox-triage validators

**Files:**
- Modify: `internal/ingestion/claude_enrich.go` (the `tokens.AgentCall` built around line 42)
- Modify: `internal/automations/model.go` (inbox-triage `AgentCall`, ~line 177)
- Test: extend `internal/ingestion/claude_enrich_test.go` and `internal/automations/standard_test.go`

**Interfaces:**
- Consumes: `AgentCall.ValidateOutput`, `AgentCall.OutputSchema`; existing `parseEnrichment(text string, candidates []string)` (claude_enrich.go:107).
- Produces: no new API — the two classify/routine call sites become schema-validated so local models are held to strict output (spec "Caller adoption").

- [ ] **Step 1: Enrichment.** In `ClaudeEnricher.Enrich`, where the `tokens.AgentCall` is constructed, add:

```go
		OutputSchema: json.RawMessage(`{"properties":{
			"title":{"type":"string"},"summary":{"type":"string"},
			"tags":{"type":"array"},"links":{"type":"array"}}}`),
		ValidateOutput: func(text string) error {
			_, err := parseEnrichment(text, in.Candidates)
			return err
		},
```

Match the actual field name for candidates in `EnrichInput` (see `parseEnrichment`'s existing call right after `Run` in the same function — reuse exactly the same arguments). Keep the existing post-`Run` parse; the validator just moves the failure into the chokepoint where the fallback ladder can see it.

- [ ] **Step 2: Inbox-triage.** In the triage `AgentCall` (model.go ~line 177), add:

```go
			ValidateOutput: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("empty classification line")
				}
				return nil
			},
```

(Add `"errors"` import if missing.)

- [ ] **Step 3: Tests.** Extend the existing table-driven tests: in `claude_enrich_test.go`, add a case where the fake agent returns non-JSON and assert `Enrich` returns an error containing "output validation" (fake is a Claude-provider router, so validation fails the call). In `standard_test.go`'s triage test, set `fake.Reply = ""` for one case and assert the run errors.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ingestion/ ./internal/automations/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ingestion/ internal/automations/
git commit -m "feat: schema-validate enrichment and triage outputs at the chokepoint"
```

---

### Task 10: Ops — `axon configure models` provider flow + convergence

**Files:**
- Modify: `cmd/axon/configure_cmd.go` (`newConfigureModelsCmd` ~line 171; the `models` menu branch ~line 92)
- Create: `cmd/axon/configure_models.go` (the convergence helper, mirroring `configure_embeddings.go`'s `switchEmbeddings`)
- Test: `cmd/axon/configure_models_test.go`

**Interfaces:**
- Consumes: `config.ParseModelRef`, `agent.NewOllama(...).Healthcheck`, `agent.EnsureAppleLMHelper`, `agent.NewAppleFM(...).CheckAvailability`, `agent.SwiftAvailable`, `tui.Select/Input/Spin`, `setConfigValue`.
- Produces: `convergeModelTier(ctx context.Context, out io.Writer, m config.ModelsConfig, tier string) error` used by both the subcommand and the menu.

- [ ] **Step 1: Implement the convergence helper** in `cmd/axon/configure_models.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tui"
)

// convergeModelTier verifies the just-configured tier can actually serve:
// ollama → one-token chat round trip; apple → compile the helper (if needed)
// and probe on-device availability; claude → nothing to converge (the plan
// tier governs availability, docs/07). Mirrors switchEmbeddings' convergence.
func convergeModelTier(ctx context.Context, out io.Writer, m config.ModelsConfig, tierValue string) error {
	ref := config.ParseModelRef(tierValue)
	switch ref.Provider {
	case config.ProviderOllama:
		return tui.Spin(out, "probing ollama model "+ref.Model+"…", func() (string, error) {
			if err := agent.NewOllama(m.OllamaHost).Healthcheck(ctx, ref.Model); err != nil {
				return "", fmt.Errorf("%w — is Ollama running and the model pulled? (ollama pull %s)", err, ref.Model)
			}
			return "ollama model " + ref.Model + " responds", nil
		})
	case config.ProviderApple:
		if runtime.GOOS != "darwin" {
			fmt.Fprintln(out, "warning: apple tier configured on a non-mac — calls on this machine will use the fallback")
			return nil
		}
		if !agent.SwiftAvailable() {
			return fmt.Errorf("swiftc not found — install Xcode Command Line Tools (xcode-select --install)")
		}
		helper := m.AppleHelper
		if helper == "" {
			helper = config.DefaultAppleLMHelperPath()
		}
		return tui.Spin(out, "compiling + probing the Apple Foundation Models helper…", func() (string, error) {
			if _, err := agent.EnsureAppleLMHelper(ctx, helper); err != nil {
				return "", err
			}
			if err := agent.NewAppleFM(helper).CheckAvailability(ctx); err != nil {
				return "", err
			}
			return "on-device model available", nil
		})
	default:
		return nil
	}
}
```

- [ ] **Step 2: Wire the subcommand.** In `newConfigureModelsCmd`, after the existing `setConfigValue(..., "models."+class, model)` succeeds, reload the config (`config.Load(gf.configPath)` + `cfg.ResolveProfile(gf.profile)` — the reload also re-runs validation, so `apple` on a non-classify tier or `ollama:` with no model is rejected here with the Task 1 error text) and call `convergeModelTier(cmd.Context(), out, profile.Models, model)`.

- [ ] **Step 3: Wire the menu.** In the `models` branch of `configureMenu` (~line 92), before the existing model-string `tui.Input`, insert a provider select for the chosen tier:

```go
		providers := []tui.Option{
			{Label: "Claude", Value: "claude", Hint: "subscription/enterprise via claude -p (default)"},
			{Label: "Ollama (local)", Value: "ollama", Hint: "free + offline; needs a pulled model"},
		}
		if class == "classify" && runtime.GOOS == "darwin" {
			providers = append(providers, tui.Option{Label: "Apple on-device (local)", Value: "apple", Hint: "Foundation Models; zero install, classify only"})
		}
		provider, err := tui.Select(out, in, "Provider for "+class, providers)
```

Then: `apple` → value is just `"apple"` (skip the model input); `ollama` → `tui.Input(out, in, "Ollama model for "+class, "qwen3:8b", ...)` and store `"ollama:"+input`; `claude` → the existing input unchanged. Persist via the existing `setConfigValue` path, then `convergeModelTier`.

- [ ] **Step 4: Test.** `cmd/axon/configure_models_test.go` — follow the existing configure command tests' pattern (temp config file + `newRootCmd` execution). Table-test: `axon configure models classify ollama:qwen3:8b` against an `httptest` Ollama (inject via `models.ollama_host` in the temp config) persists the value and succeeds; `axon configure models synthesis ollama:x` fails with the validation error; `axon configure models routine apple` fails.

- [ ] **Step 5: Run tests + commit**

Run: `go test ./cmd/axon/ -run TestConfigureModels -v && go test ./...`
Expected: PASS.

```bash
git add cmd/axon/
git commit -m "feat(cmd): provider-aware configure models flow with convergence probes"
```

---

### Task 11: Ops — doctor checks + init probe

**Files:**
- Modify: `internal/core/doctor.go` (add a check beside `embeddingsCheck`, line 122)
- Modify: `internal/core/init.go` (add a probe beside `probeAppleEmbedding`, line 349)
- Test: extend `internal/core/doctor_test.go` and the init step tests

**Interfaces:**
- Consumes: `config.ParseModelRef`, `agent.NewOllama(...).Healthcheck`, `agent.NewAppleFM(...).CheckAvailability`, `agent.EnsureAppleLMHelper`, `agent.SwiftAvailable`.
- Produces: doctor reports per configured local tier; `axon init` compiles the LM helper when a tier is `apple` (warnings only, never blocking — same convention as the embeddings probe).

- [ ] **Step 1: Doctor.** Add `localModelsCheck` following `embeddingsCheck`'s exact structure and result types (read that function first and mirror it):
  - For each tier in `{classify: m.Classify, routine: m.Routine}` with a local provider:
    - `ollama`: run `agent.NewOllama(m.OllamaHost).Healthcheck(ctx, ref.Model)` with a short timeout → OK, or Warn `"ollama model <model> unreachable: <err> — automations on this tier will use models.local_fallback (<value>)"`.
    - `apple` on darwin: stat the helper (default or `m.AppleHelper`) + executable bit → Warn `"helper missing — run axon init"` if absent; else run `CheckAvailability` → Warn with the helper's stderr (it names the Apple Intelligence remediation).
    - `apple` on non-darwin: Warn `"models tier configured as apple but this machine is not a mac — calls will use the fallback"`.
  - Also surface the effective `models.local_fallback` value in the check detail.
  - Register the check wherever `embeddingsCheck` is registered in the doctor run list.

- [ ] **Step 2: Init.** Add `probeAppleLM` next to `probeAppleEmbedding` (init.go:349), reusing the same injectable-deps pattern (`goos`, `swiftOK`, `ensure`, `probe` function fields) so tests fake the toolchain:
  - Runs only when `ParseModelRef(profile.Models.Classify).Provider == config.ProviderApple`.
  - darwin check → `SwiftAvailable` → `EnsureAppleLMHelper(ctx, helperPath)` → `NewAppleFM(helperPath).CheckAvailability(ctx)`.
  - All failures are step **warnings** (init never blocks): the runtime fallback ladder covers a missing local provider.

- [ ] **Step 3: Tests.** Mirror the existing `probeAppleEmbedding`/doctor tests with faked deps: helper-missing → warn; non-darwin + apple → warn; ollama tier with an `httptest` server → OK.

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/core/ -v && go test ./...`
Expected: PASS.

```bash
git add internal/core/
git commit -m "feat(core): doctor + init awareness of local model providers"
```

---

### Task 12: Docs, example config, FR status

**Files:**
- Modify: `docs/04-data-model-and-config.md` (models config reference, ~line 199)
- Modify: `docs/07-component-context-token-manager.md` (model selection §, lines 65-66; budget § line 52)
- Modify: `axon.config.example.yaml` (models block)
- Modify: `docs/03-requirements.md` (flip the FR-77…80 section banner from "planned" to "built")
- Modify: `CHANGELOG.md` (new entry)

- [ ] **Step 1: docs/04** — extend the `models:` example and prose:

```yaml
models:                      # per-tier model; prefix selects the provider (ADR-015)
  classify: ollama:qwen3:8b  # ollama:<model> → local Ollama /api/chat
  routine: claude-sonnet-4-6 # bare string → Claude via claude -p --model
  synthesis: claude-opus-4-8 # synthesis is always Claude (validated)
  # classify: apple          # Apple Foundation Models on-device (macOS 26+, classify only)
  ollama_host: http://localhost:11434  # local chat host (independent of embeddings.host)
  local_fallback: claude     # claude (default): retry locally once, then fall
                             # forward through the budget path; fail: surface the error
  # apple_helper: ~/.axon/bin/axon-apple-lm  # helper binary override
```

Document: local calls appear in `token_ledger` with provider-prefixed model strings (`ollama:qwen3:8b`, `apple-foundation-v1`), `cost_usd` null, and do **not** accrue to `budget_windows` (FR-78).

- [ ] **Step 2: docs/07** — in the model-selection section, replace "passed to `claude -p --model`" with the provider-routing description (prefix scheme, router in the chokepoint, local budget exemption, the fallback ladder, apple input cap). Note that `downgrade` only ever lands on Claude tiers.

- [ ] **Step 3: example config + CHANGELOG + FR banner.** Update `axon.config.example.yaml`'s models block (keep Claude defaults active; local options as comments). Flip docs/03's FR-77…80 section header from *"(planned — spec approved 2026-07-03, not yet built)"* to *"(built)"* and update the intro sentence. Add the CHANGELOG entry describing the feature and the two new config fields.

- [ ] **Step 4: Commit**

```bash
git add docs/ axon.config.example.yaml CHANGELOG.md
git commit -m "docs: local model routing config reference + FR-77..80 status"
```

---

### Task 13: Darwin-gated end-to-end test + final gates

**Files:**
- Create: `internal/agent/applefm_e2e_test.go`
- Verify: whole suite, lint, doctor

- [ ] **Step 1: Write the gated e2e** (mirror the gating style of the Apple embeddings e2e test — find it with `grep -rl "check-assets\|SwiftAvailable" internal/embeddings/*_test.go` and copy its skip conditions):

```go
package agent

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAppleFMEndToEnd compiles the real Swift helper and runs a tiny
// generation on the on-device model. Skipped unless darwin + swiftc + the
// model is actually available (CI runners and managed Macs often can't).
func TestAppleFMEndToEnd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("apple foundation models: darwin only")
	}
	if !SwiftAvailable() {
		t.Skip("swiftc not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	helper := filepath.Join(t.TempDir(), "axon-apple-lm")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := EnsureAppleLMHelper(ctx, helper); err != nil {
		t.Fatalf("compile helper: %v", err)
	}
	// Availability probe first: skip (not fail) on machines without Apple
	// Intelligence — mirrors the embeddings e2e's asset gating.
	if out, err := exec.CommandContext(ctx, helper, "--check-availability").CombinedOutput(); err != nil {
		t.Skipf("on-device model unavailable: %s", strings.TrimSpace(string(out)))
	}

	a := NewAppleFM(helper)
	resp, err := a.Run(ctx, Request{
		Model:  "apple-foundation-v1",
		System: "Reply with a single word.",
		Prompt: "Say ok.",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(resp.Text) == "" {
		t.Fatal("empty generation")
	}
}
```

- [ ] **Step 2: Final gates**

Run, in order:

```bash
go build ./... && go vet ./... && golangci-lint run && go test ./...
```

Expected: all green. Then on a machine with the daemon set up: `go run ./cmd/axon doctor` passes (local-model checks appear only when configured).

- [ ] **Step 3: Commit**

```bash
git add internal/agent/applefm_e2e_test.go
git commit -m "test(agent): darwin-gated Apple Foundation Models e2e"
```

---

## Verification (definition of done, per CLAUDE.md)

1. `go test ./...`, `go vet`, `golangci-lint run` — green.
2. FR trace: FR-77 (Tasks 1, 2, 3, 6, 8), FR-78 (Task 6), FR-79 (Task 7), FR-80 (Tasks 4, 5, 11, 13).
3. Cardinal rules: no new import of `agent` outside `tokens` (`grep -rn '"github.com/jandro-es/axon/internal/agent"' --include='*.go' internal/ | grep -v internal/tokens/ | grep -v internal/agent/` shows nothing new except `cmd/axon`, which composes); no vault writes anywhere in this feature.
4. Behavior check with all automations off (S8): configure `models.classify: ollama:<model>` with Ollama running, `axon ingest <url>` → enrichment ledger row shows `ollama:<model>`, `axon status` budgets unchanged, dashboard by-model chart shows the local model; stop Ollama → same ingest falls forward to Claude (event `token.local_fallback`), or errors visibly with `local_fallback: fail`.
5. On a mac: `axon configure models classify apple` compiles the helper and probes availability; `axon doctor` reports the tier.
