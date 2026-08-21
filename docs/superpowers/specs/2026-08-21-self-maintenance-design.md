# Self-maintenance — the daemon files its own fixes (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
an injected `SelfCheck` function plus shared report assembly, rather than the
full config on `RunCtx`; a check is proposal-worthy when it carries a non-empty
`Fix`; doctor-only in v1, run-failure streaks deferred). **FR-206, FR-207; no
new ADR, no migration.** Graduates `docs/20` **G1**.

**No migration.** Schema stays `0007`. Built-in automations go 25 → **26**.

## The idea

`axon doctor` already knows what is wrong *and* what to do about it: `Check`
separates `Detail` ("service unit PATH cannot resolve claude") from `Fix`
("`axon service reinstall`"). The dashboard's "Needs you" panel surfaces that,
and the Companion shells out to `doctor --json`. The missing step is AXON
filing the work where the owner already reviews work — the review queue
(ADR-020) — so a drifted install becomes an item beside link suggestions and
merge proposals rather than something you have to go looking for.

Two facts found by reading the code shape this design, and neither is in the
roadmap entry:

1. **Nothing in the daemon runs doctor.** `core.Doctor` has exactly one caller,
   `cmd/axon/doctor_cmd.go`. The daemon has never inspected its own
   installation. `RunCtx` carries a `config.Profile`, not the
   `*config.Config` that `core.Doctor` needs, so the seam *is* the work.
2. **The full report is assembled in the CLI, not in `core`.**
   `doctor_cmd.go` appends `update-available` and `recipes` to whatever
   `core.Doctor` returned. An automation calling `core.Doctor` directly would
   therefore propose from a **smaller report than the owner has ever seen** —
   the daemon and the CLI would quietly disagree about the system's health.

Fixing (2) is part of this slice, not a follow-up. A self-maintenance feature
whose view of "healthy" differs from the command the user runs is worse than no
feature.

**Doctor is network-free.** The `update-available` check reads a daily cache
written by `axon update` / `axon version --check` / the daemon's background
check; it never fetches. So running the report on a schedule introduces **no
new egress**, which is why this slice needs no ADR on that axis.

## FR-206 — One report, and a seam the daemon can reach it through

### Shared assembly

`core.Doctor` takes the CLI's extra checks instead of having them bolted on
afterwards:

```go
// Doctor runs the prerequisite checks. extras are caller-supplied checks
// appended in order — the CLI's update-availability and recipe checks, which
// core cannot compute (build version, and the automations package, which
// would be an import cycle).
func Doctor(cfg *config.Config, activeProfile string, extras ...Check) DoctorReport
```

`doctor_cmd.go` passes `updateAvailabilityCheck()` and
`automations.RecipesCheck(p)` through that parameter and stops appending. The
daemon passes the same two. **A test asserts both call sites produce the same
check *names* in the same order**, so the divergence cannot silently return.

The two checks stay outside `core` for real reasons, recorded here so nobody
"tidies" them in: `update-available` needs the build version, which is a
`main`-package linker variable; `recipes` lives in `automations`, and
`automations → core` already exists, so `core → automations` would be an import
cycle.

### The seam

`EngineDeps` and `RunCtx` gain exactly one field:

```go
// SelfCheck returns the same doctor report `axon doctor` prints. Injected by
// cmd/axon, where the full config and the build version live, so no
// automation gains access to other profiles' configuration. Nil when the
// caller did not wire it — the self-check automation then idles.
SelfCheck func(context.Context) []core.Check
```

Wired in `cmd/axon/deps.go:buildServices`, which already holds
`d.cfg *config.Config` and `d.name`. Nothing else changes: no automation sees
the multi-profile config, and the profile-isolation rule holds.

**A nil `SelfCheck` is an idle, not a crash.** Any caller that does not wire it
(a test, a future embedder) gets `"self-check unavailable"` as a not-changed
reason, exactly like a recipe whose input note is absent.

## FR-207 — `self-check`, the 26th automation, and the `fix` review kind

### What proposes

A check is proposal-worthy when **`Status != ok` and `Fix != ""`**.

The `Check` type already encodes actionability: `Detail` says what is wrong,
`Fix` says what to do. A check with no `Fix` has nothing to propose, and filing
it would train the owner to skim the queue. This rule needs no allow-list to
keep in sync — a newly added check participates the moment someone gives it a
`Fix`, which is also the moment it becomes worth acting on.

Both `warn` and `fail` qualify. Severity is not the question; having a
remediation is.

### The queue line

```
- [ ] fix service-path — "service unit PATH cannot resolve claude" → `axon service reinstall`
```

New `fix` kind in `internal/review`:

```go
fixRe = regexp.MustCompile("^fix ([a-z0-9-]+) — \"(.+)\" → `(.+)`$")
```

parsed as `Kind = "fix"`, `Note` = the check name, `Target` = the remediation
command. The detail group is matched but not stored on the `Item`: it exists so
the regex anchors on the full rendered line, and the owner reads it from the
line itself. Nothing in the accept path needs it. **Accept is acknowledge-only** — suffix `✓ noted`, no mutation of any
kind — the `recipe` kind's precedent (ADR-039). Dismiss archives as usual. Not
an Outcomes kind.

`Detail` and `Fix` are sanitized before rendering: double quotes become
single (the `propose` precedent in `recipe.go`), backticks are stripped from
`Fix` so it cannot terminate its own code span, and newlines are collapsed. A
check's text is developer-authored, not user input, but the queue line is
parsed back by regex and a stray quote would break the round-trip.

