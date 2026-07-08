# R5.3 — runtime cascade-with-verification — design spec

> **Status:** Approved (2026-07-07). Roadmap 1.2 slice R5, sub-slice #3 of 3.
> Anchors: `docs/15-roadmap-1.2.md` §R5; ADR-015 (`docs/02-architecture.md:224`);
> ADR-029 (R5.1 eval harness); ADR-030 (R5.2 admission gate); ADR-031 (this slice).
> Requirements: FR-144, FR-145. Builds on the local execution path R5.1/R5.2 left.

## Why

R5.1 gave us an eval harness; R5.2 turned a *persisted* passing eval into an
**admission gate** — a local `classify`/`routine` tier serves a call only when it
has demonstrably passed on this machine, else the chokepoint retargets to Claude.
But admission is binary and out-of-band: once a local tier is admitted, every call
routes straight to it, and a well-formed-but-*wrong* answer is trusted blindly.
Today's `runLocal` (`internal/tokens/manager.go`) only escalates to Claude when a
local answer **errors or fails schema validation** (FR-79) — never when it is
merely low-quality.

R5.3 closes the loop the roadmap asked for: *"a cascade with verification for
promoted tiers — local attempt → cheap local verifier → escalate to Claude on low
confidence, all ledgered."* This is **per-call quality assurance** for an
already-admitted tier, the real-time net under R5.2's out-of-band admission.

The R5.2/R5.3 boundary is load-bearing and unchanged from the R5.2 spec:
**R5.2 decides whether a local model may serve a tier at all** (binary, from
persisted evals); **R5.3 decides, per call, whether a served answer is good
enough** (real-time verification). This spec covers R5.3 only, the last R5 slice.

## Decisions (from brainstorming, 2026-07-07)

- **Verifier = a local LLM judge call.** After a successful local answer, a cheap
  local model scores the `(task, answer)` pair 0–10 (faithful + correct). Reuses
  R5.1's judge *shape* but runs **local**, not Claude — the whole point is to
  spend Claude only when the local answer is doubted. Deterministic-only checks
  were rejected (most are already `ValidateOutput`); the rerank scorer was
  rejected (it scores *relevance*, a weaker signal than answer correctness).
- **Scope = local `routine` only, default off.** `synthesis` is always Claude
  (never local); `classify` is deterministically graded by `ValidateOutput` and
  cheap to re-run, so verifying it buys little for its high volume. `routine` is
  user-facing prose where a wrong answer costs the most and Claude escalation
  earns its spend. Config-gated, **default off** (S8 — all-off still useful).
- **Judge model = a dedicated `models.verify` key.** A distinct, typically
  smaller local model avoids self-grading bias (a model judges its own output
  poorly) and lets the judge be cheaper than the answerer. Must be a local
  `ollama:<model>` ref — a Claude judge would defeat "cheap local verifier", and
  the Apple model's small context can't hold task + answer.
- **The judge and the escalation are both ledgered through the chokepoint**
  (the R5 gate says "all ledgered"). This is the ADR-031 methodology point and
  the deliberate contrast with rerank (ADR-027, an un-ledgered retrieval
  primitive): the judge *gates spend*, so it is observable like every other model
  call, not a silent side-channel.

## Cardinal-rule alignment

- **Rule 1 (chokepoint).** The judge call and the Claude escalation both run and
  ledger inside `tokens.Manager`. No new path reaches Claude — escalation reuses
  the existing `fallbackClaudeKey` + `Run` budget path. The judge is a local
  (budget-exempt) call, ledgered as `<op>:verify`.
- **Rule 2 (wikilink-safe).** No vault mutation anywhere: the cascade only
  chooses which model's answer the caller receives.

## Config (`internal/config`, `ModelsConfig`)

Two new fields, beside `eval_min_pass`:

```go
// Verify, set to "ollama:<model>", enables per-call verification of local
// routine answers (R5.3/FR-144): after a successful local routine response a
// cheap local judge scores it 0–10; a score below VerifyMinScore escalates the
// call to Claude. "" or "off" disables (default). Only the routine tier is
// verified — synthesis is always Claude, classify is deterministically validated.
Verify string `yaml:"verify,omitempty"`
// VerifyMinScore is the 0–10 confidence floor below which a verified local
// routine answer escalates to Claude. Default 6. Ignored when verify is off.
VerifyMinScore int `yaml:"verify_min_score,omitempty" validate:"omitempty,min=0,max=10"`
```

Helpers on `ModelsConfig`:

```go
// VerifyMode returns the configured verifier ref, or "off" when unset/"off".
func (m ModelsConfig) VerifyMode() string
// VerifyMinScoreOr returns the escalation floor, defaulting to 6.
func (m ModelsConfig) VerifyMinScoreOr() int
```

