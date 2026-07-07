# R5.1 — `axon eval` harness — design spec

> **Status:** Approved (2026-07-07). Roadmap 1.2 slice R5, sub-slice #1 of 3.
> Anchors: `docs/15-roadmap-1.2.md` §R5; ADR-015 (`docs/02-architecture.md:224`);
> ADR-029 (this slice). Requirements: FR-140, FR-141.

## Why

R5 turns ADR-015's static gate — *"`synthesis` always stays on Claude; local is
classify/routine only"* — into a **promotion procedure grounded in evidence, not
vibes**: a local model earns a tier only when it demonstrably passes on this
machine. That procedure has three separable pieces (decomposed per the
brainstorming discipline; each ships on its own):

1. **The eval harness + `axon eval`** — *this slice.* Golden sets from AXON's own
   task families, hybrid-graded pass/fail, runnable against any `(provider,
   model)` pair, ledgered through the chokepoint. Independently useful: you can
   benchmark a local model the day it lands.
2. **Eval-gated promotion** *(follow-on #2)* — config accepts `models.routine:
   ollama:<m>` only when the harness passes thresholds; `doctor` reports status;
   evals re-run on model/version change. Needs #1's persisted results.
3. **Runtime cascade-with-verification** *(follow-on #3)* — promoted tier does
   local → local verifier → escalate to Claude on low confidence.

This spec covers **#1 only**. #2 and #3 are explicitly out of scope; where a
decision here constrains them it says so.

## Cardinal-rule alignment

- **Rule 1 (chokepoint).** Every eval call — both the *target* model call and the
  Claude *judge* call — is a generative call and therefore runs through
  `tokens.Manager.Run` (ledgered, ADR-015 invariant). The harness never imports
  `agent` directly; it depends on a small consumer-defined interface satisfied by
  `*tokens.Manager`.
- **Rule 2 (wikilink-safe).** The harness makes **no vault mutation** — it reads
  embedded fixtures and prints a scorecard. Nothing is written to the vault.

## Measurement integrity (the load-bearing design point)

If an eval call ran with the default `local_fallback: claude`, a local model that
errored or produced schema-invalid output would **fall forward to Claude**
(ADR-015) — and the eval would silently measure Claude, not the local model.
Therefore eval target calls run with **fail-fast semantics** (`local_fallback:
fail` for the eval's own manager), and the harness additionally inspects
`resp.Model`: a case is scored **passed-local / escalated / failed** so an
accidental Claude answer is visible, never counted as a local pass. Eval calls
are still ledgered; only the fallback disposition differs from the automation
path. (The runtime cascade of #3 is the opposite posture — deliberate escalation
— which is why it is a separate slice.)

## Architecture

New leaf package `internal/eval` + a thin `cmd/axon/eval_cmd.go`. Dependency
edges: `eval → tokens` (for `AgentCall`/result types) and `eval → config`;
`cmd/axon → eval`. `eval` does **not** import `agent` (rule 1) and does **not**
import `automations` (cases are self-contained, so the harness never reaches into
automation internals). Acyclic.

```
internal/eval/
  case.go        # Case, Grade, Family; LoadCases (embed + YAML parse)
  golden/        # //go:embed golden — the versioned fixtures
    classify/*.yaml
    routine/*.yaml
  grade.go       # gradeClassify (deterministic), gradeRoutine (must_include + judge)
  run.go         # Chokepoint iface; Run(ctx, cp, cases, opts) → Report; scorecard types
  *_test.go
cmd/axon/eval_cmd.go   # newEvalCmd(gf) — wires *tokens.Manager via the deps builder
```

### Data model

```go
// Family is the task family a case exercises (== the tier it would promote).
type Family string // "classify" | "routine"

// Case is one self-contained golden example, loaded from an embedded YAML file.
type Case struct {
    Name   string `yaml:"name"`
    Family Family `yaml:"family"`
    System string `yaml:"system"` // optional system prompt
    Prompt string `yaml:"prompt"` // the full task input (context inline)
    Grade  Grade  `yaml:"grade"`
}

// Grade carries the pass/fail criteria. classify uses ExpectJSON/ExpectText
// (deterministic); routine uses MustInclude (+ optional Rubric for the judge).
type Grade struct {
    ExpectJSON  json.RawMessage `yaml:"expect_json"`  // classify: semantic JSON equality
    ExpectText  string          `yaml:"expect_text"`  // classify: normalized text equality
    MustInclude []string        `yaml:"must_include"` // routine: anchor substrings that must survive
    Rubric      string          `yaml:"rubric"`       // routine: Claude-judge criteria; "" ⇒ deterministic-only
}

// LoadCases parses every embedded golden/<family>/*.yaml, optionally filtered to
// one family. Fixtures are validated at load (known family, non-empty prompt,
// exactly one grading mode appropriate to the family) so a malformed fixture
// fails loudly rather than silently scoring.
func LoadCases(family string) ([]Case, error)
```

### Grading

```go
// Verdict is the outcome of grading one case.
type Verdict struct {
    Pass      bool
    Escalated bool   // resp.Model != target — the answer came from Claude fall-forward
    Reason    string // human-readable why (mismatch detail / judge reason / transport error)
}
```

- **classify (deterministic).** `ExpectJSON` → unmarshal both sides, compare
  semantically (key/value equality, order-insensitive). Else `ExpectText` →
  compare after trimming/space-collapsing. No model judge.
- **routine (hybrid).** First the `MustInclude` gate: every listed substring must
  appear in the candidate. Then, if `Rubric` is set, one **Claude-judge call**
  through the chokepoint: system pins the judge to output `{"pass":bool,
  "reason":string}` (guarded by `ValidateOutput` — the judge is Claude, so this
  is reliable); the prompt is the rubric + the candidate. The case passes iff
  `MustInclude` holds **and** the judge returns `pass:true`. A judge with no
  `Rubric` degrades to the deterministic `MustInclude` gate alone.

CI runs the judge against `agent.Fake` (canned `pass:true`), so CI verifies
harness *plumbing*; real quality grading happens locally against Ollama.

### Runner & the chokepoint seam

```go
// Chokepoint is the minimal surface the runner needs — satisfied by
// *tokens.Manager. Defined at the consumer so the runner is unit-testable with a
// fake and never imports internal/agent (cardinal rule 1).
type Chokepoint interface {
    Run(ctx context.Context, call tokens.AgentCall) (tokens.RunResult, error)
}

type Options struct {
    Model  string // override: eval every case against this ref; "" ⇒ per-family configured tier
    Family string // "classify" | "routine" | "all"
}

// Run evaluates cases and returns a Report. For each case it issues one target
// call (family tier model or Options.Model; fail-fast fallback) through cp,
// records escalation via resp.Model, grades, and — for routine cases with a
// Rubric — issues one judge call through cp. Never mutates the vault.
func Run(ctx context.Context, cp Chokepoint, cases []Case, opts Options) (Report, error)

type Report struct {
    Families []FamilyReport
}
type FamilyReport struct {
    Family            Family
    Model             string
    Total, Passed     int
    Escalated, Failed int
    Cases             []CaseResult
}
type CaseResult struct {
    Name    string
    Verdict Verdict
}
```

The exact `tokens.RunResult` field names (text, model, usage) are confirmed
against `internal/tokens/manager.go` at implementation time; the interface adapts
to whatever `Manager.Run` already returns rather than changing the manager.

### CLI

`axon eval [--model <ref>] [--family classify|routine|all] [--json] [--min-pass <pct>]`

- **no `--model`** → each family's cases run against its currently-configured
  tier (`models.classify` / `models.routine`): *"is my configured local model
  good enough?"*
- **`--model ollama:qwen2.5`** → every case runs against that ref: *vet a
  candidate before promoting it.*
- Default output: a per-family scorecard (model, pass N/total, escalated,
  failed) + per-case pass/fail lines. `--json` emits the `Report` for machines.
- `--min-pass <pct>` sets the process exit code (non-zero if any family's pass
  rate is below the threshold) so CI can gate — this is an **exit code only**,
  not config promotion (that is slice #2).

`eval_cmd.go` builds a `*tokens.Manager` **with the local router** (via the
existing `deps` service builder that `start`/`ask` already use — `NewWithRouter`,
reaching the Ollama adapter), cloning the resolved config with `local_fallback:
fail` for measurement integrity, then delegates to `eval.Run`. The command file
stays thin; all logic lives in `internal/eval`.

## Out of scope (constrains #2/#3, not built here)

- No DB persistence (`eval_runs` table), no `doctor` eval-status check, no config
  promotion-gating in `validateLocalRouting` — all deferred to #2, which needs
  the persisted results.
- No runtime local→verifier→Claude cascade — that is #3.
- `synthesis` family: **measurable** (you may add synthesis fixtures and run
  `axon eval --family synthesis --model <claude>` to baseline), but never
  promoted. v1 ships `classify` + `routine` fixtures.

## Testing

- **Grading unit tests** (table-driven, no I/O): `expect_json` semantic equality
  (incl. key-order and whitespace independence), `expect_text` normalization,
  `must_include` gate, judge-result JSON parsing (`{pass,reason}`, malformed →
  fail-not-panic).
- **Fixture load/embed**: every embedded `golden/**/*.yaml` parses and validates;
  a deliberately malformed fixture in a test-local set fails loudly.
- **Runner with a fake `Chokepoint`**: target returns canned text, judge returns
  canned `pass:true` → scorecard totals correct; a fake whose `resp.Model`
  differs from the target ref is scored **escalated**, not passed; a transport
  error is scored **failed** with the error reason.
- **CLI smoke**: `newEvalCmd` against a fake-backed manager produces a scorecard
  and honors `--json` / `--min-pass` exit codes.

## Requirements delivered

- **FR-140** — Eval harness + `axon eval` + in-repo golden sets (classify +
  routine families), runnable against any `(provider, model)` pair, every eval
  call ledgered through the chokepoint, fail-fast measurement with escalation
  visibility.
- **FR-141** — Hybrid grading: deterministic (JSON/text equality) for classify;
  `must_include` + Claude LLM-as-judge rubric for routine prose; CI grades
  against `agent.Fake`.

ADR-029 records the methodology decision (in-repo golden sets, hybrid grading,
ledgered fail-fast eval calls; promotion-gating + cascade noted as follow-on).
