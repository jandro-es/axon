# Local model routing for cheap tiers (Ollama + Apple Foundation Models) — design

**Date:** 2026-07-03
**Status:** approved (brainstormed + user-approved)
**Traces to:** new FR-77…FR-80 (docs/03), ADR-001 (local-model seam), ADR-007 (chokepoint), ADR-009 (auth modes), ADR-013 (Swift helper pattern), new ADR-015

## Goal

Let config route the cheap model tiers (`classify`, and optionally `routine`)
to a local model — Ollama chat or Apple's on-device Foundation Models — so
triage, enrichment metadata, and other low-stakes operations become free and
offline, while the Claude subscription is reserved for synthesis-tier work.
Every call, local or Claude, still enters through the Component 07 token
manager chokepoint.

This deliberately renegotiates the out-of-scope note in
`2026-07-02-apple-embeddings-provider-design.md` ("Apple FoundationModels text
generation would renegotiate cardinal rule 1"): local generation is now in
scope **because** it runs inside the chokepoint, not around it. Cardinal
rule 1 generalizes to: *no generative call — Claude or local — bypasses the
token manager.* `tokens` remains the only importer of `agent`.

## Background / constraints

- `agent.Agent` is a two-method interface (`Run`, `AuthMode`) designed "so a
  local-model adapter can satisfy it later" (`internal/agent/agent.go`).
- `tokens.manager` holds a single adapter, injected once by the only factory,
  `cmd/axon/deps.go agentAdapter()`. Tier → model resolution is
  `resolveModel` (`internal/tokens/manager.go:190`).
- `models.classify/routine/synthesis` are plain strings today, passed to
  `claude -p --model`.
- Apple Foundation Models (macOS 26+, Apple Silicon, Apple Intelligence
  enabled): on-device ~3B `SystemLanguageModel`, Swift-only API, guided
  generation for schema-constrained output, small context window shared
  between prompt and response. No usage reporting. Exact API surface to be
  verified against current Apple docs during implementation.
- The Go binary stays pure Go (no cgo) — the ADR-013 compiled-at-init Swift
  helper subprocess pattern applies verbatim.
- Budget windows (`daily_tokens`/`weekly_tokens`) exist to protect the Claude
  subscription quota; budget-guard pauses non-essential automations only
  (deliberate, unchanged).

## Decisions (user-approved)

1. **Provider-prefixed model strings, not a structured block.**
   `ollama:<model>` → Ollama; `apple` → Apple on-device model; anything else →
   Claude, exactly as today. Zero migration for existing configs. (A
   `{provider, model}` block per tier was rejected: breaks every existing
   config and the TUI for no functional gain.)
2. **Local calls are ledgered but budget-exempt.** They never consume the
   token windows, never trigger defer/deny/downgrade, and never contribute to
   budget-guard pressure. Budgets keep meaning "Claude quota". Observability
   is preserved: every local call writes a `token_ledger` row (`cost_usd`
   null) and emits the usual events.
3. **Fallback is a toggle, default fall-forward.**
   `models.local_fallback: claude | fail` (default `claude`): on local
   transport failure or output-validation failure, retry locally once, then
   route the same call to Claude through the normal budget path — or fail the
   run visibly when set to `fail`.
4. **Apple provider is classify-tier only**, enforced by validation: the
   shared context window cannot carry routine/synthesis inputs. Ollama may be
   configured on `classify` and `routine`; `synthesis` always stays Claude.
5. **On-device only.** The Foundation Models framework's other backends
   (Private Cloud Compute, third-party providers) are explicitly out of
   scope — they would violate G7 (local-first).

## Design

### Config (`internal/config/types.go`)

- `ModelsConfig` fields stay strings; new parse helper (in `config`) resolves
  a tier string to `(provider, model)`:
  - `ollama:qwen3:8b` → provider `ollama`, model `qwen3:8b` (first colon
    splits; the rest is the Ollama model tag verbatim).
  - `apple` → provider `apple` (no model name; identifier
    `apple-foundation-v1` used for ledger/index purposes, default in
    `config/paths.go` beside `AppleEmbeddingModel`).
  - anything else → provider `claude`.
- New optional fields on `ModelsConfig`:
  - `ollama_host` (default `http://127.0.0.1:11434`)
  - `local_fallback` — `oneof=claude fail`, default `claude`
  - `apple_helper` — path override for the helper binary, default
    `<AXON_HOME>/bin/axon-apple-lm` (machine-level, like the embeddings
    helper).
- Validator rules: `apple` rejected on `routine`/`synthesis`; any local
  provider rejected on `synthesis` (it always stays Claude, per Decision 4);
  `ollama:` with empty model rejected; non-darwin + `apple` is a
  doctor/runtime concern, not a config error (configs are portable across
  machines).

### Adapter router (`internal/tokens/manager.go`)

- `tokens.New` accepts an `agent.Router` (new, in `internal/agent`) instead of
  a single `agent.Agent`:
  `type Router interface { Resolve(provider string) (Agent, error) }` with a
  map-backed implementation. Dependency rule unchanged: `tokens` is still the
  only importer of `agent`; the router is composed in `cmd/axon/deps.go`
  `agentAdapter()` (renamed `agentRouter()`), constructing only the adapters
  the active config references.
- `resolveModel` grows into `resolveCall(key) (provider, model string)`;
  `Authorize`/`Run` branch on `provider != "claude"`:
  - skip window checks and downgrade logic (always `proceed`);
  - `downgradeKey` skips tiers whose provider is local (a local tier is
    neither a downgrade source nor target);
  - `record` writes the ledger row (model string names the provider, e.g.
    `ollama:qwen3:8b` / `apple-foundation-v1`) but skips `AddBudgetUsage`.
- New optional `AgentCall.ValidateOutput func(string) error`. `Run` applies it
  to every response (Claude included — a no-op improvement there); for local
  providers a failure triggers the retry/fallback ladder:
  local attempt → local retry → (`local_fallback: claude` ? same call
  re-authorized and re-run on the Claude path : error surfaced as a failed
  run with the usual `:failed` ledger row and event).
- Pre-flight: for `apple`, the existing heuristic estimate is checked against
  a conservative input cap (constant, ~3500 tokens); an oversized input
  short-circuits straight to the fallback ladder without invoking the helper.

### Ollama adapter (`internal/agent/ollama.go`)

- `NewOllama(host string, timeout time.Duration)`; `Run` POSTs
  `/api/chat` (`stream: false`) with system + user messages, maps
  `message.content` → `Response.Text` and
  `prompt_eval_count`/`eval_count` → `Usage`. `AuthMode()` returns `"local"`.
- `format: "json"` is set when the call carries a `ValidateOutput` expecting
  JSON — passed as a plain request hint field (`Request.JSONOutput bool`) set
  by the manager when a validator is present.
- HTTP client pattern mirrors `internal/embeddings/ollama.go`; injectable
  transport for tests.

### Apple Foundation Models adapter (ADR-013 pattern, verbatim)

- Files mirror the embeddings trio:
  `internal/agent/applefm.go`, `applefm_helper.swift` (via `go:embed`),
  `applefm_setup.go` (`EnsureAppleLMHelper` — write source, `swiftc -O`,
  sha256 marker, idempotent).
- Subprocess protocol, one process per call:
  - stdin: `{"system": "...", "prompt": "...", "max_tokens": N, "schema": {…}?}`
  - stdout: `{"text": "...", "input_tokens": N, "output_tokens": N}`
    (token counts are the helper's estimates; Go side falls back to the
    heuristic estimator when absent)
  - errors: stderr + distinct non-zero exit codes (unavailable=3, context
    overflow=4, guardrail refusal=5, …).
- `--check-availability` argv flag reports `SystemLanguageModel` availability
  without side effects (0=available, 3=not) — doctor's cheap probe.
- When `schema` is present the helper uses guided generation
  (`DynamicGenerationSchema`) so classify-tier JSON output is type-safe at the
  source; without it, plain text. The Go-side `Request` carries the optional
  schema as raw JSON provided by the caller via `AgentCall.OutputSchema
  json.RawMessage` (nil for Claude callers today; enrichment/triage adopt it
  opportunistically).
- Executor reuses the `agent` package's subprocess hardening (`WaitDelay`,
  process-group kill, injectable `run`). Non-darwin construction returns a
  clear error.

