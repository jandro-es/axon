# Apple On-Device Embeddings Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `embeddings.provider: apple` — Apple's on-device NLContextualEmbedding as a config-selectable alternative to Ollama, chosen at install/init time, switchable via config + `axon reindex --embeddings`.

**Architecture:** A Swift helper program (source embedded in the axon binary, compiled once by `axon init` via swiftc) speaks JSON over stdin/stdout; a new `embeddings.Apple` provider shells out to it per `Embed` call, implementing the existing 4-method `Provider` interface. Init, doctor, hints, installer scripts, and docs become provider-aware. Generation stays 100% on Claude (cardinal rule 1 untouched).

**Tech Stack:** Go 1.26 (pure, no cgo), Swift/NaturalLanguage (`NLContextualEmbedding`, macOS 14+, **dim 512 — verified live on this machine**), bash installers.

**Spec:** `docs/superpowers/specs/2026-07-02-apple-embeddings-provider-design.md`

## Global Constraints

- Go binary stays pure Go / single static binary — the Swift bridge is a subprocess, never cgo.
- No path to Claude changes; embeddings are local and token-free.
- All init convergence failures for embeddings are `StepWarn`, never `StepFailed` (search degrades to lexical-only).
- Config is the source of truth: no hardcoded model strings/dims in logic — defaults live in `internal/config` constants and the example config.
- Idempotency: re-running init/installers must skip work already done and say so.
- `gofmt`/`go vet` clean; table-driven tests; errors wrapped with `%w`; `context.Context` through all I/O.
- Error messages from the helper subprocess include stderr AND stdout (capped), like the Claude adapter.
- Helper binary default location: `~/.axon/bin/axon-apple-embed` (AxonHome-relative, NOT per-profile — it's a machine-level tool like ollama).
- Apple model identifier default: `apple-nlcontextual-v1`; expected dim 512.

---

### Task 1: Config — provider enum, helper path override, defaults

**Files:**
- Modify: `internal/config/types.go:146-152` (EmbeddingsConfig)
- Modify: `internal/config/paths.go` (add DefaultAppleHelperPath)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `EmbeddingsConfig.Helper string` (yaml `helper`), `EmbeddingsConfig.Provider` validated `oneof=ollama apple`, `config.DefaultAppleHelperPath() string`, constants `config.AppleEmbeddingModel = "apple-nlcontextual-v1"` and `config.AppleEmbeddingDim = 512`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go` (match the file's existing table style; it tests via `config.Parse`):

```go
func TestEmbeddingsProviderValidation(t *testing.T) {
	base := `
version: 1
project_name: axon
active_profile: p
profiles:
  p:
    vault_path: "/tmp/v"
    data_dir: "/tmp/d"
    claude: {auth_mode: subscription}
    dashboard: {host: "127.0.0.1", port: 7777}
    embeddings: {provider: %s, model: m, dim: 8, batch_size: 4}
    models: {classify: c, routine: r, synthesis: s}
    limits: {daily_tokens: 1, weekly_tokens: 1}
`
	for _, tc := range []struct {
		provider string
		wantErr  bool
	}{
		{"ollama", false},
		{"apple", false},
		{"openai", true},
		{"", true},
	} {
		_, err := Parse([]byte(fmt.Sprintf(base, tc.provider)))
		if (err != nil) != tc.wantErr {
			t.Errorf("provider %q: err = %v, wantErr %v", tc.provider, err, tc.wantErr)
		}
	}
}

func TestEmbeddingsHelperField(t *testing.T) {
	raw := []byte(`
version: 1
project_name: axon
active_profile: p
profiles:
  p:
    vault_path: "/tmp/v"
    data_dir: "/tmp/d"
    claude: {auth_mode: subscription}
    dashboard: {host: "127.0.0.1", port: 7777}
    embeddings: {provider: apple, model: apple-nlcontextual-v1, dim: 512, batch_size: 16, helper: "/opt/helper"}
    models: {classify: c, routine: r, synthesis: s}
    limits: {daily_tokens: 1, weekly_tokens: 1}
`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Embeddings.Helper != "/opt/helper" {
		t.Errorf("helper = %q", p.Embeddings.Helper)
	}
}

func TestDefaultAppleHelperPath(t *testing.T) {
	got := DefaultAppleHelperPath()
	if !strings.HasSuffix(got, filepath.Join("bin", "axon-apple-embed")) {
		t.Errorf("unexpected helper path %q", got)
	}
}
```

Add `"fmt"`, `"path/filepath"`, `"strings"` to the test file's imports if absent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestEmbeddings|TestDefaultAppleHelperPath' -v`
Expected: FAIL — `apple` rejected only if validation is added? No: today `Provider` has just `required`, so `{"openai", true}` fails the assertion, `Helper` is an unknown field (goccy may ignore it → field test fails on empty string), and `DefaultAppleHelperPath` is undefined (compile error). Compile error counts as RED.

- [ ] **Step 3: Implement**

In `internal/config/types.go` change EmbeddingsConfig:

```go
// EmbeddingsConfig configures the local embedding provider. dim MUST match the
// model's output dimension; changing the model or provider forces a full
// re-index (`axon reindex --embeddings`).
type EmbeddingsConfig struct {
	Provider  string `yaml:"provider" validate:"required,oneof=ollama apple"`
	Host      string `yaml:"host"`      // ollama only
	Model     string `yaml:"model" validate:"required"`
	Dim       int    `yaml:"dim" validate:"required,min=1"`
	BatchSize int    `yaml:"batch_size" validate:"required,min=1"`
	// Helper overrides the apple provider's helper binary path.
	// Default: DefaultAppleHelperPath(). Ignored by other providers.
	Helper string `yaml:"helper,omitempty"`
}
```

In `internal/config/paths.go` add:

```go
// AppleEmbeddingModel and AppleEmbeddingDim are the defaults written to config
// when the apple embeddings provider is selected. The dim is asserted live by
// the init probe; NLContextualEmbedding v1 reports 512.
const (
	AppleEmbeddingModel = "apple-nlcontextual-v1"
	AppleEmbeddingDim   = 512
)

// DefaultAppleHelperPath is where `axon init` compiles the Apple embeddings
// helper: a machine-level tool (like the ollama binary), not per-profile.
func DefaultAppleHelperPath() string {
	return filepath.Join(AxonHome(), "bin", "axon-apple-embed")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/paths.go internal/config/config_test.go
git commit -m "Add apple embeddings provider to config schema (provider enum, helper path)"
```

---

### Task 2: Apple provider adapter (`embeddings.Apple`)

**Files:**
- Create: `internal/embeddings/apple.go`
- Test: `internal/embeddings/apple_test.go`

**Interfaces:**
- Consumes: `embeddings.Provider` interface (provider.go), `config.DefaultAppleHelperPath()`.
- Produces: `embeddings.NewApple(helperPath, model string, dim int) *Apple` implementing `Provider`. JSON protocol: request `{"texts":[...]}` → response `{"model":string,"dim":int,"vectors":[[float32]]}`. Injectable field `run func(ctx, bin string, stdin []byte) (stdout, stderr []byte, err error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/embeddings/apple_test.go`:

```go
package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func appleWithFakeRun(t *testing.T, fn func(stdin []byte) ([]byte, []byte, error)) *Apple {
	t.Helper()
	a := NewApple("/nonexistent/helper", "apple-nlcontextual-v1", 3)
	a.goos = "darwin" // tests exercise the protocol regardless of host OS
	a.run = func(ctx context.Context, bin string, stdin []byte) ([]byte, []byte, error) {
		return fn(stdin)
	}
	return a
}

func TestAppleEmbedRoundTrip(t *testing.T) {
	var gotReq appleRequest
	a := appleWithFakeRun(t, func(stdin []byte) ([]byte, []byte, error) {
		if err := json.Unmarshal(stdin, &gotReq); err != nil {
			t.Fatal(err)
		}
		return []byte(`{"model":"apple-nlcontextual-v1","dim":3,"vectors":[[1,2,3],[4,5,6]]}`), nil, nil
	})
	vecs, err := a.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotReq.Texts) != 2 || gotReq.Texts[0] != "a" {
		t.Errorf("request texts = %v", gotReq.Texts)
	}
	if len(vecs) != 2 || vecs[1][2] != 6 {
		t.Errorf("vectors = %v", vecs)
	}
}

func TestAppleEmbedEmptyInputNoSubprocess(t *testing.T) {
	called := false
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		called = true
		return nil, nil, nil
	})
	vecs, err := a.Embed(context.Background(), nil)
	if err != nil || vecs != nil || called {
		t.Errorf("empty input: vecs=%v err=%v called=%v", vecs, err, called)
	}
}

func TestAppleEmbedCountMismatch(t *testing.T) {
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		return []byte(`{"model":"m","dim":3,"vectors":[[1,2,3]]}`), nil, nil
	})
	if _, err := a.Embed(context.Background(), []string{"a", "b"}); err == nil ||
		!strings.Contains(err.Error(), "1 vectors for 2 inputs") {
		t.Errorf("want count-mismatch error, got %v", err)
	}
}

func TestAppleEmbedDimMismatch(t *testing.T) {
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		return []byte(`{"model":"m","dim":2,"vectors":[[1,2]]}`), nil, nil
	})
	if _, err := a.Embed(context.Background(), []string{"a"}); err == nil ||
		!strings.Contains(err.Error(), "reindex") {
		t.Errorf("want dim-mismatch error mentioning reindex, got %v", err)
	}
}

func TestAppleEmbedFailureIncludesStdoutAndStderr(t *testing.T) {
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		return []byte("assets not downloaded"), []byte("some warning"), errors.New("exit status 3")
	})
	_, err := a.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"exit status 3", "assets not downloaded", "some warning"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got %q", want, err.Error())
		}
	}
}

func TestAppleNonDarwinGuard(t *testing.T) {
	a := NewApple("/x", "m", 3)
	a.goos = "linux"
	if _, err := a.Embed(context.Background(), []string{"a"}); err == nil ||
		!strings.Contains(err.Error(), "macOS") {
		t.Errorf("want macOS-only error, got %v", err)
	}
	if err := a.Healthcheck(context.Background()); err == nil {
		t.Error("healthcheck should fail on non-darwin")
	}
}

func TestAppleModelAndDim(t *testing.T) {
	a := NewApple("/x", "apple-nlcontextual-v1", 512)
	if a.Model() != "apple-nlcontextual-v1" || a.Dim() != 512 {
		t.Errorf("model=%q dim=%d", a.Model(), a.Dim())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/embeddings/ -run TestApple -v`
Expected: compile error — `Apple`, `NewApple`, `appleRequest` undefined. RED.

- [ ] **Step 3: Implement `internal/embeddings/apple.go`**

```go
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
// JSON over stdin/stdout keeps the Go binary pure Go — no cgo (spec:
// docs/superpowers/specs/2026-07-02-apple-embeddings-provider-design.md).
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/embeddings/ -v` — all PASS (including existing Ollama/fake tests).

- [ ] **Step 5: Commit**

```bash
git add internal/embeddings/apple.go internal/embeddings/apple_test.go
git commit -m "Add Apple on-device embeddings provider (Swift helper subprocess adapter)"
```

---

### Task 3: Embedded Swift helper source + EnsureAppleHelper (compile-at-init)

**Files:**
- Create: `internal/embeddings/apple_helper.swift`
- Create: `internal/embeddings/apple_setup.go`
- Test: `internal/embeddings/apple_setup_test.go`
- Test: `internal/embeddings/apple_integration_test.go`

**Interfaces:**
- Produces: `embeddings.EnsureAppleHelper(ctx context.Context, helperPath string) (changed bool, err error)` — writes+compiles the embedded Swift source idempotently (SHA-256 marker file `<helperPath>.src.sha256`). Package-level `var appleCompile = swiftCompile` seam for tests. `embeddings.SwiftAvailable() bool`.

- [ ] **Step 1: Create the Swift source** (API verified by live compile+run on macOS 26: dim=512, mean-pooling works)

`internal/embeddings/apple_helper.swift`:

```swift
// axon-apple-embed — AXON's Apple on-device embeddings helper.
// Compiled by `axon init` from source embedded in the axon binary.
// Protocol: stdin {"texts":[...]} → stdout {"model":..., "dim":..., "vectors":[[...]]}.
// Errors: message on stderr + non-zero exit.
import Foundation
import NaturalLanguage

struct Request: Decodable { let texts: [String] }
struct Response: Encodable { let model: String; let dim: Int; let vectors: [[Float]] }

func fail(_ msg: String, code: Int32) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

let modelID = "apple-nlcontextual-v1"

guard let embedding = NLContextualEmbedding(language: .english) else {
    fail("no on-device contextual embedding model (requires macOS 14+)", code: 2)
}
if !embedding.hasAvailableAssets {
    let sem = DispatchSemaphore(value: 0)
    var assetErr: Error?
    embedding.requestAssets { _, error in assetErr = error; sem.signal() }
    sem.wait()
    if let e = assetErr { fail("embedding assets unavailable: \(e.localizedDescription)", code: 3) }
}
do { try embedding.load() } catch { fail("load embedding model: \(error.localizedDescription)", code: 4) }

let input = FileHandle.standardInput.readDataToEndOfFile()
let req: Request
do { req = try JSONDecoder().decode(Request.self, from: input) } catch {
    fail("decode request: \(error.localizedDescription)", code: 5)
}

var vectors: [[Float]] = []
vectors.reserveCapacity(req.texts.count)
for text in req.texts {
    if text.isEmpty {
        vectors.append([Float](repeating: 0, count: embedding.dimension))
        continue
    }
    do {
        let result = try embedding.embeddingResult(for: text, language: .english)
        var sum = [Double](repeating: 0, count: embedding.dimension)
        var count = 0
        result.enumerateTokenVectors(in: text.startIndex..<text.endIndex) { vector, _ in
            for (i, v) in vector.enumerated() { sum[i] += v }
            count += 1
            return true
        }
        if count == 0 {
            vectors.append([Float](repeating: 0, count: embedding.dimension))
        } else {
            vectors.append(sum.map { Float($0 / Double(count)) })
        }
    } catch { fail("embed: \(error.localizedDescription)", code: 6) }
}

let resp = Response(model: modelID, dim: embedding.dimension, vectors: vectors)
guard let out = try? JSONEncoder().encode(resp) else { fail("encode response", code: 7) }
FileHandle.standardOutput.write(out)
```

- [ ] **Step 2: Write the failing tests**

`internal/embeddings/apple_setup_test.go`:

```go
package embeddings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAppleHelperCompilesOnceThenSkips(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "bin", "axon-apple-embed")
	compiles := 0
	orig := appleCompile
	appleCompile = func(ctx context.Context, src, dst string) error {
		compiles++
		return os.WriteFile(dst, []byte("fake-binary"), 0o755)
	}
	defer func() { appleCompile = orig }()

	changed, err := EnsureAppleHelper(context.Background(), helper)
	if err != nil || !changed || compiles != 1 {
		t.Fatalf("first run: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
	changed, err = EnsureAppleHelper(context.Background(), helper)
	if err != nil || changed || compiles != 1 {
		t.Fatalf("second run should skip: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
	// A corrupted marker forces recompilation.
	if err := os.WriteFile(helper+".src.sha256", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = EnsureAppleHelper(context.Background(), helper)
	if err != nil || !changed || compiles != 2 {
		t.Fatalf("stale marker: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
}

func TestEnsureAppleHelperCompileFailure(t *testing.T) {
	dir := t.TempDir()
	orig := appleCompile
	appleCompile = func(ctx context.Context, src, dst string) error {
		return os.ErrPermission
	}
	defer func() { appleCompile = orig }()
	if _, err := EnsureAppleHelper(context.Background(), filepath.Join(dir, "h")); err == nil {
		t.Error("expected compile error to propagate")
	}
}
```

`internal/embeddings/apple_integration_test.go` (real compile + real embed; self-skipping):

```go
package embeddings

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAppleHelperEndToEnd compiles the real Swift helper and embeds through it.
// Skipped unless on macOS with swiftc available (and model assets present).
func TestAppleHelperEndToEnd(t *testing.T) {
	if runtime.GOOS != "darwin" || !SwiftAvailable() {
		t.Skip("requires macOS + swiftc")
	}
	helper := filepath.Join(t.TempDir(), "axon-apple-embed")
	if _, err := EnsureAppleHelper(context.Background(), helper); err != nil {
		t.Fatalf("compile helper: %v", err)
	}
	a := NewApple(helper, "apple-nlcontextual-v1", 512)
	vecs, err := a.Embed(context.Background(), []string{"hello world", "zettelkasten"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 512 {
		t.Fatalf("got %d vectors, dim %d", len(vecs), len(vecs[0]))
	}
	if err := a.Healthcheck(context.Background()); err != nil {
		t.Errorf("healthcheck: %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/embeddings/ -run 'TestEnsureAppleHelper|TestAppleHelperEndToEnd' -v`
Expected: compile error — `EnsureAppleHelper`, `appleCompile`, `SwiftAvailable` undefined. RED.

- [ ] **Step 4: Implement `internal/embeddings/apple_setup.go`**

```go
package embeddings

import (
	"context"
	_ "embed"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// appleHelperSource is the Swift helper program, embedded so `axon init` can
// (re)build the helper from an installed binary with no repo checkout.
//
//go:embed apple_helper.swift
var appleHelperSource []byte

// appleCompile compiles src → dst; a var so tests can fake the toolchain.
var appleCompile = swiftCompile

// SwiftAvailable reports whether the Swift compiler is on PATH (Xcode CLT).
func SwiftAvailable() bool {
	_, err := exec.LookPath("swiftc")
	return err == nil
}

// EnsureAppleHelper writes + compiles the embedded Swift helper to helperPath,
// idempotently: a SHA-256 marker beside the binary records the source it was
// built from, so re-runs skip compilation unless the embedded source changed.
// Returns changed=true when a (re)compile happened.
func EnsureAppleHelper(ctx context.Context, helperPath string) (bool, error) {
	sum := sha256.Sum256(appleHelperSource)
	want := hex.EncodeToString(sum[:])
	marker := helperPath + ".src.sha256"

	if have, err := os.ReadFile(marker); err == nil && string(have) == want {
		if st, err := os.Stat(helperPath); err == nil && st.Mode()&0o111 != 0 {
			return false, nil // up to date
		}
	}

	if err := os.MkdirAll(filepath.Dir(helperPath), 0o755); err != nil {
		return false, fmt.Errorf("apple helper: create dir: %w", err)
	}
	srcPath := helperPath + ".swift"
	if err := os.WriteFile(srcPath, appleHelperSource, 0o644); err != nil {
		return false, fmt.Errorf("apple helper: write source: %w", err)
	}
	if err := appleCompile(ctx, srcPath, helperPath); err != nil {
		return false, fmt.Errorf("apple helper: compile: %w", err)
	}
	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("apple helper: write marker: %w", err)
	}
	return true, nil
}

// swiftCompile is the real toolchain invocation (requires Xcode CLT).
func swiftCompile(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "swiftc", "-O", src, "-o", dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("swiftc: %w: %s", err, out)
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/embeddings/ -v`
Expected: all PASS. `TestAppleHelperEndToEnd` runs for real on this machine (macOS + swiftc present; first run may download assets). If it fails on the *API* (not environment), fix the Swift source — the API was verified live before this plan was written.

- [ ] **Step 6: Commit**

```bash
git add internal/embeddings/apple_helper.swift internal/embeddings/apple_setup.go \
        internal/embeddings/apple_setup_test.go internal/embeddings/apple_integration_test.go
git commit -m "Embed and compile the Apple embeddings Swift helper at init"
```

---

### Task 4: Provider construction in deps.go

**Files:**
- Modify: `cmd/axon/deps.go:70-72`
- Test: `cmd/axon/deps_test.go` (create)

**Interfaces:**
- Consumes: `embeddings.NewApple`, `config.DefaultAppleHelperPath()`.
- Produces: `embeddingsProvider(profile config.Profile) embeddings.Provider` returning `*embeddings.Apple` when provider is `apple`.

- [ ] **Step 1: Write the failing test** — create `cmd/axon/deps_test.go`:

```go
package main

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/embeddings"
)

func TestEmbeddingsProviderSelection(t *testing.T) {
	ollamaP := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "ollama", Model: "m", Dim: 8}}
	if _, ok := embeddingsProvider(ollamaP).(*embeddings.Ollama); !ok {
		t.Error("ollama provider not selected")
	}
	appleP := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "apple", Model: "m", Dim: 512}}
	if _, ok := embeddingsProvider(appleP).(*embeddings.Apple); !ok {
		t.Error("apple provider not selected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/axon/ -run TestEmbeddingsProviderSelection -v`
Expected: FAIL — apple case returns `*embeddings.Ollama`.

- [ ] **Step 3: Implement** — replace `embeddingsProvider` in `cmd/axon/deps.go`:

```go
// embeddingsProvider builds the configured embedding provider for a profile.
// Construction is lazy (no network/subprocess), so an unreachable Ollama or a
// missing Apple helper only surfaces when embedding is actually attempted.
func embeddingsProvider(profile config.Profile) embeddings.Provider {
	e := profile.Embeddings
	if e.Provider == "apple" {
		helper := e.Helper
		if helper == "" {
			helper = config.DefaultAppleHelperPath()
		}
		return embeddings.NewApple(helper, e.Model, e.Dim)
	}
	return embeddings.NewOllama(e.Host, e.Model, e.Dim)
}
```

- [ ] **Step 4: Run tests** — `go test ./cmd/axon/ -v` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/deps.go cmd/axon/deps_test.go
git commit -m "Select embeddings provider from config (ollama or apple)"
```

---

### Task 5: `axon init` convergence for the apple provider

**Files:**
- Modify: `internal/core/init.go:276-306` (probeEmbeddingModel dispatch + new probeAppleEmbedding)
- Test: `internal/core/init_test.go`

**Interfaces:**
- Consumes: `embeddings.EnsureAppleHelper`, `embeddings.SwiftAvailable`, `embeddings.NewApple`, `config.DefaultAppleHelperPath()`.
- Produces: `probeAppleEmbedding(ctx, e config.EmbeddingsConfig, deps appleProbeDeps) StepResult`; `probeEmbeddingModel` dispatches on `e.Provider`. All failures are `StepWarn` (never blocks init).

- [ ] **Step 1: Write the failing tests** — append to `internal/core/init_test.go`:

```go
func TestProbeAppleEmbedding(t *testing.T) {
	e := config.EmbeddingsConfig{Provider: "apple", Model: "apple-nlcontextual-v1", Dim: 512}
	ok := appleProbeDeps{
		goos:    "darwin",
		swiftOK: func() bool { return true },
		ensure:  func(ctx context.Context, path string) (bool, error) { return true, nil },
		probe:   func(ctx context.Context, helper string, e config.EmbeddingsConfig) error { return nil },
	}
	for _, tc := range []struct {
		name   string
		mutate func(*appleProbeDeps)
		status StepStatus
		detail string
	}{
		{"compiled and verified", func(*appleProbeDeps) {}, StepDone, "compiled"},
		{"non-darwin warns", func(d *appleProbeDeps) { d.goos = "linux" }, StepWarn, "macOS"},
		{"no swiftc warns", func(d *appleProbeDeps) { d.swiftOK = func() bool { return false } }, StepWarn, "xcode-select --install"},
		{"compile failure warns", func(d *appleProbeDeps) {
			d.ensure = func(context.Context, string) (bool, error) { return false, fmt.Errorf("boom") }
		}, StepWarn, "boom"},
		{"probe failure warns", func(d *appleProbeDeps) {
			d.probe = func(context.Context, string, config.EmbeddingsConfig) error { return fmt.Errorf("dim 512 != configured 768") }
		}, StepWarn, "dim"},
		{"already current", func(d *appleProbeDeps) {
			d.ensure = func(context.Context, string) (bool, error) { return false, nil }
		}, StepDone, "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := ok
			tc.mutate(&d)
			res := probeAppleEmbedding(context.Background(), e, d)
			if res.Status != tc.status || !strings.Contains(res.Detail, tc.detail) {
				t.Errorf("got %s %q; want %s containing %q", res.Status, res.Detail, tc.status, tc.detail)
			}
		})
	}
}

func TestProbeEmbeddingModelDispatchesApple(t *testing.T) {
	// The real dispatcher must not return the old generic "not checked" warning
	// for apple; it must run the apple probe (which on any OS yields a StepResult
	// whose Detail mentions the provider, never "not checked").
	res := probeEmbeddingModel(context.Background(), config.EmbeddingsConfig{Provider: "apple", Model: "m", Dim: 512})
	if strings.Contains(res.Detail, "not checked") {
		t.Errorf("apple provider fell through to the unknown-provider branch: %q", res.Detail)
	}
}
```

Add imports `"fmt"`, `"strings"` to the test file if absent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core/ -run 'TestProbeApple|TestProbeEmbeddingModelDispatches' -v`
Expected: compile error — `appleProbeDeps`, `probeAppleEmbedding` undefined. RED.

- [ ] **Step 3: Implement** in `internal/core/init.go`. Change the top of `probeEmbeddingModel`:

```go
func probeEmbeddingModel(ctx context.Context, e config.EmbeddingsConfig) StepResult {
	switch e.Provider {
	case "apple":
		return probeAppleEmbedding(ctx, e, realAppleProbeDeps())
	case "ollama":
		// fall through to the Ollama flow below
	default:
		return StepResult{"embeddings", StepWarn, fmt.Sprintf("provider %q not checked", e.Provider)}
	}
	// ... existing Ollama body unchanged ...
```

Then add (same file, after `probeEmbeddingModel`):

```go
// appleProbeDeps are the seams probeAppleEmbedding needs; injectable in tests.
type appleProbeDeps struct {
	goos    string
	swiftOK func() bool
	ensure  func(ctx context.Context, helperPath string) (bool, error)
	probe   func(ctx context.Context, helper string, e config.EmbeddingsConfig) error
}

func realAppleProbeDeps() appleProbeDeps {
	return appleProbeDeps{
		goos:    runtime.GOOS,
		swiftOK: embeddings.SwiftAvailable,
		ensure:  embeddings.EnsureAppleHelper,
		probe: func(ctx context.Context, helper string, e config.EmbeddingsConfig) error {
			return embeddings.NewApple(helper, e.Model, e.Dim).Healthcheck(ctx)
		},
	}
}

// probeAppleEmbedding converges the Apple provider (FR-01): compile the Swift
// helper if the embedded source changed, then probe a live embedding to assert
// the dimension. Warnings only — init never blocks on embeddings (search
// degrades to lexical-only), mirroring the Ollama convention.
func probeAppleEmbedding(ctx context.Context, e config.EmbeddingsConfig, d appleProbeDeps) StepResult {
	if d.goos != "darwin" {
		return StepResult{"embeddings", StepWarn, "provider \"apple\" requires macOS — set embeddings.provider: ollama on this machine"}
	}
	if !d.swiftOK() {
		return StepResult{"embeddings", StepWarn, "swiftc not found — install Xcode Command Line Tools (`xcode-select --install`), then re-run `axon init`"}
	}
	helper := e.Helper
	if helper == "" {
		helper = config.DefaultAppleHelperPath()
	}
	changed, err := d.ensure(ctx, helper)
	if err != nil {
		return StepResult{"embeddings", StepWarn, fmt.Sprintf("could not build Apple embeddings helper: %v", err)}
	}
	if err := d.probe(ctx, helper, e); err != nil {
		return StepResult{"embeddings", StepWarn, fmt.Sprintf("helper built but probe failed: %v", err)}
	}
	if changed {
		return StepResult{"embeddings", StepDone, fmt.Sprintf("Apple helper compiled at %s (dim %d verified)", helper, e.Dim)}
	}
	return StepResult{"embeddings", StepDone, fmt.Sprintf("Apple helper ready (dim %d verified)", e.Dim)}
}
```

Add `"runtime"` to init.go imports.

- [ ] **Step 4: Run tests** — `go test ./internal/core/ -v` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/init.go internal/core/init_test.go
git commit -m "Converge the apple embeddings provider in axon init (FR-01)"
```

---

### Task 6: `axon init --embeddings` flag (persist choice to config, then converge)

**Files:**
- Modify: `cmd/axon/config_cmd.go` (extract reusable setter)
- Modify: `cmd/axon/init_cmd.go`
- Test: `cmd/axon/init_cmd_test.go`

**Interfaces:**
- Produces: `setConfigValue(configPath, profileFlag, key, value string) error` in config_cmd.go (the comment-preserving, re-validated write currently inline in `newConfigSetCmd`); `axon init --embeddings ollama|apple` which, before running Init, writes `embeddings.provider` (+ `embeddings.model`/`embeddings.dim` to the apple defaults when switching to apple, using `config.AppleEmbeddingModel`/`config.AppleEmbeddingDim`).

- [ ] **Step 1: Write the failing test** — append to `cmd/axon/init_cmd_test.go` (follow the file's existing pattern of building the root command against a temp config; reuse its helper for writing a minimal valid config):

```go
func TestInitEmbeddingsFlagPersistsProvider(t *testing.T) {
	// Arrange a temp config (existing test helper pattern in this file), then:
	cfgPath := writeTestConfig(t) // reuse/adapt the file's existing config fixture helper
	root := newRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "init", "--embeddings", "apple"})
	_ = root.Execute() // init itself may warn; we assert only the persisted config

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Embeddings.Provider != "apple" {
		t.Errorf("provider = %q, want apple", p.Embeddings.Provider)
	}
	if p.Embeddings.Model != config.AppleEmbeddingModel || p.Embeddings.Dim != config.AppleEmbeddingDim {
		t.Errorf("model/dim = %q/%d, want apple defaults", p.Embeddings.Model, p.Embeddings.Dim)
	}
	// invalid value is refused
	root2 := newRootCmd()
	root2.SetArgs([]string{"--config", cfgPath, "init", "--embeddings", "banana"})
	if err := root2.Execute(); err == nil {
		t.Error("expected error for invalid --embeddings value")
	}
}
```

(If the file's fixture helper has a different name, adapt the call — the assertion body is the contract.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/axon/ -run TestInitEmbeddingsFlag -v`
Expected: FAIL — unknown flag `--embeddings`.