### Dedup and backoff — the difference between useful and noise

Proposal memory (`loadProposalMemory`/`saveProposalMemory`, state key
`self-check/proposed`) keyed on **`hashShort(check name + "\x00" + Fix)`**.

- A standing warning proposes **once**, not weekly. This is the whole reason
  the automation is safe to enable.
- If the `Fix` **text changes**, that is a genuinely different remediation and
  it proposes again — the key changes with it.
- The check name alone is not the key: two different remediations for
  `service-path` are two different pieces of work.

Cap **5 proposals per run**, so a badly broken install files a handful rather
than twenty, and the rest surface on the next run once those are resolved.
Pending items already in the queue are skipped (the `merge-proposals`
precedent: read `review.Load`, skip unchecked `fix` items). The pending skip
matches on **check name alone**, deliberately more aggressive than the memory
key: if a fix for `service-path` is already waiting for the owner, a second one
with different wording is noise, not news.

### The automation

`internal/automations/selfcheck.go`, registered as **`self-check`** — the 26th
built-in. Weekly, **disabled by default** in both seeds (S8).

- `Essential() = false`; zero model calls; the only write is the review-queue
  append.
- **`DetectChange`:** cursor = hash of the sorted proposal-worthy
  `(name, Fix)` pairs. An unchanged set skips with no work. Nil `SelfCheck` →
  not changed, reason `"self-check unavailable"`.
- **`Run`:** append ≤ 5 new `fix` lines under a dated
  `## Self-check (YYYY-MM-DD)` header, then save proposal memory. Nothing new
  → `"no new fixes to propose"`.
- **`DryRun`:** reports what it would file, writes nothing.

## Out of scope

Recorded so their absence reads as a decision:

- **Self-application of anything.** No running `axon service reinstall`, no
  `config set`, no restart, no update. G1 defers this explicitly and this spec
  keeps it deferred: an accept path that rewrites the owner's launchd unit or
  their config is a different risk class and deserves its own spec and its own
  ADR. v1 surfaces a copyable command and records the acknowledgement.
- **Run-failure streaks.** Deferred to a later slice. They need a new
  `db.ConsecutiveFailures` query, a threshold, and their own dedup semantics —
  and, unlike a doctor check, a failed automation carries no remediation, so
  the proposal would be a notification wearing a fix's shape. Worth doing;
  worth doing separately.
- **A dashboard panel.** "Needs you" already renders doctor state; this slice
  gives the *queue* a copy, not the dashboard a second one.
- **Proposing checks with no `Fix`**, and **re-nagging on an interval**.

## Why no ADR

No boundary moves. The sink is the existing review queue (`vault.Append`, the
same call every proposing automation makes); the accept path is
acknowledge-only, so no new mutation exists; there are zero model calls, so the
chokepoint is untouched; doctor is network-free, so no egress; and the schema
is unchanged. `SelfCheck` is a function field on an existing deps struct, not a
new component. Both cardinal rules hold by construction.

## Verification

**Unit — core:** `Doctor(cfg, profile, extras...)` appends extras in order and
unchanged; with no extras the report matches today's. A test asserting the
**CLI and daemon assembly produce identical check-name sequences** — the
regression that stops the divergence returning.

**Unit — review:** the `fix` regex round-trips a rendered line (name, detail,
command); accept yields `✓ noted` and mutates nothing (assert the vault is
byte-identical apart from the queue line's own resolution suffix); dismiss
archives; a `Fix` containing a backtick or a double quote still round-trips
after sanitization.

**Unit — automations:** proposal-worthy filtering (ok-with-Fix excluded,
warn-without-Fix excluded, warn-with-Fix and fail-with-Fix included); the ≤ 5
cap; dedup across two runs with the same checks (second run proposes nothing);
re-proposal when the `Fix` text changes but the name does not; skipping an
item already pending in the queue; nil `SelfCheck` idling with the stated
reason; dry-run writing nothing; the change-gate skipping an unchanged set.

**Registration:** the built-in count moves to 26 in **all three** count
surfaces — `registry_test.go`, `seeds_test.go` (starter *and* example yaml),
and `internal/mcp/tools_more_test.go`, the one a previous slice's plan missed.

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**, the live daemon's port; build the config by editing
`axon.config.example.yaml`, `sed` the port immediately, then
`grep -rn 7777 <scratch>` before any `axon start`, and confirm `daemon
running` in the log rather than just `scheduled …`; `mkdir -p` the data dir or
SQLite fails with `unable to open database file (14)`): induce a real warn
with a `Fix` (an unscheduled recipe, or a config that trips a checked
condition), run `axon run self-check`, read the queue line, accept it through
`axon review accept <id>` and confirm the suffix is `✓ noted` and **nothing
else changed**, re-run and confirm no duplicate, and confirm `axon doctor` and
the daemon agree on the check list.

## Docs to update on completion

`docs/03` (FR-206/FR-207 rows), `docs/06-component-automation-engine.md`,
`docs/AUTOMATIONS.md` (the table gains `self-check`; count 25 → 26),
`docs/09-component-dashboard-observability.md` (the review-queue kinds table
gains `fix`, the second acknowledge-only kind),
`README.md` ("All 25 automations" → 26), `internal/config/starter.go` +
`axon.config.example.yaml` (disabled seed), `docs/20` G1 (shipped, with the
two code findings recorded), and `CHANGELOG.md` `[Unreleased]`.
