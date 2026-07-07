# R5.2 — eval-gated local-model promotion — design spec

> **Status:** Approved (2026-07-07). Roadmap 1.2 slice R5, sub-slice #2 of 3.
> Anchors: `docs/15-roadmap-1.2.md` §R5; ADR-015 (`docs/02-architecture.md:224`);
> ADR-029 (R5.1, the eval harness); ADR-030 (this slice).
> Requirements: FR-142, FR-143. Builds on R5.1 (`internal/eval`, `axon eval`).

## Why

R5.1 shipped the eval harness: golden sets, hybrid grading, `axon eval` runnable
against any `(provider, model)` pair, ledgered fail-fast. But nothing yet *acts*
on a passing result — a local model that aces the harness still isn't trusted by
the runtime, and a local model that was never evaluated is trusted blindly. R5.2
closes that loop: **a local model earns a tier only when it has demonstrably
passed the harness on this machine, and loses it the moment its version drifts.**

This is ADR-015's static gate becoming evidence-based, per the roadmap: *"the
`synthesis` gate becomes a promotion procedure; local wins a tier only when the
harness passes thresholds on this machine; `doctor` reports eval status; silent
regressions caught by re-running evals on model/version change."*

R5 decomposes into three slices (brainstorming discipline; each ships alone):

1. **R5.1 — eval harness + `axon eval`** *(shipped, ADR-029).*
2. **R5.2 — eval-gated promotion** *(this slice).* Persist eval results; a
   runtime **admission gate** in the chokepoint routes a local tier to Claude
   unless a passing eval exists; `doctor` reports vetting + drift; an optional
   automation re-runs evals on version drift.