- [ ] **Step 3: Implement.** In `config_cmd.go`, extract the body of `newConfigSetCmd`'s RunE (read file → parse → jsonPathFor → ReplaceWithReader → re-validate → writeFileAtomic) into:

```go
// setConfigValue applies one comment-preserving, re-validated config edit.
// Only existing keys may be set (same contract as `axon config set`).
func setConfigValue(configPath, profileFlag, key, value string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		return err
	}
	name := cfg.ResolveProfileName(profileFlag)
	file, err := parser.ParseBytes(raw, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	path, err := yaml.PathString(jsonPathFor(name, key))
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
	}
	if _, rerr := path.ReadNode(bytes.NewReader(raw)); rerr != nil {
		return fmt.Errorf("key %q not found; only existing keys can be set", key)
	}
	if err := path.ReplaceWithReader(file, strings.NewReader(yamlScalar(value))); err != nil {
		return fmt.Errorf("set %q: %w", key, err)
	}
	updated := []byte(file.String())
	if _, err := config.Parse(updated); err != nil {
		return fmt.Errorf("refusing to write: the change makes the config invalid: %w", err)
	}
	return writeFileAtomic(configPath, updated)
}
```

and make `newConfigSetCmd`'s RunE call it. In `init_cmd.go` add the flag and pre-step:

```go
var embeddingsChoice string
// in RunE, BEFORE loadProfileDeps / config load:
if embeddingsChoice != "" {
	if embeddingsChoice != "ollama" && embeddingsChoice != "apple" {
		return fmt.Errorf("--embeddings must be ollama or apple, got %q", embeddingsChoice)
	}
	if err := setConfigValue(gf.configPath, gf.profile, "embeddings.provider", embeddingsChoice); err != nil {
		return fmt.Errorf("persist embeddings provider: %w", err)
	}
	if embeddingsChoice == "apple" {
		if err := setConfigValue(gf.configPath, gf.profile, "embeddings.model", config.AppleEmbeddingModel); err != nil {
			return fmt.Errorf("persist embeddings model: %w", err)
		}
		if err := setConfigValue(gf.configPath, gf.profile, "embeddings.dim", strconv.Itoa(config.AppleEmbeddingDim)); err != nil {
			return fmt.Errorf("persist embeddings dim: %w", err)
		}
	}
}
// ... then the existing init flow (which now reads the updated config)
// flag registration:
cmd.Flags().StringVar(&embeddingsChoice, "embeddings", "", "select the embeddings provider (ollama|apple) and persist it to config before converging")
```

- [ ] **Step 4: Run tests** — `go test ./cmd/axon/ -v` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/config_cmd.go cmd/axon/init_cmd.go cmd/axon/init_cmd_test.go
git commit -m "Add axon init --embeddings to choose and persist the provider"
```

---

### Task 7: Doctor + hints + dashboard health become provider-aware

**Files:**
- Modify: `internal/core/doctor.go:85-87`
- Modify: `internal/ui/hint.go:38-41`
- Modify: `cmd/axon/start_cmd.go:101-109` (wire the dashboard `Health` func)
- Test: `internal/core/doctor_test.go`, existing hint tests file (check `internal/ui/`)

**Interfaces:**
- Consumes: `config.DefaultAppleHelperPath()`, profile embeddings config.
- Produces: `embeddingsCheck(p config.Profile) Check` in doctor.go replacing the unconditional ollama `binaryCheck`; dashboard `/health` payload gains `embeddings_provider`, `embeddings_model`, `embeddings_dim`.

- [ ] **Step 1: Write the failing tests** — append to `internal/core/doctor_test.go`:

```go
func TestEmbeddingsCheckProviderAware(t *testing.T) {
	ollama := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "ollama", Model: "m", Dim: 8}}
	if c := embeddingsCheck(ollama); c.Name != "ollama" {
		t.Errorf("ollama profile should keep the ollama binary check, got %q", c.Name)
	}
	apple := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "apple", Model: "m", Dim: 512,
		Helper: "/nonexistent/axon-apple-embed"}}
	c := embeddingsCheck(apple)
	if c.Name != "apple-embeddings" || c.Status != StatusWarn || !strings.Contains(c.Detail, "axon init") {
		t.Errorf("missing helper should warn pointing at axon init, got %+v", c)
	}
	// A present, executable helper is OK.
	dir := t.TempDir()
	helper := filepath.Join(dir, "axon-apple-embed")
	if err := os.WriteFile(helper, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	apple.Embeddings.Helper = helper
	if c := embeddingsCheck(apple); c.Status != StatusOK {
		t.Errorf("existing helper should be OK, got %+v", c)
	}
}
```

For hints, add to the hint tests (`internal/ui/` — locate the test file for hint.go; create `hint_test.go` if absent):

```go
func TestHintAppleHelper(t *testing.T) {
	h := HintFor("apple embed helper /x/axon-apple-embed: fork/exec: no such file or directory")
	if !strings.Contains(h, "axon init") {
		t.Errorf("apple helper hint should point at axon init, got %q", h)
	}
}
```

(Adapt the function name to hint.go's actual exported entry point — read the file first; the assertion is the contract.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core/ ./internal/ui/ -run 'TestEmbeddingsCheck|TestHintApple' -v`
Expected: compile error (`embeddingsCheck` undefined) / FAIL (no apple hint). RED.