`validateLocalRouting` (in `internal/config/models.go`) gains one rule: when
`verify` is non-empty and not `"off"`, it must parse as a local `ollama:<model>`
ref with a non-empty model — reject Claude, `apple`, and `ollama:` (empty). The
`verify_min_score` range (0–10) is covered by the struct tag. `managerConfig`
does not need to copy these separately: the manager already receives the whole
`config.ModelsConfig` as `Config.Models`, so `VerifyMode()`/`VerifyMinScoreOr()`
are read directly. Default **off**; `axon init` does **not** scaffold it on (it
requires a chosen judge model) — it is documented in the config reference and
nudged by `doctor` only when partially configured.

## The cascade (`internal/tokens`)

### Trigger

A pure predicate, evaluated on a **successful** local answer inside `runLocal`:

```go
// verifyActive reports whether a successful local answer should be judged before
// it is trusted (R5.3): the routine family alias, an ollama provider, verify on.
func (m *manager) verifyActive(call AgentCall, auth Authorization) bool {
    return call.ModelKey == "routine" &&
        auth.Provider == config.ProviderOllama &&
        m.cfg.Models.VerifyMode() != "off"
}
```

`call.ModelKey` is still `"routine"` here (Authorize mutates only its own
by-value copy; `Run` passes the original `call` to `runLocal`), so the trigger is
the family alias, never a concrete ref. `runLocal` is only reached when the tier
resolved local (an eval-gate retarget to Claude never enters `runLocal`), so
reaching here means routine was admitted local.

### `runJudge` — the local judge call (loop-safe, best-effort, ledgered)

```go
// runJudge issues one local judge call scoring answer 0–10 for the task in call.
// It uses the configured verify model (a concrete ollama ref, so it is never
// itself gated or verified — no recursion). Ledgered as "<op>:verify"
// (budget-exempt). Best-effort: any transport/parse failure returns ok=false, and
// the caller keeps the local answer. It NEVER falls forward to Claude — a broken
// judge must not spend the Claude quota. (R5.3/FR-144, ADR-031.)
func (m *manager) runJudge(ctx context.Context, call AgentCall, answer string) (score int, ok bool)
```

Implementation notes:
- Resolve the `ollama` adapter from `VerifyMode()`'s ref; on resolve error →
  `(0, false)`.
- Build the request via `buildVerifyPrompt` (below), **redacted** through the
  same `applyRedaction` path as every other call, `Operation: call.Operation +
  ":verify"`, `RunID` preserved.
- One attempt (no retry — best-effort; the answer already exists). On adapter
  error → `recordFailure` under the `:verify` label, return `(0, false)`.
- On success, ledger the judge via `m.record` with a synthetic
  `Authorization{Decision: Proceed, Model: ref.Model, Provider: ollama}` (so the
  ledger row is `ollama:<verify-model>`, budget-exempt); fill missing usage from
  the heuristic estimator exactly as `runLocal` does; then
  `parseVerifyScore(resp.Text)`.

The judge deliberately does **not** route through `m.Run`: `Run`'s local path
carries `fallForward`-to-Claude, which we must not inherit for a grading call.

### `verifyAndMaybeEscalate` — the decision

```go
// verifyAndMaybeEscalate judges a successful local routine answer and, below the
// floor, escalates the call to Claude through the normal budget path. Both the
// local answer (already ledgered by the caller) and the judge call are ledgered
// before any escalation. Degrades to the local answer if the judge is
// inconclusive OR if Claude is unavailable (budget deny/defer) — S8.
func (m *manager) verifyAndMaybeEscalate(ctx context.Context, call AgentCall,
    auth Authorization, local AgentResult) (AgentResult, error) {

    score, ok := m.runJudge(ctx, call, local.Text)
    floor := m.cfg.Models.VerifyMinScoreOr()
    if !ok || score >= floor {
        m.emit(events.LevelInfo, "token.verify_pass", call.Operation, auth,
            map[string]any{"score": score, "scored": ok, "floor": floor})
        return local, nil
    }
    esc := call
    esc.ModelKey = m.fallbackClaudeKey("routine")
    m.emit(events.LevelWarn, "token.verify_escalate", call.Operation, auth,
        map[string]any{"score": score, "floor": floor, "escalate_to": esc.ModelKey})
    res, err := m.Run(ctx, esc)
    if err != nil {
        // Claude denied/deferred by budget: the low-scored local answer is the
        // baseline this tier already provides — degrade rather than error (S8).
        m.emit(events.LevelWarn, "token.verify_escalate_failed", call.Operation, auth,
            map[string]any{"score": score, "error": err.Error()})
        return local, nil
    }
    return res, nil
}
```

### Wiring into `runLocal`

In the existing success branch, after the local answer is recorded
(`res.LedgerID = ledgerID`):

```go
res.LedgerID = ledgerID
if m.verifyActive(call, auth) {
    return m.verifyAndMaybeEscalate(ctx, call, auth, res)
}
return res, nil
```

The local answer is always ledgered (it ran and consumed local compute) before
any escalation, so the ledger honestly shows the full cascade: `<op>` (local
answer) + `<op>:verify` (local judge) + `<op>` at a Claude tier (escalation, when
it happens). On escalation the caller pays local answer + local judge + Claude —
the intended cost of buying a second opinion.