3. **R5.3 — runtime cascade-with-verification** *(follow-on #3).* For an
   *already-admitted* local tier, run local → local verifier → escalate to
   Claude on low confidence. Per-call quality assurance, not admission.

The R5.2/R5.3 boundary is load-bearing: **R5.2 decides whether a local model may
serve a tier at all** (binary, from persisted evals); **R5.3 decides, per call,
whether a served answer is good enough** (real-time verification). This spec
covers R5.2 only.

## Decisions (from brainstorming, 2026-07-07)

- **Runtime gate in the manager**, not config-time or doctor-only. Config
  validation is pure and synchronous (runs before the DB opens — it *cannot*
  consult eval results), and the determinism principle wants admission enforced
  in code on every path, not by asking nicely. The chokepoint already resolves
  tiers and owns the Claude-fallback machinery, so it is the natural home.
- **Cheap DB gate + out-of-band drift.** The hot-path gate is a single indexed
  SQLite read — *no Ollama call per request*. Version-drift (digest change) is
  detected out-of-band by `doctor` and the optional re-eval automation, which
  refresh the persisted rows. A short drift window is acceptable; R5.3's per-call
  verification is the real-time net.
- **Single global threshold** `models.eval_min_pass` (percent, 0–100), **default
  0 = opt-in.** An existing install with a local tier keeps routing local exactly
  as today until the user sets a threshold; `axon init` scaffolds `80` for new
  installs; `doctor` nudges. No surprise Claude spend on upgrade.
- **Include the drift-triggered re-eval automation**, off by default (S8).

## Cardinal-rule alignment

- **Rule 1 (chokepoint).** The gate only *redirects which tier serves a call*
  (local → its Claude fallback); every resulting call still runs through
  `Manager.Run` and is ledgered. The re-eval automation spends tokens only
  through R5.1's `eval.Run`, which already routes every target and judge call
  through the chokepoint. No new path reaches Claude.
- **Rule 2 (wikilink-safe).** No vault mutation anywhere: the gate reads the DB,
  `doctor` reads config + DB + Ollama, `axon eval` prints a scorecard and writes
  the `eval_runs` table. `reindex` does not touch `eval_runs`.

## Data model — `eval_runs` (migration `0006_eval_runs.sql`)

```sql
CREATE TABLE eval_runs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    family    TEXT    NOT NULL,            -- "classify" | "routine" | "synthesis"
    model_ref TEXT    NOT NULL,            -- the tier ref, e.g. "ollama:qwen3:8b"
    digest    TEXT    NOT NULL DEFAULT '', -- ollama model digest at eval time ("" for apple/claude)
    passed    INTEGER NOT NULL,
    total     INTEGER NOT NULL,
    pass_pct  INTEGER NOT NULL,            -- passed*100/total, precomputed for the gate query
    ran_at    TEXT    NOT NULL             -- RFC3339
);
CREATE INDEX idx_eval_runs_lookup ON eval_runs (family, model_ref, ran_at DESC);
```

`eval_runs` is a **derived/operational** table in the sense of `automation_state`
(migration `0003`): it records measurements taken on *this* machine. It is not
vault-knowledge — there is nothing in the vault it could be rebuilt from — so it
is correctly DB-only and **exempt from S9's "vault rebuilds the DB"**; `reindex`
leaves it untouched. (This exemption is stated in ADR-030 so the S9 invariant is
not read as violated.)

Repository (`internal/db/eval.go`):

```go
type EvalRun struct {
    Family   string
    ModelRef string
    Digest   string
    Passed   int
    Total    int
    PassPct  int
    RanAt    time.Time
}

// RecordEvalRun inserts one row.
func RecordEvalRun(ctx context.Context, ex Execer, r EvalRun) error

// LatestEvalRun returns the most recent row for (family, ref) and ok=false when
// none exists. The gate uses PassPct; doctor also uses Digest/RanAt.
func LatestEvalRun(ctx context.Context, q Queryer, family, modelRef string) (EvalRun, bool, error)
```

(`Execer`/`Queryer` are the existing consumer-defined DB interfaces in
`internal/db`.)

## The runtime gate (`internal/tokens`)

Two new `tokens.Config` fields, both mapped from config by `managerConfig`:

```go
// EvalMinPass is models.eval_min_pass (percent, 0–100). 0 disables the gate.
EvalMinPass int
// PromotionGateOff disables the gate for this manager regardless of EvalMinPass.
// Set ONLY by the eval harness's own manager (R5.1 evalManager) so `axon eval`
// always measures the real local model — the chicken-and-egg guard.
PromotionGateOff bool
```

The gate lives in `Authorize`, right after `ref := m.resolveRef(call.ModelKey)`:

```
if gate active (EvalMinPass > 0 && !PromotionGateOff)
   && ref.Provider is local (ollama|apple)
   && call.ModelKey is a promotable tier (classify|routine):
       row, ok := LatestEvalRun(family=call.ModelKey, ref=resolveModel(call.ModelKey))
       if !ok || row.PassPct < EvalMinPass:
           key := m.fallbackClaudeKey(call.ModelKey)   // reuse existing machinery
           ref  = m.resolveRef(key)                    // now a Claude tier
           auth.Model, auth.Provider = ref.Model, ref.Provider
           auth.Reason = "local tier <ref> not vetted (<why>); routed to <key>"
           emit token.unvetted_local (LevelWarn)
```

Notes:
- The lookup key is the **family name** (`call.ModelKey` when it is
  `classify`/`routine`) and the resolved concrete ref string
  (`resolveModel(call.ModelKey)`, e.g. `ollama:qwen3:8b`) — the same
  `(family, model_ref)` pair `axon eval` persists.
- A concrete-ref `ModelKey` (not a family alias) is **not** gated: that path is
  a deliberate override (e.g. the eval harness targeting `--model`), never an
  automation's tier selection.
- `synthesis` is always Claude (config-validated), so it is never gated.
- The gate is a pure indexed SQLite read; it makes **no** Ollama/network call.
- After retargeting, the call proceeds through the normal Claude budget path
  (defer/deny/downgrade all still apply). The unvetted-local event is emitted
  once, before budget evaluation, so the redirect is always observable.

`fallbackClaudeKey` already returns the first Claude tier at/above the key and
terminates at `synthesis`, so an unvetted `classify` or `routine` always has a
Claude destination.

## Config (`internal/config`)

```go
// EvalMinPass gates local-tier promotion: a local classify/routine model serves
// its tier only when its latest `axon eval` pass rate is >= this percent.
// 0 (default) disables the gate — local tiers route as configured. New installs
// scaffold 80 (see axon init); doctor nudges existing installs.
EvalMinPass int `yaml:"eval_min_pass,omitempty" validate:"omitempty,min=0,max=100"`
```

Added to `ModelsConfig`. `validateLocalRouting` gains one rule: reject
`eval_min_pass` outside 0–100 (the struct tag covers it; the cross-field
validator adds a friendly message). `managerConfig` copies it into
`tokens.Config.EvalMinPass`. `axon init`'s config scaffold sets `eval_min_pass:
80`; existing configs without the key default to 0 (opt-in).

## Persistence wiring (`cmd/axon`, R5.1 `eval` untouched-as-a-package)

After `eval.Run` returns in `eval_cmd.go`, persist one `eval_runs` row per
`FamilyReport`:

- `model_ref` = the family's target ref (`Options.Model` or the resolved tier);
- `digest` = for an ollama ref, one `ollama /api/show` lookup at persist time
  (eval is not hot-path; a fetch failure stores `""` and is not fatal); `""` for
  apple/claude;
- `passed`/`total`/`pass_pct` from the `FamilyReport`; `ran_at` = now.

`internal/eval` stays pure (no DB, no agent import): the digest fetch and row
write live in `cmd/axon`, which already reaches the Ollama host and the DB. A new
`--no-save` flag skips persistence for throwaway runs; persistence is on by
default so vetting a `--model` candidate creates the row that later admits it.

## Doctor (`internal/core`)

Extend `localModelsCheck` for each gated local tier:

- gate disabled (`eval_min_pass == 0`) but a local tier is set → **WARN**:
  "local tier <ref> is ungated — set models.eval_min_pass to require evals";
- gated, no `eval_runs` row → **WARN**: "not vetted — run `axon eval --family
  <f> --model <ref>`";
- gated, latest `pass_pct < threshold` → **WARN**: "<pct>% < <threshold>% — tier
  routes to Claude until it passes";
- gated, passing, but current ollama digest ≠ stored `digest` → **WARN**:
  "model changed since eval (<short-old> → <short-new>) — re-run `axon eval`";
- gated, passing, digest matches → **OK**: "vetted <pct>% (<ago>)".

Doctor is the only place a live Ollama digest is fetched for comparison (it
already reachable-checks Ollama here); a fetch failure degrades to the non-drift
OK/WARN message rather than erroring.

## Automation — `eval-drift` (`internal/automations`, off by default)

A `routine`-tier automation (toggleable, default off — S8):

- For each gated local tier, fetch the current ollama digest; if it differs from
  the latest `eval_runs.digest` (or no row exists), run `eval.Run` for that
  family+ref through the chokepoint and `RecordEvalRun` the fresh result.
- Content-gated on digest change (token frugality: no digest change → no work).
- With the automation off, drift is still surfaced by `doctor` and fixed by a
  manual `axon eval` — the feature is fully usable all-off.

## Out of scope (→ R5.3)

- No per-call local→verifier→Claude cascade. R5.2's gate is admission-only; once
  a tier is admitted, calls route straight to the local model (today's behavior).
  Per-call verification of a *served* answer is R5.3.
- No change to R5.1's grading, fixtures, or `eval.Run` signature.

## Testing

- **Repo** (`internal/db`): `RecordEvalRun`/`LatestEvalRun` round-trip; latest-row
  selection across multiple rows for the same `(family, ref)`.
- **Gate** (`internal/tokens`, fake router + in-memory DB): admitted tier
  (passing row ≥ threshold) routes local; missing row retargets to Claude and
  emits `token.unvetted_local`; below-threshold row retargets; `EvalMinPass == 0`
  bypasses (routes local); `PromotionGateOff == true` bypasses even with a
  threshold set and no row (the eval-mode guard); a concrete-ref `ModelKey` is
  never gated; `synthesis` never gated.
- **Config**: `eval_min_pass` out of 0–100 rejected; default 0; round-trips.
- **Persistence** (`cmd/axon`): after a fake-backed `eval.Run`, one row per family
  is written with expected `pass_pct`; `--no-save` writes nothing.
- **Doctor**: table-driven over the five states (ungated / not-vetted /
  below-threshold / drift / vetted-ok) with a fake digest source.
- **Automation**: digest-drift detected → `eval.Run` invoked and a fresh row
  recorded (fake chokepoint + fake digest); no drift → no work; off → no work.

## Requirements delivered

- **FR-142** — Persisted `eval_runs`; runtime promotion gate in the chokepoint
  (local classify/routine tier served only when a passing eval ≥
  `models.eval_min_pass` exists, else deterministically routed to Claude and
  ledgered); `eval_min_pass` config (default 0, opt-in); `axon eval` persists
  results; eval-mode gate bypass.
- **FR-143** — `doctor` reports per-tier vetting status and version drift; an
  optional `eval-drift` automation re-runs evals when a gated local model's
  digest changes.

ADR-030 records the methodology decision: runtime admission gate in the
chokepoint; `eval_runs` as a DB-only operational table (S9-exempt); cheap
hot-path read with out-of-band drift detection; opt-in default threshold;
per-call verification cascade deferred to R5.3.