- [ ] **Step 3: Implement.**

doctor.go — replace line 86-87's unconditional check with `checks = append(checks, embeddingsCheck(profile))` (the `Doctor` func resolves the active profile already — reuse that variable) and add:

```go
// embeddingsCheck verifies the configured embeddings provider's local
// prerequisite: the ollama binary, or the compiled Apple helper.
func embeddingsCheck(p config.Profile) Check {
	if p.Embeddings.Provider == "apple" {
		const name = "apple-embeddings"
		helper := p.Embeddings.Helper
		if helper == "" {
			helper = config.DefaultAppleHelperPath()
		}
		st, err := os.Stat(helper)
		if err != nil || st.Mode()&0o111 == 0 {
			return Check{name, StatusWarn, fmt.Sprintf("Apple embeddings helper not built at %s — run `axon init` (requires Xcode CLT)", helper)}
		}
		return Check{name, StatusOK, "Apple embeddings helper present: " + helper}
	}
	return binaryCheck("ollama", "ollama",
		"Ollama found", "ollama not found on PATH (needed for local embeddings in Phase 2)")
}
```

hint.go — add a case above the existing Ollama one:

```go
// Apple embeddings helper missing/failed.
case strings.Contains(msg, "apple embed helper"), strings.Contains(msg, "axon-apple-embed"):
	return "The Apple embeddings helper isn't built or failed. Re-run `axon init` (needs Xcode Command Line Tools), then verify with `axon doctor`."
```