### Caller adoption (minimal, mechanical)

- `ingestion.ClaudeEnricher` (`ModelKey: routine`) and
  `automation.inbox-triage` (`ModelKey: classify`) pass their existing JSON
  parse as `ValidateOutput` (and optionally `OutputSchema`). No other call
  sites change; tier routing covers them automatically via config.

### Ops surface

- **`axon configure models`**: per tier, a provider `tui.Select`
  (Claude / Ollama / Apple on-device — the last shown only for `classify` on
  darwin), then a model-string `tui.Input` where applicable, then
  convergence: Ollama → probe `/api/chat` with a one-token round trip and
  check the model exists via `/api/tags`; Apple → `EnsureAppleLMHelper` +
  `--check-availability`. Mirrors `switchEmbeddings`
  (`cmd/axon/configure_embeddings.go`).
- **`axon init`**: compiles the LM helper only when a tier is set to `apple`
  (new probe beside `probeAppleEmbedding` in `internal/core/init.go`,
  warnings-only — a failed local provider never blocks init; the fallback
  ladder covers runtime).
- **`axon doctor`**: provider-aware per configured tier — Ollama: host
  reachable + model present in `/api/tags`; Apple: helper binary present +
  executable + `--check-availability` passes; both: clear remediation text
  (`ollama pull …` / `axon init` / enable Apple Intelligence). Non-darwin
  with `apple` configured → warn "this machine cannot serve the classify
  tier; calls will use the fallback".
