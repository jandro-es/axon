# Apple on-device embeddings provider — design

**Date:** 2026-07-02
**Status:** approved (brainstormed + user-approved)
**Traces to:** FR-01 (init convergence), embeddings/search FRs (docs/03), ADR-001 (provider seam), ADR-010 (in-file vectors)

## Goal

Let a user choose Apple's on-device embedding model instead of Ollama for AXON's
vector pipeline (chunk + query embeddings for hybrid search), selected at
install time and switchable later purely through config. Generation is out of
scope: every generative call stays on Claude through the token manager
(cardinal rule 1).

## Background / constraints

- Ollama is today the only `embeddings.Provider` implementation
  (`internal/embeddings/ollama.go`), constructed in `cmd/axon/deps.go`.
- Apple's *FoundationModels* framework is generation-only. The on-device
  embedding API is **NLContextualEmbedding** (NaturalLanguage framework,
  macOS 14+): Swift-only, no HTTP/CLI surface, model assets downloaded on
  demand, sentence vector obtained by mean-pooling token vectors. Exact API
  surface to be verified against current Apple docs during implementation.
- The Go binary must stay pure Go / single static binary (no cgo).
- The vault is the source of truth; vectors are derived and rebuildable
  (`axon reindex --embeddings`).

## Decisions

1. **Scope: embeddings only.** The `apple` provider replaces Ollama for
   vectors; Claude remains the sole generative path.
2. **Bridge: Swift helper subprocess.** A small Swift program shipped as
   source, compiled once at init time, invoked per `Embed` call with JSON over
   stdin/stdout — the same subprocess pattern as the `claude -p` adapter.
3. **Choice surface: installer flag + init prompt + config.** Config is the
   single source of truth; installer and init are conveniences that write it.

## Design

### Config

- `embeddings.provider: ollama | apple`, validated `oneof=ollama apple`
  (mirrors `auth_mode`'s validation style).
- For `apple`:
  - `host` is ignored.
  - `model` is an informational identifier (default `apple-nlcontextual-v1`)
    that keys the index, so a model change forces re-embedding exactly as with
    Ollama models.
  - `dim` must equal the helper's reported dimension (expected 512 for the v1
    multilingual model; the probe verifies the live value).
  - New optional `embeddings.helper`: path override for the helper binary.
    Default: `~/.axon/bin/axon-apple-embed`.
- Switching provider later: edit config or `axon config set
  embeddings.provider apple`, then `axon reindex --embeddings` (dim changes,
  e.g. 768 → 512, make this mandatory; doctor says so).

### Provider adapter (`internal/embeddings/apple.go`)

- `Apple` struct implements the existing 4-method `Provider` interface
  (Embed / Model / Dim / Healthcheck). No caller changes.
- Per `Embed` call it runs the helper as a subprocess:
  - stdin: `{"texts": ["...", ...]}`
  - stdout: `{"model": "...", "dim": 512, "vectors": [[...], ...]}`
  - errors: stderr + non-zero exit. Failure messages include both stderr and
    stdout (capped), matching the Claude adapter's failure-output behaviour.
- Context-aware execution with process-group kill and `WaitDelay`, reusing the
  `agent` package's subprocess hardening pattern.
- Injectable executor function for tests (same seam as `ClaudeCode.run`).
- Constructing the provider on non-darwin returns a clear error.
- `Healthcheck` embeds a probe string, asserts vector count and live dim
  against config.
- Vector-count and per-vector dim validation identical to the Ollama
  provider's (`got N vectors for M inputs`, dim-mismatch → suggest reindex).

### Swift helper

- Source embedded in the axon binary via `embed.FS` (like the vault
  scaffold assets), so `axon init` works from an installed binary with no
  repo checkout.
- `axon init` (apple provider only):
  1. checks darwin + Xcode Command Line Tools (`xcode-select -p`);
  2. writes the embedded source to the data dir and compiles it with
     `swiftc -O` into `~/.axon/bin/axon-apple-embed`;
  3. idempotent via a content-hash marker beside the binary — re-runs skip
     compilation unless the embedded source changed;
  4. triggers the helper's asset download (one-time, analogous to
     `ollama pull`) and runs the dim-verifying probe.
- Helper behaviour: read request JSON, ensure NLContextualEmbedding assets,
  embed each text (mean-pooled sentence vector), emit response JSON. Exit
  non-zero with a human-readable stderr message on any failure (assets not
  downloadable, OS too old, etc.).

### Install & update UX

- `scripts/install-macos.sh`: new `--embeddings=ollama|apple` flag; with no
  flag on a TTY it asks interactively (default ollama). Choosing apple skips
  Ollama install/model-pull steps and checks Xcode CLT instead.
- `scripts/preflight.sh`: provider-aware — ollama optional-check only when the
  config (or flag) selects ollama; Xcode CLT check when apple.
- `scripts/install-linux.sh`: rejects `apple` explicitly with a clear message.
- `axon init --embeddings ollama|apple`: persists the choice to config before
  converging. With no flag, init converges whatever config names. All apple
  convergence failures are warnings, never hard failures — search degrades to
  lexical-only, same convention as an unreachable Ollama.

### Observability & guidance

- `axon doctor`: provider-aware check — apple: helper present + executable +
  probe ok; ollama: existing checks. Warns when the index's stored vector dim
  differs from config (reindex needed).
- `internal/ui/hint.go`: apple-helper hint alongside the Ollama one.
- Dashboard health detail reports the active provider.

### Testing

- Table-driven unit tests against the injectable executor: protocol
  encode/decode, vector-count mismatch, dim mismatch, helper missing,
  non-darwin construction guard, stdout-in-error.
- Init-step tests via the existing `CheckEmbeddingModel`-style override hooks
  with faked compile/probe functions.
- One integration test that compiles and runs the real helper; skipped unless
  darwin + swiftc present.

### Docs

- `docs/04` config reference + `axon.config.example.yaml`: document the
  provider enum, apple defaults, and the switch/reindex procedure.
- New ADR (docs/02 format): "Apple on-device embeddings via Swift helper
  subprocess" — records the no-cgo constraint and the compiled-at-init choice.

## Trade-offs accepted

- Apple path requires macOS 14+ and Xcode CLT (one-time
  `xcode-select --install`); in exchange: no Ollama server, no model
  management, Apple-native and fully local.
- Ollama remains the default everywhere; nothing changes for existing
  installs.

## Out of scope

- Apple FoundationModels text generation (would renegotiate cardinal rule 1).
- Windows/Linux support for the apple provider.
- Automatic re-embedding on provider switch (user runs `axon reindex
  --embeddings`; doctor/init tell them when).