start_cmd.go — in the `dashboard.New(dashboard.Config{...})` literal add:

```go
Health: func(ctx context.Context) map[string]any {
	return map[string]any{
		"embeddings_provider": deps.profile.Embeddings.Provider,
		"embeddings_model":    deps.profile.Embeddings.Model,
		"embeddings_dim":      deps.profile.Embeddings.Dim,
	}
},
```

(Add `"context"` to start_cmd.go imports if absent.)

- [ ] **Step 4: Run tests** — `go test ./internal/core/ ./internal/ui/ ./cmd/axon/ -v` — all PASS; `go vet ./...` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/doctor_test.go internal/ui/ cmd/axon/start_cmd.go
git commit -m "Make doctor, hints and dashboard health embeddings-provider-aware"
```

---

### Task 8: Installer scripts — provider choice on macOS, rejection on Linux

**Files:**
- Modify: `scripts/install-macos.sh` (flag + prompt + conditional Ollama/apple steps)
- Modify: `scripts/install-linux.sh` (reject apple)
- Modify: `scripts/preflight.sh` (swiftc note on macOS)
- Modify: `scripts/_common.sh` (install hint for Xcode CLT)

**Interfaces:**
- Produces: `install-macos.sh --embeddings ollama|apple`; interactive prompt when the flag is absent, a TTY is present, and the config was just created; `axon init --embeddings <choice>` invoked to persist; Ollama steps skipped for apple.

- [ ] **Step 1: Implement `_common.sh` hint** — in the `install_hint` case list (line ~106) add:

```bash
    xcode-clt)
      case "$OS" in
        macos) echo "xcode-select --install" ;;
        *)     echo "not available on this OS" ;;
      esac ;;
