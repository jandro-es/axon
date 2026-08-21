# Self-maintenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the daemon file its own drifted-install problems into the review queue, with the exact remediation attached and nothing self-applied.

**Architecture:** Four layers in dependency order. `internal/core` gains a variadic on `Doctor` so the CLI's two extra checks flow through one assembly path instead of being appended afterwards. `internal/review` gains a `fix` kind whose accept is acknowledge-only. `internal/automations` gains a `SelfCheck` function field on `EngineDeps`/`RunCtx` and one zero-model built-in that turns checks carrying a `Fix` into proposals. `cmd/axon` wires the seam, where the full config and build version already live.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, `spf13/cobra`, table-driven tests.

**Spec:** `docs/superpowers/specs/2026-08-21-self-maintenance-design.md`

## Global Constraints

- **FR IDs:** FR-206 (shared report assembly + the `SelfCheck` seam), FR-207 (the `self-check` automation + the `fix` review kind). Trace every change to one of them.
- **No new ADR.** No boundary moves: the sink is the existing review queue, accept is acknowledge-only, zero model calls, doctor is network-free, schema unchanged. If something appears to need an ADR, stop and raise it.
- **No migration.** Schema stays `0007`.
- **Zero model calls.** `self-check` never reaches Claude: no `runModel`, no `tokens.AgentCall`.
- **Accept never self-applies anything.** No running `axon service reinstall`, no `config set`, no restart, no update. Accept sets the suffix `✓ noted` and mutates nothing else. This is the single most important constraint in the slice.
- **Proposal-worthy rule (verbatim from the spec):** `Status != ok` **and** `Fix != ""`. Both `warn` and `fail` qualify.
- **Dedup:** proposal memory keyed on `hashShort(name + "\x00" + Fix)`, state key `self-check/proposed`. Cap **5 proposals per run**. The already-pending skip matches on **check name alone** — deliberately coarser than the memory key.
- **Nil `SelfCheck` is an idle, not a crash** — reason `"self-check unavailable"`.
- **Built-in count goes 25 → 26**, and the count is asserted in **three** places: `internal/automations/registry_test.go`, `internal/config/seeds_test.go` (starter *and* example yaml), and `internal/mcp/tools_more_test.go`. A previous slice's plan missed the third; do not repeat that.
- **Seeded disabled** in both seed files (S8).
- **Go hygiene:** `gofmt`/`goimports` clean, `go vet` and `golangci-lint` green, errors wrapped with `%w`, `context.Context` propagated. Run `gofmt -l .` before every commit.
- **Never bind port 7777** in any smoke config — that is the live daemon's port. Smoke work uses 7799.

---

### Task 1: `core.Doctor` takes the extras (FR-206)

**Files:**
- Modify: `internal/core/doctor.go:77` (the `Doctor` signature and its final return)
- Modify: `cmd/axon/doctor_cmd.go:57-65` (pass extras instead of appending)
- Test: `internal/core/doctor_test.go` (append)

**Interfaces:**
- Consumes: `core.Check`, `core.DoctorReport`, `config.Config`.
- Produces: `core.Doctor(cfg *config.Config, activeProfile string, extras ...Check) DoctorReport` — extras appended in order, after every built-in check. Tasks 4 and 5 call it.

- [ ] **Step 1: Write the failing test**

Append to `internal/core/doctor_test.go`:

```go
func TestDoctorAppendsExtrasInOrder(t *testing.T) {
	base := Doctor(nil, "personal")
	withExtras := Doctor(nil, "personal",
		Check{Name: "update-available", Status: StatusOK, Detail: "up to date"},
		Check{Name: "recipes", Status: StatusWarn, Detail: "one unscheduled", Fix: "schedule it"},
	)
	if len(withExtras.Checks) != len(base.Checks)+2 {
		t.Fatalf("want %d checks, got %d", len(base.Checks)+2, len(withExtras.Checks))
	}
	last := withExtras.Checks[len(withExtras.Checks)-2:]
	if last[0].Name != "update-available" || last[1].Name != "recipes" {
		t.Fatalf("extras must be appended in order, got %+v", last)
	}
	if last[1].Fix != "schedule it" {
		t.Fatalf("extras must pass through unchanged, got %+v", last[1])
	}
	// A failing extra must count toward the overall verdict.
	failing := Doctor(nil, "personal", Check{Name: "x", Status: StatusFail, Detail: "broken"})
	if !failing.HasFailure() {
		t.Fatal("a failing extra must make the report fail")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestDoctorAppendsExtras -v`
Expected: FAIL — `too many arguments in call to Doctor` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `internal/core/doctor.go`, change the signature and doc comment:

```go
// Doctor runs the Phase 0 prerequisite checks. cfg may be nil (e.g. when the
// config failed to load); the relevant checks degrade to warnings/failures
// rather than panicking. activeProfile is the resolved profile name, used to
// pick the profile whose auth_mode governs the ANTHROPIC_API_KEY check.
//
// extras are caller-supplied checks appended after the built-ins, in order.
// They exist because two checks cannot live here: `update-available` needs the
// build version (a main-package linker variable), and `recipes` lives in
// internal/automations, which imports core — so core importing it back would
// be an import cycle. Routing them through this parameter keeps ONE assembly
// path, so the CLI and the daemon cannot disagree about the report (FR-206).
func Doctor(cfg *config.Config, activeProfile string, extras ...Check) DoctorReport {
```

Then find the function's `return` and append before it. The final statement is currently `return DoctorReport{Checks: checks}` — change it to:

```go
	checks = append(checks, extras...)
	return DoctorReport{Checks: checks}
```

If the function returns `DoctorReport{Checks: checks}` at more than one place, append at each. Verify with:

```bash
grep -n "return DoctorReport{" internal/core/doctor.go
```

- [ ] **Step 4: Update the CLI to pass extras instead of appending**

In `cmd/axon/doctor_cmd.go`, replace the block at lines 57-65:

```go
			// Two checks core cannot compute: update-available needs the build
			// version, and recipes lives in automations (core→automations would
			// be an import cycle). They flow through Doctor's extras parameter
			// so the CLI and the daemon assemble the SAME report (FR-206).
			extras := []core.Check{updateAvailabilityCheck()}
			if cfg != nil {
				if p, ok := cfg.Profiles[activeProfile]; ok {
					extras = append(extras, automations.RecipesCheck(p))
				}
			}
			report := core.Doctor(cfg, activeProfile, extras...)
```

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/core/ ./cmd/axon/ && gofmt -l internal/core cmd/axon`
Expected: PASS both packages, `gofmt -l` silent. The existing doctor CLI tests must still pass — the rendered report is unchanged, only its assembly moved.

```bash
git add internal/core/doctor.go internal/core/doctor_test.go cmd/axon/doctor_cmd.go
git commit -m "refactor(core): Doctor takes caller-supplied extras, one assembly path (FR-206)"
```

---

### Task 2: The shared `selfCheckExtras` helper and its divergence test (FR-206)

**Files:**
- Modify: `cmd/axon/doctor_cmd.go` (extract the extras into a named helper)
- Test: `cmd/axon/doctor_cmd_test.go` (create if absent; append if present)

**Interfaces:**
- Consumes: `core.Check`, `automations.RecipesCheck`, `updateAvailabilityCheck` (both in package `main`).
- Produces: `func selfCheckExtras(cfg *config.Config, activeProfile string) []core.Check` in package `main`. Task 5 calls it when wiring the daemon, which is what makes the two reports provably identical.

- [ ] **Step 1: Write the failing test**

Append to `cmd/axon/doctor_cmd_test.go` (create with `package main` and imports `testing`, `github.com/jandro-es/axon/internal/config`, `github.com/jandro-es/axon/internal/core` if the file does not exist):

```go
// FR-206: the CLI and the daemon must assemble the SAME report. Both build
// their extras from this one helper, so this test is the regression that stops
// the two drifting apart again.
func TestSelfCheckExtrasNamesTheTwoCLIOnlyChecks(t *testing.T) {
	cfg := &config.Config{
		ActiveProfile: "personal",
		Profiles:      map[string]config.Profile{"personal": {}},
	}
	extras := selfCheckExtras(cfg, "personal")
	var names []string
	for _, c := range extras {
		names = append(names, c.Name)
	}
	want := []string{"update-available", "recipes"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}

	// A nil config still yields the update check — doctor must work when the
	// config failed to load.
	if got := selfCheckExtras(nil, "personal"); len(got) != 1 || got[0].Name != "update-available" {
		t.Fatalf("nil cfg: want just update-available, got %+v", got)
	}

	// The full report ends with exactly these, in this order.
	report := core.Doctor(cfg, "personal", selfCheckExtras(cfg, "personal")...)
	tail := report.Checks[len(report.Checks)-2:]
	if tail[0].Name != "update-available" || tail[1].Name != "recipes" {
		t.Fatalf("report tail wrong: %+v", tail)
	}
}
```

If `config.Config`'s field for the active profile is not `ActiveProfile`, check with `grep -n "ActiveProfile\|Profiles " internal/config/types.go` and adjust the literal.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/axon/ -run TestSelfCheckExtras -v`
Expected: FAIL — `undefined: selfCheckExtras` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `cmd/axon/doctor_cmd.go`, add the helper beside `updateAvailabilityCheck`:

```go
// selfCheckExtras builds the two checks core cannot compute: update
// availability (needs the build version) and recipes (lives in automations,
// which core cannot import). Both the `axon doctor` command and the daemon's
// self-check seam call this, so the two reports are identical by construction
// (FR-206) rather than by two lists someone has to keep in sync.
func selfCheckExtras(cfg *config.Config, activeProfile string) []core.Check {
	extras := []core.Check{updateAvailabilityCheck()}
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			extras = append(extras, automations.RecipesCheck(p))
		}
	}
	return extras
}
```

Then replace the inline block added in Task 1 with a call to it:

```go
			report := core.Doctor(cfg, activeProfile, selfCheckExtras(cfg, activeProfile)...)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/axon/ && gofmt -l cmd/axon`
