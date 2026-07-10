# T5 — Stale-Action Sweep & Weekly Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A zero-model weekly `actions-review` automation that proposes stale open actions to the review queue (new `stalled` kind), and an Accept that demotes the source line to `#someday` via an additive `vault.TagAction`.

**Architecture:** `vault.TagAction` mirrors T3's `CompleteAction` (additive line edit). A new `stalled` review kind + `Accept` case route to it. `ActionsReview` clones `MergeProposals` (all-notes scan → proposal-memory dedup → `rc.Vault.Append`), using `db.NotesUpdatedBefore` for the staleness signal. Off by default, zero model calls.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, the `internal/automations` engine + `internal/review` queue.

**Spec:** `docs/superpowers/specs/2026-07-10-t5-actions-review-design.md`

## Global Constraints

- **Cardinal rule 1:** No Claude call bypasses the token manager. *T5 is `model: none` — no `runModel`, no ledger.*
- **Cardinal rule 2:** No vault mutation outside wikilink-safe ops. *The sweep only `Append`s to `.axon/review-queue.md`; Accept calls `vault.TagAction` (ADR-034-class additive checkbox-line edit). No delete/move/managed-block clobber.*
- **User-approved only:** the sweep *proposes*; the `#someday` edit happens solely on a human Accept via the review queue — no agent/MCP tagging path.
- **S8:** off by default; a fresh install never runs it.
- **S9:** reads the derived index + notes table; writes only the review queue and (on accept) the source line, reproduced by the next reindex.
- **FR-31:** change-gated; a week with no new stale actions is a skip.
- Go: `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green. Wrap errors `%w`. Propagate `context.Context`. Use `rc.now()`, not `time.Now()`, in automations.
- Run suites with `env -u FORCE_COLOR`.
- Staleness = source note's `updated` predates `today − actions.stale_after_days` (default 30). Accept disposition = `#someday` only. Propose-once (proposal memory); cap `staleMaxProposals = 10`/run.
- FR IDs: FR-167 (sweep + `stalled` kind + config), FR-168 (Accept → `#someday` via `vault.TagAction`). No new ADR (ADR-034 extension).
- GateGuard fires a fact-force preamble on the first Write/Edit/Bash each turn and blocks `git commit --amend` / `rm -rf`; comply tersely, use follow-up commits, skip scratch cleanup.

---

### Task 1: `vault.TagAction` (FR-168)

**Files:**
- Modify: `internal/vault/actions.go` (add `TagAction`)
- Test: `internal/vault/actions_test.go` (add tests)

**Interfaces:**
- Consumes: `(*FS).safeAbs`, `splitFrontmatter`, `writeRaw`, `reassemble`; `actions.Extract`, `actions.StateOpen`; `ErrActionNotFound` (all already in `internal/vault/actions.go` + `internal/actions` from T3).
- Produces (for Task 2): `func (v *FS) TagAction(ctx context.Context, path, actionText, tag string) error`.

- [ ] **Step 1: Write the failing test**

Add to `internal/vault/actions_test.go`:

```go
func TestTagActionAppendsTag(t *testing.T) {
	ctx := context.Background()
	note := "---\ntitle: T\n---\n## Todo\n- [ ] email John\n- [ ] other\n"
	v := newTempVault(t, map[string]string{"p.md": note})

	if err := v.TagAction(ctx, "p.md", "email John", "someday"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "p.md"))
	got := string(raw)
	if !strings.Contains(got, "- [ ] email John #someday") {
		t.Errorf("tag not appended:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] other\n") || strings.Contains(got, "- [ ] other #someday") {
		t.Error("other line must be untouched")
	}
	// idempotent: second call adds nothing, no error
	if err := v.TagAction(ctx, "p.md", "email John", "someday"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(filepath.Join(v.Root(), "p.md"))
	if strings.Count(string(raw2), "#someday") != 1 {
		t.Errorf("tag double-applied:\n%s", raw2)
	}
}

func TestTagActionUnknownText(t *testing.T) {
	ctx := context.Background()
	v := newTempVault(t, map[string]string{"p.md": "- [ ] x\n"})
	err := v.TagAction(ctx, "p.md", "no such task", "someday")
	if !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("want ErrActionNotFound, got %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "p.md"))
	if string(raw) != "- [ ] x\n" {
		t.Errorf("file must be unchanged: %q", raw)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/vault/ -run TestTagAction -v`
Expected: FAIL — `v.TagAction undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/vault/actions.go`:

```go
// TagAction appends " #<tag>" to the FIRST open checkbox line in the note whose
// T1 text matches actionText, if the tag isn't already present. Byte-precise +
// atomic; additive (never removes/reorders). Returns ErrActionNotFound (nothing
// written) if no open line matches. Like CompleteAction (ADR-034) it edits a
// human-authored checkbox line, user-initiated via the review queue only.
func (v *FS) TagAction(ctx context.Context, path, actionText, tag string) error {
	abs, err := v.safeAbs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	fm, body := splitFrontmatter(string(data))
	lines := strings.Split(body, "\n")
	for _, a := range actions.Extract(path, body, false) {
		if a.State != actions.StateOpen || a.Text != actionText {
			continue
		}
		if strings.Contains(lines[a.LineNo], "#"+tag) {
			return nil // already tagged — idempotent
		}
		lines[a.LineNo] = strings.TrimRight(lines[a.LineNo], " ") + " #" + tag
		return v.writeRaw(path, reassemble(fm, strings.Join(lines, "\n")))
	}
	return ErrActionNotFound
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/vault/ -run TestTagAction -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/vault/actions.go internal/vault/actions_test.go
git commit -m "feat(T5): vault.TagAction — additive #tag checkbox-line edit (FR-168, ADR-034 ext)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: `stalled` review kind (FR-167/168)

**Files:**
- Modify: `internal/review/review.go` (regex + `Load` case + `Accept` case)
- Test: `internal/review/review_test.go` (or a new `stalled_test.go`)

**Interfaces:**
- Consumes: `vault.TagAction` (Task 1); the `Item` struct (`Kind`/`Note`/`Target`); the `Load` parse switch (`review.go:94`); the `Accept` switch (`review.go:142`).
- Produces (for Task 4): a `stalled` review kind — the queue-line grammar `- [ ] stalled action "<text>" in [[<note>]] (<N>d) — still relevant?`.

- [ ] **Step 1: Write the failing test**

Add to `internal/review/review_test.go` (mirror the existing merge/reconcile kind tests — grep for how they seed `.axon/review-queue.md` and call `Load`/`Accept`; the helper is likely `writeQueue`/a temp vault):

```go
func TestStalledKindParseAndAccept(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	// source note with the open task
	mustWrite(t, v, "01-Projects/p.md", "## Todo\n- [ ] tidy backlog\n")
	// the review-queue proposal
	mustWrite(t, v, ".axon/review-queue.md",
		"## Stale actions\n- [ ] stalled action \"tidy backlog\" in [[01-Projects/p]] (42d) — still relevant?\n")

	items, err := Load(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, x := range items {
		if x.Kind == "stalled" {
			it = x
		}
	}
	if it.ID == "" {
		t.Fatalf("stalled item not parsed: %+v", items)
	}
	if it.Note != "01-Projects/p" || it.Target != "tidy backlog" {
		t.Errorf("stalled fields wrong: note=%q target=%q", it.Note, it.Target)
	}

	if _, err := Accept(ctx, v, it.ID); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Read(ctx, "01-Projects/p.md")
	if !strings.Contains(n.Body, "- [ ] tidy backlog #someday") {
		t.Errorf("accept did not tag #someday:\n%s", n.Body)
	}
}
```

> Adapt `mustWrite`/`vault.NewFS` to the package's actual review-test helpers (grep `func mustWrite`/`func writeQueue` in `internal/review/*_test.go`; the merge kind's test is the closest precedent). The contract: a `stalled` line parses with note+text; Accept tags `#someday`.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/review/ -run TestStalledKind -v`
Expected: FAIL — no `stalled` kind parsed / Accept default error.

- [ ] **Step 3: Write minimal implementation**

In `internal/review/review.go`, add the regex to the `var (...)` block (near `mergeRe`, `review.go:55`):

```go
	stalledRe = regexp.MustCompile(`^stalled action "(.+)" in \[\[([^\]]+)\]\] \(\d+d\)`)
```

Add a case to the `Load` parse switch (after the `mergeRe` case, `review.go:117`):

```go
		case stalledRe.MatchString(body):
			sm := stalledRe.FindStringSubmatch(body)
			it.Kind, it.Target, it.Note = "stalled", sm[1], sm[2] // Target=text, Note=note path
```

Add a case to the `Accept` switch (after the `merge` case, `review.go:157`):

```go
		case "stalled":
			if err := v.TagAction(ctx, it.Note+".md", it.Target, "someday"); err != nil {
				return Item{}, err
			}
			suffix = "✓ demoted to #someday"
```

Update the `Item.Kind` doc comment (`review.go:36`) to include `| stalled`.

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/review/ -run TestStalledKind -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/review/review.go internal/review/review_test.go
git commit -m "feat(T5): stalled review kind — accept demotes to #someday (FR-167/168)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Config — `actions.stale_after_days` (FR-167)

**Files:**
- Modify: `internal/config/types.go` (`ActionsConfig` + `Profile.Actions`)
- Modify: `axon.config.example.yaml` + `internal/config/starter.go` (seeds)
- Test: `internal/config/` (a small helper test)

**Interfaces:**
- Produces (for Task 4): `config.ActionsConfig{StaleAfterDays int}` + `func (ActionsConfig) StaleAfterDaysOr() int`; `Profile.Actions ActionsConfig`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/actions_test.go`:

```go
package config

import "testing"

func TestStaleAfterDaysOr(t *testing.T) {
	if (ActionsConfig{}).StaleAfterDaysOr() != 30 {
		t.Error("default stale_after_days must be 30")
	}
	if (ActionsConfig{StaleAfterDays: 14}).StaleAfterDaysOr() != 14 {
		t.Error("override not honored")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestStaleAfterDaysOr -v`
Expected: FAIL — `undefined: ActionsConfig`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/types.go`, add the struct + helper (near the other per-feature config blocks like `MergeConfig`/`ResurfacingConfig`):

```go
// ActionsConfig tunes the 1.2.5 actions subsystem (T5). Optional.
type ActionsConfig struct {
	StaleAfterDays int `yaml:"stale_after_days" validate:"omitempty,min=1"`
}

func (a ActionsConfig) StaleAfterDaysOr() int {
	if a.StaleAfterDays > 0 {
		return a.StaleAfterDays
	}
	return 30
}
```

Add the field to `Profile` (beside `Merge`/`Resurfacing`):

```go
	Actions ActionsConfig `yaml:"actions"`
```

In `axon.config.example.yaml` (near the `merge:` block) and `internal/config/starter.go` (the embedded starter, same place), add:

```yaml
    actions: { stale_after_days: 30 }   # T5: sweep open undated actions in notes untouched this long → review queue
```

And in the `automations:` map of both seeds (next to `merge-proposals:`):

```yaml
      actions-review:      { enabled: false, schedule: "0 8 * * 6",  model: none,    budget_tokens: 0 }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/config/ ./cmd/axon/ -run 'TestStaleAfterDaysOr|TestConfigValidateOnExampleConfig' -v`
Expected: PASS (helper + example config parses with the new block/automation).

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/starter.go internal/config/actions_test.go axon.config.example.yaml
git commit -m "feat(T5): actions.stale_after_days config + actions-review seed (FR-167)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: `actions-review` automation (FR-167)

**Files:**
- Create: `internal/automations/actionsreview.go`
- Modify: `internal/automations/registry.go` (register) + `internal/automations/catalog.go` (purpose)
- Test: `internal/automations/actionsreview_test.go`; bump `internal/automations/registry_test.go` (21→22) + `internal/mcp/tools_more_test.go` automations count (21→22)

**Interfaces:**
- Consumes: `db.NotesUpdatedBefore(ctx, Queryer2, beforeDate string) ([]db.NoteStamp{ID,Path,Title,Updated}, error)`; `db.ListActions(ctx, q, db.ListActionsOpts{State:"open"})`; `db.Action` (SourcePath/Text/Due/Hash); `loadProposalMemory`/`saveProposalMemory`; `scannableNote`; `stripExt`; `review.Load`; `rc.Vault.Append`; `rc.Config.Actions.StaleAfterDaysOr()`; `rc.now()`.
- Produces: `ActionsReview` with `Name() string { return "actions-review" }`.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/actionsreview_test.go`:

```go
package automations

import (
	"context"
	"strings"
	"testing"
)

func TestActionsReviewProposesStaleUndated(t *testing.T) {
	ctx := context.Background()
	// A stale note (old updated) with an open undated task, and a fresh note with one.
	rc, _ := newRC(t, map[string]string{
		"01-Projects/old.md":   "---\nupdated: 2000-01-01\n---\n- [ ] tidy backlog\n",
		"01-Projects/fresh.md": "---\nupdated: 2099-01-01\n---\n- [ ] recent task\n",
		"01-Projects/dated.md": "---\nupdated: 2000-01-01\n---\n- [ ] has a due 📅 2099-01-01\n",
	})
	mustReindex(t, rc)

	res, err := ActionsReview{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedTokens != 0 {
		t.Errorf("zero-model automation must report 0 tokens, got %d", res.EstimatedTokens)
	}
	q := readReviewQueue(t, rc)
	if !strings.Contains(q, `stalled action "tidy backlog" in [[01-Projects/old]]`) {
		t.Errorf("stale undated task not proposed:\n%s", q)
	}
	if strings.Contains(q, "recent task") {
		t.Error("fresh note's task must not be proposed")
	}
	if strings.Contains(q, "has a due") {
		t.Error("dated task must not be proposed")
	}
	// re-run → proposal-memory silenced (no second copy)
	if _, err := (ActionsReview{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	q2 := readReviewQueue(t, rc)
	if strings.Count(q2, "tidy backlog") != 1 {
		t.Errorf("re-run must not re-propose:\n%s", q2)
	}
}
```

> `newRC`, `mustReindex`, `readReviewQueue` are the shared automations-test helpers (used by `pulse_test.go`/`dedup_test.go`). If `readReviewQueue` isn't exported by the test package, grep `func readReviewQueue` (it's in `pulse_test.go`) — same package, reusable.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestActionsReview -v`
Expected: FAIL — `undefined: ActionsReview`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/automations/actionsreview.go`:

```go
package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/review"
)

type ActionsReview struct{}

func (ActionsReview) Name() string    { return "actions-review" }
func (ActionsReview) Essential() bool { return false }

const (
	actionsReviewState = "actions-review/proposed"
	staleMaxProposals  = 10
)

func (a ActionsReview) staleCandidates(ctx context.Context, rc RunCtx) ([]db.Action, map[string]string, error) {
	cutoff := rc.now().UTC().AddDate(0, 0, -rc.Config.Actions.StaleAfterDaysOr()).Format("2006-01-02")
	stale, err := db.NotesUpdatedBefore(ctx, rc.DB, cutoff)
	if err != nil {
		return nil, nil, err
	}
	updated := map[string]string{}
	for _, n := range stale {
		if scannableNote(n.Path) {
			updated[n.Path] = n.Updated
		}
	}
	open, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})
	if err != nil {
		return nil, nil, err
	}
	var cands []db.Action
	for _, act := range open {
		if act.Due != "" {
			continue // only undated
		}
		if _, ok := updated[act.SourcePath]; !ok {
			continue // note not stale
		}
		cands = append(cands, act)
	}
	return cands, updated, nil
}

func (a ActionsReview) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	cands, _, err := a.staleCandidates(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	var sb strings.Builder
	for _, c := range cands {
		sb.WriteString(c.Hash)
		sb.WriteByte('\n')
	}
	cursor := fmt.Sprintf("stale:%s:%s", weekStart(rc).Format("2006-01-02"), hashShort(sb.String()))
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no new stale actions this week"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d stale candidate(s)", len(cands)), Cursor: cursor}, nil
}

func (a ActionsReview) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	cands, updated, err := a.staleCandidates(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	// Pending stalled items already queued — never duplicate.
	pending := map[string]bool{}
	if items, lerr := review.Load(ctx, rc.Vault); lerr == nil {
		for _, it := range items {
			if !it.Checked && it.Kind == "stalled" {
				pending[it.Note+"\x00"+it.Target] = true
			}
		}
	}
	proposed := loadProposalMemory(ctx, rc, actionsReviewState)
	today := rc.now().UTC()

	type prop struct{ hash, line string }
	var props []prop
	for _, c := range cands {
		if proposed[c.Hash] {
			continue
		}
		note := stripExt(c.SourcePath)
		if pending[note+"\x00"+c.Text] {
			continue
		}
		age := int(today.Sub(parseDay(updated[c.SourcePath])).Hours() / 24)
		props = append(props, prop{
			hash: c.Hash,
			line: fmt.Sprintf("stalled action %q in [[%s]] (%dd) — still relevant?", c.Text, note, age),
		})
	}
	sort.Slice(props, func(i, j int) bool { return props[i].line < props[j].line })
	if len(props) > staleMaxProposals {
		props = props[:staleMaxProposals]
	}

	changes := make([]string, 0, len(props))
	queue := make([]string, 0, len(props))
	for _, p := range props {
		changes = append(changes, p.line)
		queue = append(queue, "- [ ] "+p.line)
	}
	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would propose %d stale action(s)", len(changes)), Changes: changes}, nil
	}
	if len(queue) > 0 {
		header := fmt.Sprintf("\n## Stale actions (%s)\n", today.Format("2006-01-02 15:04"))
		if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
			return RunResult{}, aerr
		}
		for _, p := range props {
			proposed[p.hash] = true
		}
		saveProposalMemory(ctx, rc, actionsReviewState, proposed)
	}
	return RunResult{Summary: fmt.Sprintf("actions-review proposed %d stale action(s)", len(changes)), Changes: changes}, nil
}

// parseDay turns a YYYY-MM-DD stamp into a time; zero on failure.
func parseDay(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}
```

In `internal/automations/registry.go`, add to the `reg` map:

```go
		ActionsReview{}.Name():     ActionsReview{},
```

In `internal/automations/registry_test.go`, add `"actions-review"` to the `want` slice (21 → 22).

In `internal/automations/catalog.go`, add to `purposes`:

```go
	"actions-review":      "Weekly (zero-model, T5, off by default): proposes open, undated actions in notes untouched for > actions.stale_after_days (default 30) to the review queue as 'stalled action …' items. Accepting demotes the task to Someday/Maybe (tags the source line #someday); dismiss silences it (proposal memory). No model call.",
```

In `internal/mcp/tools_more_test.go`, bump the automations-count assertion (`if len(list.Automations) != 21` → `22`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/automations/ ./internal/mcp/ -run 'TestActionsReview|TestRegistry|TestCatalog|TestAutomationsList' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/actionsreview.go internal/automations/registry.go internal/automations/registry_test.go internal/automations/catalog.go internal/automations/actionsreview_test.go internal/mcp/tools_more_test.go
git commit -m "feat(T5): actions-review automation — zero-model stale-action sweep (FR-167)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Doctor `actions-review` advisory check

**Files:**
- Modify: `internal/core/doctor.go` (add `actionsReviewCheck` + wire into `Doctor`)
- Test: `internal/core/actionsreview_doctor_test.go`

**Interfaces:**
- Consumes: `config.Profile`; the `Check{Name,Status,Detail}`/`StatusOK` types; `mergeCheck` template (`doctor.go:276`).
- Produces: an advisory `actions-review` check.

- [ ] **Step 1: Write the failing test**

Create `internal/core/actionsreview_doctor_test.go`:

```go
package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestActionsReviewCheck(t *testing.T) {
	off := actionsReviewCheck(config.Profile{})
	if off.Status != StatusOK || off.Name != "actions-review" {
		t.Fatalf("off check = %+v", off)
	}
	on := actionsReviewCheck(config.Profile{
		Automations: map[string]config.Automation{"actions-review": {Enabled: true}},
		Actions:     config.ActionsConfig{StaleAfterDays: 21},
	})
	if !strings.Contains(on.Detail, "21") {
		t.Errorf("enabled detail should mention the threshold: %q", on.Detail)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestActionsReviewCheck -v`
Expected: FAIL — `undefined: actionsReviewCheck`.

- [ ] **Step 3: Write minimal implementation**

In `internal/core/doctor.go`, add (near `mergeCheck`):

```go
// actionsReviewCheck reports the T5 stale-action sweep. Advisory (always StatusOK):
// zero-model, off by default; accept demotes to #someday (never deletes).
func actionsReviewCheck(p config.Profile) Check {
	const name = "actions-review"
	auto, ok := p.Automations["actions-review"]
	if !ok || !auto.Enabled {
		return Check{name, StatusOK, "actions-review off (stale-action sweep; enable to nudge forgotten tasks)"}
	}
	return Check{name, StatusOK, fmt.Sprintf("actions-review active (open undated actions in notes untouched > %dd → review queue; accept → #someday)", p.Actions.StaleAfterDaysOr())}
}
```

Wire it into `Doctor(...)` in the profile-scoped block (next to `mergeCheck(p)`):

```go
		checks = append(checks, actionsReviewCheck(p))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestActionsReviewCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/actionsreview_doctor_test.go
git commit -m "feat(T5): doctor actions-review advisory check

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Full green + docs + live smoke

**Files:**
- Modify: `docs/02-architecture.md` (ADR-034 extension note), `docs/03-requirements.md` (FR-167/168), `docs/04-data-model-and-config.md` (`actions:` block), `docs/06-component-automation-engine.md` (automation entry), `docs/16-roadmap-1.2.5.md` (T5 built), `CLAUDE.md` (FR range → FR-168), `README.md` (automation count 21→22)

**Interfaces:** none (verification + docs).

- [ ] **Step 1: Whole-suite green + lint**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./... && golangci-lint run`
Expected: all pass. Fix drift (`gofmt -w`; watch `strings.Cut` hints; unused imports).

- [ ] **Step 2: Write the docs**

- `docs/02-architecture.md`: extend the **ADR-034** entry with a sentence — the "byte-precise, user-initiated, never-agentic checkbox-line edit" now covers both *completion* (`CompleteAction`) and *demotion* (`TagAction`, adding `#someday` via the review-queue accept); both additive, reversible, review-queue/dashboard only.
- `docs/03-requirements.md`: FR-167 (the zero-model `actions-review` sweep — stale = open undated action whose source note's `updated` predates `today − actions.stale_after_days`; new `stalled` review kind; deduped by proposal memory; off by default) and FR-168 (Accept → `#someday` via `vault.TagAction`; dismiss silences).
- `docs/04-data-model-and-config.md`: document the `actions: { stale_after_days }` block + the `actions-review` automation entry.
- `docs/06-component-automation-engine.md`: add an `actions-review` entry to the standard-automations list.
- `docs/16-roadmap-1.2.5.md`: mark **T5** ✅ BUILT 2026-07-10 (FR-167/168, no ADR).
- `CLAUDE.md`: `docs/03` FR range → FR-168; update the 1.2.5 doc-pack line (T5 shipped; remaining T6).
- `README.md`: automation count 21 → 22 (if stated); add `actions-review` to any list.

- [ ] **Step 3: Live smoke (isolated AXON_HOME — never :7777)**

```bash
go build -o /tmp/axon-t5 ./cmd/axon
SMOKE=$(mktemp -d)
# T1-shape config with actions-review enabled + actions.stale_after_days.
# Seed an OLD note (updated far in the past via frontmatter, or `touch -t`) with an open undated task.
/tmp/axon-t5 init --config "$SMOKE/config.yaml"
mkdir -p "$SMOKE/vault/01-Projects"
printf -- '---\nupdated: 2000-01-01\n---\n- [ ] tidy the backlog\n' > "$SMOKE/vault/01-Projects/old.md"
touch -t 200001010000 "$SMOKE/vault/01-Projects/old.md"   # mtime → stale updated stamp
/tmp/axon-t5 reindex --config "$SMOKE/config.yaml"
/tmp/axon-t5 run actions-review --config "$SMOKE/config.yaml"
grep -n "stalled action" "$SMOKE/vault/.axon/review-queue.md"
/tmp/axon-t5 run actions-review --config "$SMOKE/config.yaml"   # second run → proposal-memory skip
grep -c "tidy the backlog" "$SMOKE/vault/.axon/review-queue.md"  # still 1
```

Expected: the first run appends `- [ ] stalled action "tidy the backlog" in [[01-Projects/old]] (Nd) — still relevant?`; the second run adds no duplicate (proposal memory). Accept path is unit-covered (`review` + `vault.TagAction` tests). `env -u FORCE_COLOR`; skip scratch cleanup (GateGuard).

- [ ] **Step 4: Commit docs**

```bash
git add docs/ CLAUDE.md README.md
git commit -m "docs(T5): FR-167/168, actions-review automation + #someday demotion, roadmap T5 built

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Read one neighbor first:** `internal/automations/dedup.go` (`MergeProposals` — the clone target), `internal/review/review.go` (the `merge` kind), `internal/vault/actions.go` (`CompleteAction` — `TagAction`'s sibling), and the `newRC`/`mustReindex`/`readReviewQueue` test helpers.
- **The staleness signal is the note's `updated`, not the action's** — there is no per-action created date. `db.NotesUpdatedBefore` gives the stale notes; only **undated** open actions in them are swept.
- **Propose-once** — proposal memory keyed by the action hash; a dismissal is never re-proposed (no ladder). Skip actions already pending in the queue.
- **Accept is the only tag path** — `#someday` is applied solely by a human review-queue Accept via `vault.TagAction`; no agent/MCP tagging.
- **+1 automation bumps THREE assertions** — `registry_test` want-list, `mcp/tools_more_test` automations count, and the `catalog` purpose map must all gain `actions-review` (the T2 gotcha).
- **GateGuard:** first Write/Edit/Bash each turn triggers a fact-force preamble (comply tersely); `git commit --amend`/`rm -rf` blocked (follow-up commits; leave scratch dirs).
```