```

- [ ] **Step 2: Implement `install-macos.sh`.**

Add to the option block and parser (after `--skip-build`):

```bash
#   --embeddings P   embeddings provider: ollama or apple  (default: ask on a TTY, else ollama)
EMBEDDINGS=""
    --embeddings) EMBEDDINGS="$2"; shift 2 ;;
```

After option parsing, validate:

```bash
case "$EMBEDDINGS" in ""|ollama|apple) : ;; *) err "--embeddings must be ollama or apple"; usage 1 ;; esac
```

After the config-creation block (`created_config=1`) and BEFORE `config validate`, insert the choice step:

```bash
# ── Embeddings provider choice ───────────────────────────────────────────────
step "Embeddings provider"
if [ -z "$EMBEDDINGS" ]; then
  if [ "$created_config" -eq 1 ] && [ -t 0 ]; then
    if confirm "Use Apple's on-device model for embeddings instead of Ollama? (no Ollama server needed; requires Xcode CLT)"; then
      EMBEDDINGS=apple
    else
      EMBEDDINGS=ollama
    fi
  else
    EMBEDDINGS="$("${AX[@]}" config get embeddings.provider 2>/dev/null || echo ollama)"
    skip "keeping configured provider '$EMBEDDINGS' (choose with --embeddings ollama|apple)"
  fi