- **Dashboard**: no SPA changes; local model strings appear naturally in
  by-model charts, and budget gauges are unaffected by design.

### Testing

- Table-driven manager tests against `agent.Fake` + a fake router: tier
  resolution per prefix, budget exemption (no `AddBudgetUsage`, Authorize
  always proceeds), downgrade skipping local tiers, fallback ladder in both
  `claude` and `fail` modes, validator-failure retry, apple input-cap
  short-circuit, ledger rows written for local calls.
- Ollama adapter against `httptest.Server`: request shape, usage mapping,
  `format: "json"` hint, error paths.
- Apple adapter with injectable executor: protocol encode/decode, exit-code
  mapping, schema pass-through, non-darwin guard — mirroring the embeddings
  adapter tests.
- Config tests: prefix parsing, validator rules (apple-on-synthesis rejected,
  `local_fallback` enum).
- One darwin-gated integration test compiling and running the real helper,
  CI-safe (skipped unless darwin + swiftc + assets available), following the
  existing Apple embeddings e2e gating.

### Docs

- **ADR-015** in docs/02: "Local model routing through the token-manager
  chokepoint (Ollama + Apple Foundation Models)" — amends ADR-001 (exploits
  the seam), ADR-007 (generalizes cardinal rule 1 to all generative calls),
  ADR-009 (a `local` provider axis orthogonal to `auth_mode`); reuses
  ADR-013's helper pattern; records the on-device-only scope decision.
- **FR-77…FR-80** appended to docs/03:
  - FR-77 (M): per-tier local provider routing via prefixed model strings;
    every local call passes through the token manager and is ledgered.
  - FR-78 (M): local calls are budget-exempt but fully observable.
  - FR-79 (M): configurable fallback (`claude`/`fail`, default `claude`) on
    local failure or invalid output.
  - FR-80 (S): Apple Foundation Models adapter (darwin/arm64, classify tier
    only, guided generation when a schema is provided).
- docs/04 config reference + `axon.config.example.yaml`: prefix scheme,
  `ollama_host`, `local_fallback`, `apple_helper`, apple constraints.
- docs/07: update the model-selection section (tier → provider+model, local
  budget exemption, fallback ladder).

## Trade-offs accepted

- Small local models produce lower-quality output than Haiku; mitigated by
  schema validation + guided generation + the fall-forward default. Users who
  set `local_fallback: fail` accept visible degradation over token spend.
- The Apple path adds an OS/hardware/Apple-Intelligence gate surfaced by
  doctor; in exchange: zero-install, zero-cost, offline classify tier.
- Two more subprocess/HTTP dependencies in the runtime path — both already
  present in the system (Ollama daemon, Swift helper pattern).

## Out of scope

- Local `synthesis` tier (quality floor; revisit if local models improve).
- Private Cloud Compute / third-party backends of the Foundation Models
  framework (violates G7).
- Per-automation tier overrides (the dormant `automations.<name>.model`
  config field stays display-only).
- Local embeddings changes, budget-guard changes, dashboard SPA changes.
- Exact token counting for local models (heuristic estimates suffice; nothing
  bills by token).