### Prompt & parse (`internal/tokens/verify.go`, pure, unit-tested)

Kept in `package tokens` (one consumer — the manager — so no new leaf package,
unlike rerank which `search` consumes):

```go
// buildVerifyPrompt renders the judge's system + user prompt from the original
// task (system + messages) and the candidate answer.
func buildVerifyPrompt(system string, msgs []Message, answer string) (sys, prompt string)
// parseVerifyScore extracts the first integer 0–10 from the judge's reply,
// clamped to [0,10]; ok is false when none is found. Mirrors rerank.parseScore.
func parseVerifyScore(text string) (score int, ok bool)
```

`buildVerifyPrompt` system: a strict evaluator instructed to reply with **only** a
single integer 0–10 where 10 = fully correct and faithful to the task, 0 = wrong
or unfaithful; user prompt: the task (system + joined messages) then the candidate
answer, then `SCORE (0-10):`. `parseVerifyScore` takes the first `\b(10|[0-9])\b`
match, clamps, `ok=false` on no match (→ inconclusive → keep local answer).

## Doctor (`internal/core`)

A new `verifyCheck` (mirroring `rerankCheck`), reported once:

- verify off → silent (no line);
- verify on but the `routine` tier is **not** local (routine resolves to Claude)
  → **WARN**: "models.verify set but routine tier is Claude — verification never
  triggers";
- verify on, routine local, verify model **not** present in Ollama's tag list →
  **WARN**: "verify model <m> not pulled — run `ollama pull <m>`";
- verify on, routine local, model present → **OK**: "verify ollama:<m> ready,
  floor <n>/10".

Reachability is best-effort (a fetch failure degrades to a non-blocking WARN),
exactly like `rerankCheck`.

## Consumers

None change. Every `routine`-tier automation and MCP tool inherits the cascade
transparently because it already routes through the chokepoint — that is the
point. **No new automation and no new MCP tool**, so the registry/tool
count-assertions are untouched this slice.

## Out of scope

- `classify` and `synthesis` verification (scope decision above).
- Multi-judge / majority voting — one judge call; YAGNI for v1.
- Persisting verification outcomes to a table or feeding them back into R5.2's
  `eval_runs` — the cascade is stateless per call; the ledger + events are the
  record.
- Changing R5.1/R5.2 signatures, `eval_runs`, or the admission gate.

## Testing

- **Config**: `VerifyMode()` default `off` / returns ref; `VerifyMinScoreOr()`
  default 6 / returns set value; validation rejects `verify_min_score` outside
  0–10 and a non-local / empty-model `verify` (claude, `apple`, `ollama:`);
  round-trips.
- **Prompt/parse** (pure): `parseVerifyScore` — `"8"`→8, `"score: 3"`→3, `"10"`→10,
  `"12"`→10 (clamp), `"abc"`/`""`→`ok=false`; `buildVerifyPrompt` includes both the
  task and the answer.
- **Cascade** (fake router: an ollama adapter scripted to return the answer then
  the judge score; a claude adapter for escalation):
  - routine local answer + judge ≥ floor → keeps local; ledger has `<op>` and
    `<op>:verify`; **no** Claude call; `token.verify_pass` emitted;
  - judge < floor → escalates; result `Model` is Claude; ledger has local answer +
    `:verify` + a Claude row; `token.verify_escalate` emitted;
  - judge adapter errors / unparseable score → keeps local (best-effort), no
    escalation, no Claude call;
  - the judge call **never** hits the claude adapter (loop/spend guard) and is
    never itself verified;
  - escalation denied by budget (zero window) → returns the local answer,
    `token.verify_escalate_failed` emitted;
  - verify **off** → no judge call (today's behaviour, byte-for-byte);
  - a `classify` local answer with verify on → **not** verified (scope).
- **Doctor**: table over off / routine-not-local / model-missing / ready with a
  fake Ollama tag source.

## Requirements delivered

- **FR-144** — Per-call verification cascade for the local `routine` tier: a
  successful local answer is scored 0–10 by a cheap local judge (`models.verify`);
  below `models.verify_min_score` (default 6) the call escalates to Claude via
  `fallbackClaudeKey` through the normal budget path. The local answer, the local
  judge (`<op>:verify`), and any Claude escalation are all ledgered; the judge is
  loop-safe (concrete ref) and never falls forward to Claude; escalation degrades
  to the local answer when the judge is inconclusive or Claude is budget-blocked.
  Config `models.verify` + `models.verify_min_score` with validation; **default
  off** (S8).
- **FR-145** — `doctor` reports the verify subsystem: off (silent) /
  routine-tier-not-local / verify-model-not-pulled / ready.

ADR-031 records the methodology decision: per-call verification as a chokepoint
cascade (local answer → ledgered local judge → Claude escalation on low
confidence), the judge ledgered (contrast ADR-027's un-ledgered rerank primitive)
because it gates spend; routine-only scope; default off; graceful degrade to the
local answer.