fi
if [ "$EMBEDDINGS" = apple ]; then
  DO_OLLAMA=0
  xcode-select -p >/dev/null 2>&1 || warn "Xcode Command Line Tools not found — the helper build in 'axon init' will be skipped until you run: $(install_hint xcode-clt)"
fi
ok "embeddings provider: $EMBEDDINGS"
```

Guard the *existing* Ollama convenience-install block (step 1 area, `if [ "$DO_OLLAMA" -eq 1 ] && ! have ollama`) so it only runs when `[ "$EMBEDDINGS" != apple ]` — since `EMBEDDINGS` is resolved later, move the Ollama convenience-install block to AFTER the choice step (it currently sits before the build; relocate it just before "── 5. Ollama at login" and merge with that step). Change the `axon init` invocation (step 6):

```bash
"${AX[@]}" init --embeddings "$EMBEDDINGS"   # persists the choice, then streams its ✓/↻/⚠/✗ report
```

- [ ] **Step 3: Implement `install-linux.sh` rejection** — after the config-validate line, add:

```bash
PROVIDER="$("${AX[@]}" config get embeddings.provider 2>/dev/null || echo ollama)"
[ "$PROVIDER" = apple ] && die "embeddings.provider 'apple' is macOS-only — set it to 'ollama' in $CONFIG"
```

- [ ] **Step 4: Implement `preflight.sh`** — in the runtime scope, after the ollama check, add:

```bash
  if [ "$OS" = macos ]; then
    if have swiftc; then report_ok "swiftc" "enables the 'apple' embeddings provider"
    else report_bad optional "swiftc" "only needed for embeddings.provider: apple" xcode-clt; fi
  fi