Expected: PASS, `gofmt -l` silent.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/doctor_cmd.go cmd/axon/doctor_cmd_test.go
git commit -m "refactor(cli): extract selfCheckExtras so CLI and daemon share one report (FR-206)"
```

---

### Task 3: The `fix` review kind, acknowledge-only (FR-207)

**Files:**
- Modify: `internal/review/review.go` (the regex block ~line 46-60, the parse switch ~line 132, the `Accept` switch ~line 155, and the `Kind` doc comment on `Item` at line 36)
- Test: `internal/review/fix_test.go` (create)

**Interfaces:**
- Consumes: `review.Item`, `review.Load`, `review.Accept`, `review.Dismiss`, `vault.FS`.
- Produces: a parsed `Item{Kind: "fix", Note: <check name>, Target: <remediation command>}` and an `Accept` that returns the suffix `✓ noted` without mutating anything. Task 4 renders lines this regex must match.

- [ ] **Step 1: Write the failing test**

Create `internal/review/fix_test.go`. First check how the package's existing tests build a vault — `grep -n "func newTestVault\|vault.NewFS" internal/review/*_test.go | head -3` — and reuse that helper rather than inventing one. The test below assumes a helper `newQueueVault(t, lines string) *vault.FS` exists; if the package uses a different name or an inline `t.TempDir()` + `os.WriteFile` pattern, follow that instead.

```go
package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func fixQueueVault(t *testing.T, body string) *vault.FS {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".axon", "review-queue.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return vault.NewFS(dir)
}

func TestFixKindParsesAndAcceptsWithoutMutating(t *testing.T) {
	const line = "- [ ] fix service-path — \"service unit PATH cannot resolve claude\" → `axon service reinstall`\n"
	v := fixQueueVault(t, "# Review queue\n\n## Self-check (2026-08-21)\n"+line)
	ctx := context.Background()

	items, err := Load(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, c := range items {
		if c.Kind == "fix" {
			it = c
		}
	}
	if it.Kind != "fix" {
		t.Fatalf("fix kind not parsed from %q; got %+v", line, items)
	}
	if it.Note != "service-path" {
		t.Fatalf("Note must be the check name, got %q", it.Note)
	}
	if it.Target != "axon service reinstall" {
		t.Fatalf("Target must be the remediation command, got %q", it.Target)
	}

	// Accept acknowledges and mutates NOTHING — the single most important
	// property of this kind (FR-207).
	before, err := os.ReadDir(v.Root())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Accept(ctx, v, it.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got.Kind != "fix" {
		t.Fatalf("accepted the wrong item: %+v", got)
	}
	after, err := os.ReadDir(v.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("accept created or removed a vault entry — it must acknowledge only")
	}
	q, err := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(q), "✓ noted") {
		t.Fatalf("accept must mark the line noted:\n%s", q)
	}
}

func TestFixKindSurvivesQuotesAndBackticksAfterSanitization(t *testing.T) {
	// What the automation renders after sanitizing: quotes downgraded to
	// single, backticks stripped from the command.
	const line = "- [ ] fix claude-cli — \"claude 'CLI' not found on PATH\" → `brew install claude`\n"
	v := fixQueueVault(t, "# Review queue\n\n## Self-check (2026-08-21)\n"+line)
	items, err := Load(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range items {
		if c.Kind == "fix" {
			if c.Note != "claude-cli" || c.Target != "brew install claude" {
				t.Fatalf("round-trip wrong: %+v", c)
			}
			return
		}
	}
	t.Fatalf("fix kind not parsed: %+v", items)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/review/ -run TestFixKind -v`
Expected: FAIL — `fix kind not parsed`, because the line falls through the parse switch with an empty `Kind`.

- [ ] **Step 3: Write minimal implementation**

In `internal/review/review.go`, add to the regex `var` block beside `recipeRe`:

```go
	fixRe = regexp.MustCompile("^fix ([a-z0-9-]+) — \"(.+)\" → `(.+)`$")
```

Add a case to the parse switch, beside the `recipeRe` case:

```go
		case fixRe.MatchString(body):
			fm := fixRe.FindStringSubmatch(body)
			// fm[2] is the detail: matched so the regex anchors on the whole
			// rendered line, but not stored — nothing in the accept path needs
			// it, and the owner reads it from the line itself.
			it.Kind, it.Note, it.Target = "fix", fm[1], fm[3]
```

Add `"fix"` to the `Accept` switch beside `"recipe"`:

```go
	case "recipe", "fix":
		// Acknowledge-only: a recipe proposal (ADR-039) and a self-check fix
		// (FR-207) never mutate on accept. For `fix` this is the whole safety
		// property — AXON surfaces the remediation, the owner runs it.
		suffix = "✓ noted"
```

Update the `Kind` doc comment on `Item` (line 36) to include `| fix`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/review/ -v && gofmt -l internal/review`
Expected: PASS for the whole package, `gofmt -l` silent.

- [ ] **Step 5: Commit**

```bash
git add internal/review/review.go internal/review/fix_test.go
git commit -m "feat(review): fix kind, acknowledge-only accept (FR-207)"
```

---

### Task 4: The `self-check` automation (FR-207)

**Files:**
- Modify: `internal/automations/automation.go:40-58` (`RunCtx` gains `SelfCheck`)
- Modify: `internal/automations/engine.go:28-41` (`EngineDeps` gains `SelfCheck`), plus wherever `NewEngine` copies deps into a `RunCtx`
- Create: `internal/automations/selfcheck.go`
- Test: `internal/automations/selfcheck_test.go`

**Interfaces:**
- Consumes: `core.Check`/`core.CheckStatus`/`core.StatusOK` (`automations → core` already exists), `review.Load`, `loadProposalMemory`/`saveProposalMemory`/`hashShort` (`internal/automations/helpers.go`), `newRC(t, files)` (`standard_test.go`).
- Produces: `type SelfCheck struct{}` implementing `Automation` with `Name() == "self-check"`, and the `RunCtx.SelfCheck func(context.Context) []core.Check` field. Task 5 registers the automation and wires the field.

- [ ] **Step 1: Add the seam field**

In `internal/automations/automation.go`, add to `RunCtx`:

```go
	// SelfCheck returns the same doctor report `axon doctor` prints. Injected
	// by cmd/axon, where the full config and the build version live, so no
	// automation gains access to other profiles' configuration (FR-206). Nil
	// when the caller did not wire it — the self-check automation then idles.
	SelfCheck func(context.Context) []core.Check
```

Add the identical field to `EngineDeps` in `internal/automations/engine.go`, and copy it into the `RunCtx` the engine builds. Find the construction site:

```bash
grep -n "RunCtx{" internal/automations/engine.go
```

Add `SelfCheck: e.deps.SelfCheck,` (adjust to the receiver/field names used there) alongside the other copied fields.

- [ ] **Step 2: Write the failing test**

Create `internal/automations/selfcheck_test.go`:

```go
package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/core"
)

func withChecks(rc RunCtx, checks ...core.Check) RunCtx {
	rc.SelfCheck = func(context.Context) []core.Check { return checks }
	return rc
}

func TestSelfCheckProposesOnlyChecksWithAFix(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc,
		core.Check{Name: "config", Status: core.StatusOK, Detail: "valid"},
		core.Check{Name: "ok-with-fix", Status: core.StatusOK, Detail: "fine", Fix: "nothing"},
		core.Check{Name: "warn-no-fix", Status: core.StatusWarn, Detail: "odd, but nothing to do"},
		core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "PATH cannot resolve claude", Fix: "axon service reinstall"},
		core.Check{Name: "claude-cli", Status: core.StatusFail, Detail: "not found on PATH", Fix: "brew install claude"},
	)
	res, err := (SelfCheck{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	q, err := rc.Vault.Read(context.Background(), ".axon/review-queue.md")
	if err != nil {
		t.Fatalf("queue not written: %v (summary=%s)", err, res.Summary)
	}
	if !strings.Contains(q.Body, "fix service-path — \"PATH cannot resolve claude\" → `axon service reinstall`") {
		t.Fatalf("warn-with-fix must propose:\n%s", q.Body)
	}
	if !strings.Contains(q.Body, "fix claude-cli") {
		t.Fatalf("fail-with-fix must propose:\n%s", q.Body)
	}
	if strings.Contains(q.Body, "warn-no-fix") {
		t.Fatalf("a check with no Fix has nothing to propose:\n%s", q.Body)
	}
	if strings.Contains(q.Body, "ok-with-fix") {
		t.Fatalf("a passing check must never propose:\n%s", q.Body)
	}
	if strings.Contains(q.Body, "fix config") {
		t.Fatalf("a passing check must never propose:\n%s", q.Body)
	}
}

func TestSelfCheckDedupsAcrossRuns(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	ctx := context.Background()
	if _, err := (SelfCheck{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	res, err := (SelfCheck{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "no new") {
		t.Fatalf("a standing warning must propose once, got %q", res.Summary)
	}
	q, _ := rc.Vault.Read(ctx, ".axon/review-queue.md")
	if n := strings.Count(q.Body, "fix service-path"); n != 1 {
		t.Fatalf("want exactly 1 proposal, got %d:\n%s", n, q.Body)
	}
}

func TestSelfCheckReproposesWhenTheFixChanges(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	first := withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	if _, err := (SelfCheck{}).Run(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Same check name, DIFFERENT remediation => genuinely different work.
	// Resolve the pending item first, so the name-based pending skip does not
	// mask the memory-key behaviour under test.
	q, _ := rc.Vault.Read(ctx, ".axon/review-queue.md")
	resolved := strings.ReplaceAll(q.Body, "- [ ] fix", "- [x] fix")
	if err := rc.Vault.Patch(ctx, ".axon/review-queue.md", "", resolved); err != nil {
		// The queue is not a managed block; rewrite it directly instead.
		writeQueue(t, rc, resolved)
	}
	second := withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service install --force"})
	res, err := (SelfCheck{}).Run(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Summary, "no new") {
		t.Fatalf("a changed Fix is different work and must re-propose, got %q", res.Summary)
	}
}

func TestSelfCheckCapsProposalsPerRun(t *testing.T) {
	rc, _ := newRC(t, nil)
	var checks []core.Check
	for i := 0; i < selfCheckMaxProposals+3; i++ {
		checks = append(checks, core.Check{
			Name: "check-" + string(rune('a'+i)), Status: core.StatusWarn,
			Detail: "d", Fix: "do thing " + string(rune('a'+i)),
		})
	}
	rc = withChecks(rc, checks...)
	if _, err := (SelfCheck{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	q, _ := rc.Vault.Read(context.Background(), ".axon/review-queue.md")
	if n := strings.Count(q.Body, "- [ ] fix "); n != selfCheckMaxProposals {
		t.Fatalf("want %d proposals, got %d:\n%s", selfCheckMaxProposals, n, q.Body)
	}
}

func TestSelfCheckIdlesWithoutASeam(t *testing.T) {
	rc, _ := newRC(t, nil) // SelfCheck left nil
	ch, err := (SelfCheck{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatalf("a nil seam must idle, not error: %v", err)
	}
	if ch.Changed || !strings.Contains(ch.Reason, "unavailable") {
		t.Fatalf("want an unavailable idle, got %+v", ch)
	}
	res, err := (SelfCheck{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("a nil seam must idle, not error: %v", err)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatalf("a nil seam must write nothing (summary=%s)", res.Summary)
	}
}

func TestSelfCheckDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	res, err := (SelfCheck{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "would") {
		t.Fatalf("dry-run summary wrong: %q", res.Summary)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("dry-run must not write the queue")
	}
}

func TestSelfCheckChangeGate(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	ctx := context.Background()
	ch, err := (SelfCheck{}).DetectChange(ctx, rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first detect: %+v err=%v", ch, err)
	}
	rc.LastCursor = ch.Cursor
	again, err := (SelfCheck{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Fatalf("an unchanged check set must skip, got %+v", again)
	}
}
```

Add the small helper the re-propose test needs, at the bottom of the same file:

```go
// writeQueue rewrites the review queue directly. The queue is a plain
// appended file, not a managed block, so tests that need to resolve an item
// edit it in place the way a human would.
func writeQueue(t *testing.T, rc RunCtx, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

with `"os"` and `"path/filepath"` added to the imports. Then simplify the re-propose test's resolve step to call `writeQueue(t, rc, resolved)` directly instead of attempting `Patch` first — `Patch` targets managed blocks and the queue has none.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/automations/ -run TestSelfCheck -v`
Expected: FAIL — `undefined: SelfCheck` (compile error).

- [ ] **Step 4: Write minimal implementation**

Create `internal/automations/selfcheck.go`:

```go
package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/review"
)

const (
	selfCheckState         = "self-check/proposed"
	selfCheckMaxProposals  = 5
)

// SelfCheck turns doctor findings into review-queue proposals (FR-207): the
// daemon files its own drifted-install work where the owner already reviews
// work. Zero model calls. **Accepting a proposal never applies anything** —
// it acknowledges, and the owner runs the command. Auto-application of system
// changes is deliberately out of scope (docs/20 G1).
type SelfCheck struct{}

func (SelfCheck) Name() string    { return "self-check" }
func (SelfCheck) Essential() bool { return false }

// proposal is one actionable finding: a check that failed or warned AND
// carries a remediation.
type proposal struct {
	name   string
	detail string
	fix    string
	key    string
}

// sanitize makes a check's text safe to round-trip through the queue line's
// regex. Check text is developer-authored, not user input, but the line is
// parsed back by regex: a stray double quote or backtick would break it.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, `"`, "'")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}

// actionable filters the report to proposal-worthy checks: not ok, and
// carrying a Fix. The Check type already separates Detail (what is wrong)
// from Fix (what to do), so a check with no Fix has nothing to propose —
// filing it would only train the owner to skim the queue.
func (SelfCheck) actionable(ctx context.Context, rc RunCtx) []proposal {
	if rc.SelfCheck == nil {
		return nil
	}
	var out []proposal
	for _, c := range rc.SelfCheck(ctx) {
		if c.Status == core.StatusOK || strings.TrimSpace(c.Fix) == "" {
			continue
		}
		p := proposal{name: c.Name, detail: sanitize(c.Detail), fix: sanitize(c.Fix)}
		p.key = hashShort(p.name + "\x00" + p.fix)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// DetectChange hashes the actionable set, so an unchanged system skips with
// no work. A nil seam idles rather than erroring.
func (s SelfCheck) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	if rc.SelfCheck == nil {
		return Change{Changed: false, Reason: "self-check unavailable (no doctor seam wired)"}, nil
	}
	props := s.actionable(ctx, rc)
	var b strings.Builder
	for _, p := range props {
		b.WriteString(p.key)
		b.WriteByte('\n')
	}
	cursor := "selfcheck:" + hashShort(b.String())
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no change in actionable checks"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d actionable check(s)", len(props)), Cursor: cursor}, nil
}

func (s SelfCheck) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	if rc.SelfCheck == nil {
		return RunResult{Summary: "self-check unavailable (no doctor seam wired)"}, nil
	}
	props := s.actionable(ctx, rc)

	// Already pending in the queue — matched on check NAME alone, deliberately
	// coarser than the memory key: if a fix for this check is already waiting
	// for the owner, a second one with different wording is noise, not news.
	pending := map[string]bool{}
	if items, lerr := review.Load(ctx, rc.Vault); lerr == nil {
		for _, it := range items {
			if !it.Checked && it.Kind == "fix" {
				pending[it.Note] = true
			}
		}
	}
	proposed := loadProposalMemory(ctx, rc, selfCheckState)

	var queue []string
	var fresh []proposal
	for _, p := range props {
		if pending[p.name] || proposed[p.key] {
			continue
		}
		queue = append(queue, fmt.Sprintf("- [ ] fix %s — %q → `%s`", p.name, p.detail, p.fix))
		fresh = append(fresh, p)
		if len(queue) >= selfCheckMaxProposals {
			break
		}
	}
	if len(queue) == 0 {
		return RunResult{Summary: "no new fixes to propose"}, nil
	}
	if rc.DryRun {
		return RunResult{
			Summary: fmt.Sprintf("would propose %d fix(es) to review", len(queue)),
			Changes: queue,
		}, nil
	}
	header := fmt.Sprintf("\n## Self-check (%s)\n", today(rc))
	if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
		return RunResult{}, aerr
	}
	for _, p := range fresh {
		proposed[p.key] = true
	}
	saveProposalMemory(ctx, rc, selfCheckState, proposed)
	return RunResult{
		Summary: fmt.Sprintf("proposed %d fix(es) to review", len(queue)),
		Changes: []string{".axon/review-queue.md"},
	}, nil
}
```

Note the `%q` in the queue line: it renders the detail with surrounding double quotes and escapes anything remaining, matching `fixRe`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/automations/ -v && gofmt -l internal/automations && go vet ./internal/automations/`
Expected: PASS for the whole package, `gofmt -l` silent, vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/automations/automation.go internal/automations/engine.go internal/automations/selfcheck.go internal/automations/selfcheck_test.go
git commit -m "feat(automations): self-check proposes doctor fixes to the review queue (FR-207)"
```

---

### Task 5: Register the 26th automation and wire the seam

**Files:**
- Modify: `internal/automations/registry.go`, `internal/automations/catalog.go`, `internal/automations/registry_test.go`
- Modify: `internal/config/starter.go`, `axon.config.example.yaml`
- Modify: `internal/mcp/tools_more_test.go` (the third count surface)
- Modify: `cmd/axon/deps.go:198-201` (wire `SelfCheck` into `EngineDeps`)

**Interfaces:**
- Consumes: `SelfCheck{}` (Task 4), `selfCheckExtras` (Task 2), `core.Doctor` (Task 1).
- Produces: `"self-check"` in `Registry(profile)`, seeded disabled in both files, and a live `SelfCheck` function in the daemon's engine.

- [ ] **Step 1: Add to the want list and watch the invariants fail**

Add `"self-check"` to the `want` literal in `internal/automations/registry_test.go`, after `"orphan-report"`.

Run: `go test ./internal/automations/ ./internal/config/ ./internal/mcp/ -run 'Registry|Seeds|Catalog|AutomationsList' 2>&1 | tail -20`
Expected: FAIL — registry has 25, want 26; `seeds_test` reports `self-check` missing from both seeds.

- [ ] **Step 2: Register and seed it**

In `internal/automations/registry.go`, beside `OrphanReport`:

```go
		SelfCheck{}.Name():          SelfCheck{},
```

In `internal/automations/catalog.go`:

```go
	"self-check":          "Weekly zero-model sweep: turns doctor findings that carry a remediation into review-queue proposals. Accepting one only acknowledges it — AXON never applies a system change itself; the fix is a copyable command. Proposes each distinct remediation once. Disabled by default.",
```

In `internal/config/starter.go`, beside `orphan-report`:

```
      self-check:        { enabled: false, schedule: "0 12 * * 1",      model: none,      budget_tokens: 0 }
```

In `axon.config.example.yaml`, beside `orphan-report`:

```
      self-check:          { enabled: false, schedule: "0 12 * * 1",     model: none,      budget_tokens: 0 }        # weekly doctor→review-queue proposals; accept only acknowledges, never applies (G1/FR-207, off by default)
```

In `internal/mcp/tools_more_test.go`, change the automation-count assertion from 25 to 26 (find it with `grep -n "25 automations" internal/mcp/tools_more_test.go`).

- [ ] **Step 3: Wire the seam in the daemon**

In `cmd/axon/deps.go`, inside `buildServices`, extend the `EngineDeps` literal:

```go
	engine := automations.NewEngine(automations.EngineDeps{
		Profile: d.name, Config: d.profile, DB: d.db, Vault: d.vault,
		Manager: mgr, Searcher: searcher, Embedder: d.embedder, Pipeline: pipeline, Bus: bus,
		// FR-206: the daemon sees exactly the report `axon doctor` prints —
		// same assembly, same extras — so self-check cannot propose from a
		// view of the system the owner has never seen.
		SelfCheck: func(context.Context) []core.Check {
			return core.Doctor(d.cfg, d.name, selfCheckExtras(d.cfg, d.name)...).Checks
		},
	})
```

Add `"context"` and `"github.com/jandro-es/axon/internal/core"` to `cmd/axon/deps.go`'s imports if absent.

- [ ] **Step 4: Run the invariants and the example validator**

Run: `go test ./internal/automations/ ./internal/config/ ./internal/mcp/ ./cmd/axon/ && gofmt -l internal cmd`
Expected: PASS all four, `gofmt -l` silent.

Run: `go run ./cmd/axon config validate --config axon.config.example.yaml`
Expected: `✓ OK … is valid`.

- [ ] **Step 5: Full gate and commit**

```bash
go build ./... && go test ./... && golangci-lint run
git add internal/automations/registry.go internal/automations/catalog.go internal/automations/registry_test.go internal/config/starter.go axon.config.example.yaml internal/mcp/tools_more_test.go cmd/axon/deps.go
git commit -m "feat: register self-check as the 26th built-in and wire the doctor seam (FR-206/207)"
```

Expected: whole suite green, 0 lint issues.

---

### Task 6: Documentation and the roadmap

**Files:**
- Modify: `docs/03-requirements.md`, `docs/06-component-automation-engine.md`, `docs/AUTOMATIONS.md`, `docs/09-component-dashboard-observability.md`, `README.md`, `docs/20-roadmap-ai-os.md`, `CHANGELOG.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: the final shapes from Tasks 1–5. No code.
- Produces: nothing consumed by a later task.

- [ ] **Step 1: Add the FR rows to `docs/03-requirements.md`**

After the FR-205 row, add a section and two rows:

```markdown
### Self-maintenance (docs/20 G1) *(built 2026-08-21)*

FR-206…FR-207 graduate `docs/20` G1; spec in
`docs/superpowers/specs/2026-08-21-self-maintenance-design.md`. No ADR (no
boundary moves), no migration.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-206 | S | **One doctor report, and a seam the daemon reaches it through.** `core.Doctor(cfg, activeProfile, extras ...Check)` appends caller-supplied checks after the built-ins, and `cmd/axon`'s `selfCheckExtras(cfg, profile)` builds the two the CLI alone could compute — `update-available` (needs the build version, a `main` linker variable) and `recipes` (lives in `automations`, which imports `core`, so `core` importing it back would be an import cycle). Both `axon doctor` and the daemon call that one helper, so the CLI and the daemon cannot disagree about the report; previously the CLI appended those two *after* `core.Doctor` returned, so any in-daemon caller would have seen a smaller report than the owner. `EngineDeps`/`RunCtx` gain `SelfCheck func(context.Context) []core.Check`, wired in `cmd/axon/deps.go` where the full config lives — no automation gains access to other profiles' configuration. A nil `SelfCheck` idles ("self-check unavailable"), never panics. Doctor is network-free (the update check reads a daily cache), so scheduling it adds no egress. |
| FR-207 | S | **`self-check` automation + the `fix` review kind.** The 26th built-in, zero-model, weekly, disabled by default: every check with `Status != ok` **and** a non-empty `Fix` becomes a review-queue item `- [ ] fix <name> — "<detail>" → \`<command>\``. **Accepting only acknowledges** (`✓ noted`, the `recipe` kind's precedent) — AXON never applies a system change itself; auto-application is deliberately out of scope. Detail and Fix are sanitized (newlines collapsed, `"` → `'`, backticks stripped) so the line round-trips through its own regex. Dedup: proposal memory keyed on `hashShort(name + "\x00" + Fix)`, so a standing warning proposes **once** and a *changed* remediation re-proposes; items already pending are skipped by check name alone, deliberately coarser. Capped at 5 proposals per run. The change-gate hashes the actionable set. Run-failure streaks are deferred to a later slice — a failed automation carries no remediation, so it would be a notification wearing a fix's shape. |
```

- [ ] **Step 2: Update the component and user docs**

- `docs/AUTOMATIONS.md`: bump `# The 25 automations` → 26 and `AXON ships 25 automations.` → 26; add a `self-check` section after `orphan-report` and a table row `| \`self-check\` | off | none | \`0 12 * * 1\` | Doctor findings → review proposals |`.
- `docs/06-component-automation-engine.md`: add a `self-check` subsection describing the seam, the proposal-worthy rule, the dedup key and the acknowledge-only accept.
- `docs/09-component-dashboard-observability.md`: add `fix` to the review-queue kinds, noting it is the second acknowledge-only kind.
- `README.md`: `All 25 automations` → `All 26 automations`.

Verify no stale count survives:

```bash
grep -rn "25 automations\|all 25\|All 25" README.md docs/ CLAUDE.md | grep -v superpowers/
```

Expected: no hits.

- [ ] **Step 3: Mark G1 shipped in `docs/20`**

```markdown
### G1 — The daemon proposes its own fixes (M) · **SHIPPED 2026-08-21 — FR-206/FR-207** (spec: `docs/superpowers/specs/2026-08-21-self-maintenance-design.md`)
```

Add the two code findings and resolve the open decisions:

```markdown
**Two findings from the code, neither visible from this entry.** (1) Nothing in
the daemon had ever run doctor — `core.Doctor` had exactly one caller, the CLI —
so the seam was the work, not the proposal logic. (2) The *full* report was
assembled in `cmd/axon/doctor_cmd.go`, which appended `update-available` and
`recipes` after `core.Doctor` returned; an in-daemon caller would have proposed
from a smaller report than the owner had ever seen. Both are fixed by FR-206's
shared `selfCheckExtras` helper.

**Open decisions resolved.** Proposal-worthy = any check carrying a non-empty
`Fix`, which needs no allow-list to maintain — the `Check` type already
separates "what is wrong" from "what to do". Dedup is proposal memory keyed on
name + remediation, so a standing warning proposes once and a *changed*
remediation re-proposes; there is no weekly re-nag. `update-available` belongs
here rather than staying dashboard-only, since it carries a `Fix` like any
other check. Run-failure streaks were **cut** from v1 and remain a candidate.
```

Also update the sequencing sketch, which currently names G1 as "the strongest remaining pick".

- [ ] **Step 4: CHANGELOG and CLAUDE.md**

`CHANGELOG.md` under `[Unreleased]` → `### Added`:

```markdown
- **AXON now files its own maintenance work.** (FR-206, FR-207; no ADR, no
  schema change; graduating `docs/20` G1.) A new zero-model automation,
  `self-check` (the 26th, disabled by default), turns any `axon doctor` finding
  that carries a remediation into a review-queue item with the exact command
  attached. **Accepting one only acknowledges it** — AXON never runs a system
  change on your behalf. Each distinct remediation proposes once, not weekly.
  The daemon and the CLI now assemble the doctor report through one path, so
  they cannot disagree about what is wrong.
```

`CLAUDE.md`: FR range → `FR-01…FR-207`; `all 25 automations` → 26; add a G1 line beside the E1 entry noting the seam, the acknowledge-only accept, and that built-ins are 26.

- [ ] **Step 5: Final gate and commit**

```bash
gofmt -l . && go build ./... && go test ./... && golangci-lint run
git add -A
git commit -m "docs: FR-206/FR-207, self-check in the automation set, G1 shipped"
```

---

### Task 7: Live smoke in an isolated environment

**Files:**
- Create: `<scratchpad>/selfcheck-smoke/` (throwaway — never committed)

**Interfaces:**
- Consumes: the built binary and every change from Tasks 1–6.
- Produces: a verification record for the completion report.

- [ ] **Step 1: Build the binary**

```bash
cd /Users/jandro/Projects/axon/web && npm run build
cd /Users/jandro/Projects/axon
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/selfcheck-smoke
mkdir -p "$S/home/profiles/personal" "$S/vault/.axon"
go build -o "$S/axon" ./cmd/axon
```

`cd` back to the repo root **by absolute path** before `go build` — the `cd web` persists within a compound command and has broken this step in two previous slices.

- [ ] **Step 2: Build the smoke config and re-port it**

```bash
cp /Users/jandro/Projects/axon/axon.config.example.yaml "$S/home/config.yaml"
sed -i '' 's/port: 7777/port: 7799/g' "$S/home/config.yaml"
grep -rn 7777 "$S" && echo "STOP: 7777 present" || echo "port clean"
```

`port: 7777` appears in **both** profiles and is the live daemon's port. Then edit `$S/home/config.yaml`: point the personal profile's `vault_path` at `$S/vault` and `data_dir` at `$S/home/profiles/personal`, and set `self-check: { enabled: true, … }`.

- [ ] **Step 3: Induce a real warning that carries a Fix**

The cheapest reliable one is an unscheduled recipe — `automations.RecipesCheck` warns with a `Fix` when a recipe name collides with a built-in. Add to the personal profile:

```yaml
    recipes:
      - name: heartbeat
        purpose: "Deliberate collision to make doctor warn."
        inputs:
          - name: recent
            recent_notes: {lookback_days: 7, limit: 5}
        render: "{{recent}}"
        output:
          block: {note: "03-Resources/Smoke.md", block: "smoke"}
```

`heartbeat` is a built-in, so `RecipesCheck` returns a warn with `Fix: "rename the recipe in config.yaml"`. Confirm the CLI sees it:

```bash
export AXON_HOME="$S/home"
"$S/axon" doctor --config "$S/home/config.yaml" | grep -i recipe
```

Note: `axon run`/`axon start` **refuse** on a built-in collision (FR-201), so run `self-check` via a config whose recipe collides only if `axon run` still executes; if it refuses, instead induce the warning by pointing `claude` off PATH or using any other check that warns with a Fix, and record which one you used.

- [ ] **Step 4: Run self-check and verify the queue line**

```bash
"$S/axon" run self-check --config "$S/home/config.yaml"
cat "$S/vault/.axon/review-queue.md"
```

Verify: the line matches `- [ ] fix <name> — "<detail>" → \`<command>\``, and the command is the check's `Fix` verbatim.

- [ ] **Step 5: Verify accept acknowledges and changes nothing else**

```bash
"$S/axon" review list --config "$S/home/config.yaml"
# take the id of the fix item
cp -R "$S/vault" "$S/vault-before"
"$S/axon" review accept <id> --config "$S/home/config.yaml"
diff -r "$S/vault-before" "$S/vault"
```

Expected: the **only** difference is the queue line gaining `✓ noted`. Nothing else in the vault changed, no service unit was touched, no config was rewritten. This is the slice's central safety property — check it explicitly, not by inference.

Then re-run and confirm no duplicate:

```bash
"$S/axon" run self-check --config "$S/home/config.yaml"
grep -c "fix " "$S/vault/.axon/review-queue.md"
```

Expected: the count is unchanged and the run reports "no new fixes to propose".

- [ ] **Step 6: Confirm the daemon and CLI agree, then clean up**

```bash
"$S/axon" doctor --json --config "$S/home/config.yaml" | grep -o '"name":"[^"]*"' | head -30
"$S/axon" automations --config "$S/home/config.yaml" | grep self-check
"$S/axon" start --config "$S/home/config.yaml"
```

For `axon start`, confirm the log contains **`daemon running`** (not merely `scheduled …` — a bind failure prints the banner and schedules everything before dying), confirm `self-check` is scheduled, confirm the live daemon on 7777 is untouched (`lsof -ti :7777`), then SIGTERM and confirm a clean exit with the pidfile removed. Delete `$S`.

---

## Self-Review

**Spec coverage:** shared assembly + the import-cycle rationale → Tasks 1 and 2; the divergence regression test → Task 2; the `SelfCheck` field and nil-idle → Task 4 Step 1 and its idle test; the proposal-worthy rule → Task 4; the queue line, the `fix` kind and acknowledge-only accept → Task 3; sanitization → Task 3's second test and Task 4's `sanitize`; dedup key, re-propose on changed Fix, name-based pending skip, 5-cap → Task 4's tests; the automation's change-gate and dry-run → Task 4; registration across all **three** count surfaces plus the daemon wiring → Task 5; FR rows, docs, G1, CHANGELOG → Task 6; live smoke including the explicit "accept mutated nothing" diff → Task 7. Out-of-scope items (self-application, run-failure streaks, a dashboard panel, re-nagging) appear in no task, correctly.

**Type consistency:** `core.Doctor(cfg, activeProfile, extras ...Check)` defined in Task 1, called in Task 2 and Task 5. `selfCheckExtras(cfg, activeProfile) []core.Check` defined in Task 2, called in Tasks 2 and 5. `SelfCheck func(context.Context) []core.Check` added to both `RunCtx` and `EngineDeps` in Task 4 Step 1, populated in Task 5 Step 3, read in Task 4's `actionable`. `selfCheckState` / `selfCheckMaxProposals` declared once in Task 4 and referenced by those names in Task 4's tests. The automation type `SelfCheck` and the `RunCtx` field `SelfCheck` share a name in different namespaces — legal Go, and the field is always reached as `rc.SelfCheck` while the type is always constructed as `SelfCheck{}`; flagged here so it does not read as a mistake.

**Known fragility, flagged rather than hidden:** Task 7 Step 3 induces a doctor warning via a deliberately colliding recipe, but FR-201 makes `axon run` **refuse** on exactly that collision — so the induction may need a different check. The step says so and tells the executor to pick another warning-with-Fix and record which. Do not silently skip the smoke if the first induction fails.