```

(Reuse the script's existing `OS` detection variable; if it's named differently — check `_common.sh` — use that name.)

- [ ] **Step 5: Verify**

Run: `bash -n scripts/install-macos.sh scripts/install-linux.sh scripts/preflight.sh scripts/_common.sh`
Expected: no output (all parse). Then `shellcheck scripts/install-macos.sh scripts/preflight.sh` if shellcheck is installed — no NEW warnings vs. `git stash && shellcheck ... && git stash pop` baseline.
Also: `scripts/preflight.sh --runtime` runs and reports swiftc on this machine.

- [ ] **Step 6: Commit**

```bash
git add scripts/install-macos.sh scripts/install-linux.sh scripts/preflight.sh scripts/_common.sh
git commit -m "Offer ollama vs apple embeddings choice in the installers"
```

---

### Task 9: Docs — example config, config reference, ADR

**Files:**
- Modify: `axon.config.example.yaml` (embeddings blocks + comments)
- Modify: `docs/04-data-model-and-config.md` (embeddings config reference)
- Modify: `docs/02-architecture.md` (append ADR-011)
- Modify: `docs/05-component-...` ingestion/embeddings spec if it names Ollama as the only provider (check `docs/05*`)

**Interfaces:** none (documentation).

- [ ] **Step 1: Example config.** In `axon.config.example.yaml`, update BOTH profiles' `embeddings:` comment blocks. Replace the personal profile's block comment with:

```yaml
    embeddings:
      provider: ollama                        # ollama | apple
      #   ollama : local Ollama server (any pulled model; cross-platform)
      #   apple  : Apple's on-device model (macOS 14+; no server; helper built
      #            by `axon init`, needs Xcode CLT). Use with:
      #              provider: apple, model: apple-nlcontextual-v1, dim: 512
      #            Switching provider changes dim → run `axon reindex --embeddings`.
      host: "http://localhost:11434"          # ollama only
      model: nomic-embed-text                 # 768-dim. Changing the model forces a full re-index.
      dim: 768                                # MUST match the model's output dimension
      batch_size: 32
      # helper: "~/.axon/bin/axon-apple-embed"  # apple only: helper binary override
```

- [ ] **Step 2: Config reference.** In `docs/04-data-model-and-config.md`, find the embeddings section and document: the `provider` enum, apple's requirements (macOS 14+, Xcode CLT), defaults (`apple-nlcontextual-v1`, dim 512), the `helper` override, and the switch procedure (`axon config set embeddings.provider apple && axon config set embeddings.model apple-nlcontextual-v1 && axon config set embeddings.dim 512 && axon init && axon reindex --embeddings` — or just `axon init --embeddings apple && axon reindex --embeddings`).

- [ ] **Step 3: ADR.** Append to `docs/02-architecture.md`, following the file's existing ADR format exactly (read ADR-010 for the template):

> **ADR-011 — Apple on-device embeddings via a compiled-at-init Swift helper subprocess.**
> Context: users on macOS want embeddings without running an Ollama server; NLContextualEmbedding is Swift-only (no HTTP/CLI). Decision: ship the Swift source embedded in the axon binary, compile it once during `axon init` with swiftc (Xcode CLT prereq), and shell out to it with JSON over stdin/stdout — the same subprocess seam as the `claude -p` adapter. Rejected: cgo (breaks the pure-Go static binary and cross-compilation), committed prebuilt binaries (drift, signing). Consequences: apple provider is darwin-only; dim moves 768→512 so provider switches require `axon reindex --embeddings`; the helper is a machine-level artifact in `~/.axon/bin`, outside profile isolation.

- [ ] **Step 4: Check docs/05** — `grep -n -i "ollama" docs/05*.md docs/09*.md` and adjust any sentence that says Ollama is the only embedding path to mention the provider enum ("Ollama (default) or Apple on-device (ADR-011)").

- [ ] **Step 5: Verify + commit**

Run: `go build ./... && go test ./... ` (docs changes can't break the build, but this is the slice gate) and `axon config validate` against a config with `provider: apple` written to a temp file via `go run ./cmd/axon --config <tmp> config validate`.

```bash
git add axon.config.example.yaml docs/
git commit -m "Document the apple embeddings provider (config reference + ADR-011)"
```

---

### Task 10: End-to-end verification (definition of done)

**Files:** none new — verification only.

- [ ] **Step 1: Full gates**

```bash
gofmt -l . | grep -v web/ ; go vet ./... && go test ./... && golangci-lint run
```
Expected: no gofmt output, vet clean, all tests pass, lint green.

- [ ] **Step 2: Live slice proof on this machine (macOS + swiftc present)**

```bash
export TMPHOME=$(mktemp -d)
# minimal config with provider apple, vault + data dir under TMPHOME
go run ./cmd/axon --config "$TMPHOME/config.yaml" init          # after writing a minimal config there
go run ./cmd/axon --config "$TMPHOME/config.yaml" doctor
```
Expected: init's `embeddings` step reports "Apple helper compiled … (dim 512 verified)"; doctor's `apple-embeddings` check is OK; re-running init reports the helper as already current (idempotency). Then flip the config back to ollama, re-run doctor, confirm the ollama check returns.

- [ ] **Step 3: Integration test explicitly**

Run: `go test ./internal/embeddings/ -run TestAppleHelperEndToEnd -v`
Expected: PASS (real compile, real 512-dim vectors).

- [ ] **Step 4: Commit any fixes; final commit if needed.**

---

## Self-Review (done at plan time)

- **Spec coverage:** config enum+helper (T1), adapter+protocol+stdout-in-errors (T2), embedded source+compile+idempotency+asset download (T3 — assets handled inside the helper), deps wiring+non-darwin guard (T2/T4), init convergence warnings-only (T5), init flag persistence (T6), doctor/hints/dashboard (T7), installers+preflight+linux rejection (T8), docs+ADR (T9), reindex-on-switch documented (T9) and enforced by existing dim-mismatch errors (T2). ✔
- **Placeholder scan:** two intentional adapt-points are flagged as such with the contract stated (init_cmd test fixture helper name, hint.go entry-point name) — implementer reads the file first; everything else is complete code. ✔
- **Type consistency:** `EnsureAppleHelper(ctx, helperPath) (bool, error)` used identically in T3/T5; `NewApple(helperPath, model, dim)` in T2/T4/T5; `config.AppleEmbeddingModel`/`AppleEmbeddingDim` in T1/T6/T9; `appleRequest/appleResponse` internal to T2. ✔
