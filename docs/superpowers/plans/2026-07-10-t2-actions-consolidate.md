# T2 — Actions Consolidation Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A zero-model `actions-consolidate` automation that renders the T1 action index into an `axon:actions` managed block in `01-Projects/Actions.md` (GTD sections), plus a deterministic task counter on the heartbeat.

**Architecture:** A pure renderer (`renderActionsSections(rows, today)`) turns `[]db.Action` into the GTD-ordered block body; `ActionsConsolidate` (cloned from `project-pulse`) wraps it with a change-gate (hash of the rendered body) and a wikilink-safe `Patch`. FR-161 adds an `openTaskCounts` helper to the heartbeat status line. Zero model calls, one managed-block write.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, the existing `internal/automations` engine.

**Spec:** `docs/superpowers/specs/2026-07-10-t2-actions-consolidate-design.md`

## Global Constraints

- **Cardinal rule 1:** No Claude call bypasses the token manager. *T2 is `model: none` — `runModel` is never called, `EstimatedTokens: 0`, no ledger entry.*
- **Cardinal rule 2:** No vault mutation outside wikilink-safe ops. *The only write is `rc.Vault.Patch` into the `axon:actions` block (+ `Create` of the stub on first run). Human prose above the block is never touched; no Move/delete.*
- **S8:** all-off still useful. *Disabling `actions-consolidate` removes only `Actions.md`; `axon actions` and the index are unaffected.*
- **S9:** vault rebuilds the DB. *The automation reads the derived index and writes a projection note; it stores no authoritative state.*
- **FR-31 change gate:** an unchanged projection (same tasks, same day, same buckets) is a hash match → skip, no write, no event.
- Go: `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green. Wrap errors with `%w`. Propagate `context.Context`. Use `rc.now()` (injectable clock), never `time.Now()`.
- Run test suites with `env -u FORCE_COLOR` (the ambient shell exports `FORCE_COLOR=3`).
- Lines are **plain-list references** (`- text — [[source]] · 📅 due ⏫`), never `- [ ]` checkboxes (constitution §3).
- Note path `01-Projects/Actions.md`; block name `actions`; "This week" = rolling 7 days; enabled by default; daily at 07:00; `model: none`.
- FR IDs: FR-160 (automation), FR-161 (heartbeat counter). No new ADR.
- The ambient GateGuard hook fires a fact-force preamble on the first Write/Edit/Bash each turn and blocks `git commit --amend` / `rm -rf`; comply tersely, use follow-up commits, skip scratch cleanup.

---

### Task 1: The pure renderer (FR-160)

**Files:**
- Create: `internal/automations/actions_consolidate.go`
- Test: `internal/automations/actions_consolidate_test.go`

**Interfaces:**
- Consumes (existing in package): `stripExt(path string) string`; `db.Action` (fields State/Due/Scheduled/Start/DoneDate/SourcePath/Section/Text/Priority/Project/Contexts/Tags/Archived); `actions.Action`, `actions.State`, `actions.Bucket(a actions.Action, today time.Time) string`.
- Produces (for Task 2): `actionFromRow(r db.Action) actions.Action`; `renderActionsSections(rows []db.Action, today time.Time) (body string, openTotal int)`; `actionsNoteStub() string`; `actionsNotePath`/`actionsBlock` consts.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/actions_consolidate_test.go`:

```go
package automations

import (
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/db"
)

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestRenderActionsSections(t *testing.T) {
	today := day("2026-07-10")
	rows := []db.Action{
		{SourcePath: "01-Projects/work.md", Section: "Sprint", Text: "fix login", State: "open", Checkbox: " ", Due: "2000-01-01", Priority: "high", Project: "Auth"},
		{SourcePath: "Daily/2026-07-10.md", Text: "standup notes", State: "open", Checkbox: " ", Due: "2026-07-10"},
		{SourcePath: "01-Projects/work.md", Text: "write RFC", State: "open", Checkbox: " ", Due: "2026-07-14"},   // within 7d → This week
		{SourcePath: "01-Projects/work.md", Text: "refactor later", State: "open", Checkbox: " "},                 // no date → Next actions
		{SourcePath: "01-Projects/work.md", Text: "hear from legal", State: "open", Checkbox: " ", Tags: []string{"waiting"}, Due: "2026-07-01"}, // waiting outranks overdue
		{SourcePath: "Ideas.md", Text: "learn rust", State: "open", Checkbox: " ", Tags: []string{"someday"}},
		{SourcePath: "01-Projects/work.md", Text: "ship v2", State: "done", Checkbox: "x", DoneDate: "2026-07-09"}, // done this week
		{SourcePath: "01-Projects/work.md", Text: "ancient done", State: "done", Checkbox: "x", DoneDate: "2026-01-01"}, // outside 7d window
		{SourcePath: "01-Projects/work.md", Text: "scrapped", State: "cancelled", Checkbox: "-"},                  // omitted
		{SourcePath: "04-Archive/old.md", Text: "archived", State: "open", Checkbox: " ", Archived: true},         // omitted
	}
	body, total := renderActionsSections(rows, today)

	// Open total excludes done/cancelled/archived: overdue-none + today1 + week1 + next1 + waiting1 + someday1 = 5.
	if total != 5 {
		t.Fatalf("open total = %d, want 5\n%s", total, body)
	}
	sec := func(h string) string { // slice of body under heading h up to the next "## "
		i := strings.Index(body, h)
		if i < 0 {
			t.Fatalf("missing section %q:\n%s", h, body)
		}
		rest := body[i+len(h):]
		if j := strings.Index(rest, "\n## "); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	if !strings.Contains(sec("## 📅 Today"), "standup notes") {
		t.Error("today section wrong")
	}
	if !strings.Contains(sec("## ⏳ This week"), "write RFC") {
		t.Error("this-week section wrong")
	}
	if !strings.Contains(sec("## ▶ Next actions"), "refactor later") {
		t.Error("next-actions section wrong")
	}
	if !strings.Contains(sec("## 🕓 Waiting for"), "hear from legal") {
		t.Error("waiting task (with due) must be in Waiting, not Overdue/This week")
	}
	if !strings.Contains(sec("## 💭 Someday"), "learn rust") {
		t.Error("someday section wrong")
	}
	if !strings.Contains(sec("## ✅ Done this week"), "ship v2") || strings.Contains(body, "ancient done") {
		t.Error("done-this-week window wrong")
	}
	if strings.Contains(body, "scrapped") || strings.Contains(body, "archived") {
		t.Error("cancelled/archived must be omitted")
	}
	// The overdue task lands under Overdue, in reference format (NOT a checkbox),
	// carrying [[source]] + due + priority glyph.
	if !strings.Contains(sec("## 🔴 Overdue"), "fix login") {
		t.Error("overdue task misfiled")
	}
	if !strings.Contains(body, "- fix login — [[01-Projects/work]]") || !strings.Contains(body, "📅 2000-01-01") || !strings.Contains(body, "⏫") {
		t.Errorf("reference line format wrong:\n%s", body)
	}
	if strings.Contains(body, "- [ ]") || strings.Contains(body, "- [x]") {
		t.Error("projection must contain NO checkboxes")
	}
}

func TestRenderActionsSectionsEmpty(t *testing.T) {
	body, total := renderActionsSections(nil, day("2026-07-10"))
	if total != 0 {
		t.Errorf("empty index open total = %d, want 0", total)
	}
	// Every GTD section still renders, each showing the _none_ placeholder.
	for _, h := range []string{"## 🔴 Overdue", "## 📅 Today", "## ⏳ This week", "## ▶ Next actions", "## 🕓 Waiting for", "## 💭 Someday / Maybe", "## ✅ Done this week"} {
		if !strings.Contains(body, h) {
			t.Errorf("missing section %q", h)
		}
	}
	if !strings.Contains(body, "_none_") {
		t.Error("empty sections should render _none_")
	}
}

func TestActionsNoteStub(t *testing.T) {
	s := actionsNoteStub()
	if !strings.Contains(s, "type: actions") || !strings.Contains(s, "never overwrites") || !strings.Contains(s, "axon:actions") {
		t.Errorf("stub missing frontmatter/preamble:\n%s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestRenderActionsSections|TestActionsNoteStub' -v`
Expected: FAIL — `undefined: renderActionsSections` / `undefined: actionsNoteStub`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/automations/actions_consolidate.go`:

```go
package automations

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
)

const (
	actionsNotePath = "01-Projects/Actions.md"
	actionsBlock    = "actions"
)

func actionFromRow(r db.Action) actions.Action {
	return actions.Action{
		SourcePath: r.SourcePath, LineNo: r.LineNo, Section: r.Section, Text: r.Text, Raw: r.Raw,
		State: actions.State(r.State), Checkbox: r.Checkbox, Priority: r.Priority,
		Due: r.Due, Scheduled: r.Scheduled, Start: r.Start, DoneDate: r.DoneDate,
		Project: r.Project, Contexts: r.Contexts, Tags: r.Tags, Archived: r.Archived,
	}
}

func priorityGlyph(p string) string {
	switch p {
	case "highest":
		return "🔺"
	case "high":
		return "⏫"
	case "medium":
		return "🔼"
	case "low":
		return "🔽"
	case "lowest":
		return "⏬"
	}
	return ""
}

// actionRefLine renders one action as a plain-list REFERENCE (never a checkbox).
func actionRefLine(a actions.Action) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(a.Text)
	b.WriteString(" — [[")
	b.WriteString(stripExt(a.SourcePath))
	b.WriteString("]]")
	if a.Section != "" {
		b.WriteString(" · ")
		b.WriteString(a.Section)
	}
	if a.Due != "" {
		b.WriteString(" · 📅 ")
		b.WriteString(a.Due)
	}
	if g := priorityGlyph(a.Priority); g != "" {
		b.WriteString(" ")
		b.WriteString(g)
	}
	return b.String()
}

func sortByDueThenText(as []actions.Action) {
	sort.SliceStable(as, func(i, j int) bool {
		di, dj := as[i].Due, as[j].Due
		if (di == "") != (dj == "") {
			return di != "" // dated first, empty last
		}
		if di != dj {
			return di < dj
		}
		return as[i].Text < as[j].Text
	})
}

func renderSection(sb *strings.Builder, heading string, as []actions.Action) {
	sb.WriteString("## ")
	sb.WriteString(heading)
	sb.WriteString("\n")
	if len(as) == 0 {
		sb.WriteString("_none_\n\n")
		return
	}
	sortByDueThenText(as)
	for _, a := range as {
		sb.WriteString(actionRefLine(a))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

// renderActionsSections renders the GTD block body (NO footer — the caller adds
// the timestamped footer so it never affects the change-gate hash). Returns the
// body and the count of OPEN actions (done/cancelled/archived excluded).
func renderActionsSections(rows []db.Action, today time.Time) (string, int) {
	tStr := today.Format("2006-01-02")
	weekEnd := today.AddDate(0, 0, 7).Format("2006-01-02")
	doneCut := today.AddDate(0, 0, -7).Format("2006-01-02")

	var overdue, todayA, thisWeek, waiting, someday, doneWeek []actions.Action
	nextByProject := map[string][]actions.Action{}
	var projOrder []string
	open := 0
	for _, r := range rows {
		if r.Archived {
			continue
		}
		a := actionFromRow(r)
		switch actions.Bucket(a, today) {
		case "cancelled":
			continue
		case "done":
			if r.DoneDate >= doneCut {
				doneWeek = append(doneWeek, a)
			}
			continue
		case "overdue":
			overdue = append(overdue, a)
		case "today":
			todayA = append(todayA, a)
		case "waiting":
			waiting = append(waiting, a)
		case "someday":
			someday = append(someday, a)
		default: // next | scheduled
			if a.Due != "" && a.Due > tStr && a.Due <= weekEnd {
				thisWeek = append(thisWeek, a)
			} else {
				proj := a.Project
				if proj == "" {
					proj = stripExt(a.SourcePath)
				}
				if _, seen := nextByProject[proj]; !seen {
					projOrder = append(projOrder, proj)
				}
				nextByProject[proj] = append(nextByProject[proj], a)
			}
		}
		open++
	}

	var sb strings.Builder
	renderSection(&sb, "🔴 Overdue", overdue)
	renderSection(&sb, "📅 Today", todayA)
	renderSection(&sb, "⏳ This week", thisWeek)

	// Next actions: grouped by project, then context, then text.
	sb.WriteString("## ▶ Next actions\n")
	if len(projOrder) == 0 {
		sb.WriteString("_none_\n\n")
	} else {
		sort.Strings(projOrder)
		for _, proj := range projOrder {
			group := nextByProject[proj]
			sort.SliceStable(group, func(i, j int) bool {
				ci, cj := firstContext(group[i]), firstContext(group[j])
				if ci != cj {
					return ci < cj
				}
				return group[i].Text < group[j].Text
			})
			sb.WriteString("**[[")
			sb.WriteString(proj)
			sb.WriteString("]]**\n")
			for _, a := range group {
				sb.WriteString(actionRefLine(a))
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	renderSection(&sb, "🕓 Waiting for", waiting)
	renderSection(&sb, "💭 Someday / Maybe", someday)

	// Done this week: an Obsidian collapsible callout (count + list).
	sb.WriteString("## ✅ Done this week\n")
	if len(doneWeek) == 0 {
		sb.WriteString("_none_\n")
	} else {
		sortByDueThenText(doneWeek)
		fmt.Fprintf(&sb, "> [!success]- %d completed this week\n", len(doneWeek))
		for _, a := range doneWeek {
			fmt.Fprintf(&sb, "> - %s — [[%s]]\n", a.Text, stripExt(a.SourcePath))
		}
	}
	return strings.TrimRight(sb.String(), "\n"), open
}

func firstContext(a actions.Action) string {
	if len(a.Contexts) > 0 {
		return a.Contexts[0]
	}
	return "~" // no-context sorts last
}

func actionsNoteStub() string {
	return "---\ntitle: \"Actions\"\ntype: actions\ntags: [actions]\n---\n\n" +
		"> AXON maintains your consolidated action list below inside the `axon:actions` block.\n" +
		"> These are references — tick tasks off in their source notes (linked), not here.\n" +
		"> Write your own notes above this line — AXON never overwrites them.\n\n"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestRenderActionsSections|TestActionsNoteStub' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/actions_consolidate.go internal/automations/actions_consolidate_test.go
git commit -m "feat(T2): GTD actions projection renderer (FR-160)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: The automation — `DetectChange` / `Run` (FR-160)

**Files:**
- Modify: `internal/automations/actions_consolidate.go` (add struct + `buildActionsBody` + `DetectChange` + `Run`)
- Test: `internal/automations/actions_consolidate_test.go` (add automation tests)

**Interfaces:**
- Consumes: `renderActionsSections`, `actionsNoteStub`, `actionsNotePath`, `actionsBlock` (Task 1); `db.ListActions(ctx, q, db.ListActionsOpts{IncludeAll: true})`; `RunCtx` (fields `DB`, `Vault *vault.FS`, `DryRun`, `LastCursor`, `Now`, `now()`); `Change`/`RunResult`; `hashShort(s string) string`; `rc.Vault.Exists`/`Create`/`Patch`.
- Produces (for Task 3): `ActionsConsolidate` with `Name() string { return "actions-consolidate" }`.

- [ ] **Step 1: Write the failing test**

Add to `internal/automations/actions_consolidate_test.go`:

```go
import "context" // add to the existing import block

func seedActions(t *testing.T, rc RunCtx, rows []db.Action) {
	t.Helper()
	if err := db.ReplaceActions(context.Background(), rc.DB, rows); err != nil {
		t.Fatal(err)
	}
}

func TestActionsConsolidateWritesBlock(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{})
	seedActions(t, rc, []db.Action{
		{Hash: "h1", SourcePath: "01-Projects/work.md", Text: "fix login", State: "open", Checkbox: " ", Due: "2000-01-01", Updated: "u"},
	})
	res, err := ActionsConsolidate{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedTokens != 0 {
		t.Errorf("zero-model automation must report 0 tokens, got %d", res.EstimatedTokens)
	}
	n, err := rc.Vault.Read(ctx, actionsNotePath)
	if err != nil {
		t.Fatalf("actions note not created: %v", err)
	}
	for _, want := range []string{"axon:actions:start", "never overwrites them", "## 🔴 Overdue", "fix login — [[01-Projects/work]]"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("block missing %q:\n%s", want, n.Body)
		}
	}
	if strings.Contains(n.Body, "- [ ]") {
		t.Error("projection must have no checkboxes")
	}
}

func TestActionsConsolidateChangeGate(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{})

	// Empty index + no note → not changed.
	ch, err := ActionsConsolidate{}.DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Changed {
		t.Error("empty index + no note should be Changed:false")
	}

	seedActions(t, rc, []db.Action{{Hash: "h1", SourcePath: "a.md", Text: "x", State: "open", Checkbox: " ", Updated: "u"}})
	ch, _ = ActionsConsolidate{}.DetectChange(ctx, rc)
	if !ch.Changed || ch.Cursor == "" {
		t.Fatalf("new task should be Changed:true with a cursor: %+v", ch)
	}
	// Same index, same day → the returned cursor, fed back, is a no-op.
	rc.LastCursor = ch.Cursor
	ch2, _ := ActionsConsolidate{}.DetectChange(ctx, rc)
	if ch2.Changed {
		t.Error("unchanged index should be Changed:false when LastCursor matches")
	}
}

func TestActionsConsolidateDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{})
	seedActions(t, rc, []db.Action{{Hash: "h1", SourcePath: "a.md", Text: "x", State: "open", Checkbox: " ", Updated: "u"}})
	rc.DryRun = true
	if _, err := (ActionsConsolidate{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if rc.Vault.Exists(actionsNotePath) {
		t.Error("dry-run must not create the note")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestActionsConsolidate -v`
Expected: FAIL — `undefined: ActionsConsolidate`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/automations/actions_consolidate.go` (add `"context"` to the imports):

```go
// ActionsConsolidate renders the whole action index into 01-Projects/Actions.md.
// Zero-model, enabled by default. Change-gated on the rendered projection so a
// day with no visible change writes nothing.
type ActionsConsolidate struct{}

func (ActionsConsolidate) Name() string   { return "actions-consolidate" }
func (ActionsConsolidate) Essential() bool { return false }

// buildActionsBody reads the index and renders the block body (no footer).
func buildActionsBody(ctx context.Context, rc RunCtx) (string, int, error) {
	rows, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		return "", 0, err
	}
	body, total := renderActionsSections(rows, rc.now())
	return body, total, nil
}

func (a ActionsConsolidate) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	body, total, err := buildActionsBody(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	if total == 0 && !rc.Vault.Exists(actionsNotePath) {
		return Change{Changed: false, Reason: "no actions yet"}, nil
	}
	cursor := "actions:" + hashShort(body)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "actions unchanged"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d open action(s)", total), Cursor: cursor}, nil
}

func (a ActionsConsolidate) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	body, total, err := buildActionsBody(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	if rc.DryRun {
		return RunResult{
			Summary: fmt.Sprintf("would consolidate %d open action(s) → %s", total, actionsNotePath),
			Changes: []string{actionsNotePath + ": axon:actions (dry-run)"},
		}, nil
	}
	footer := fmt.Sprintf("_generated %s UTC · %d open_", rc.now().UTC().Format("2006-01-02 15:04"), total)
	block := strings.TrimSpace(body + "\n\n" + footer)

	if !rc.Vault.Exists(actionsNotePath) {
		if _, cerr := rc.Vault.Create(actionsNotePath, actionsNoteStub()); cerr != nil {
			return RunResult{}, cerr
		}
	}
	if perr := rc.Vault.Patch(ctx, actionsNotePath, actionsBlock, block); perr != nil {
		return RunResult{}, perr
	}
	return RunResult{
		Summary: fmt.Sprintf("actions consolidated (%d open) → %s", total, actionsNotePath),
		Changes: []string{actionsNotePath + ": axon:actions updated"},
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestActionsConsolidate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/actions_consolidate.go internal/automations/actions_consolidate_test.go
git commit -m "feat(T2): actions-consolidate automation — change-gate + wikilink-safe write (FR-160)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Register the automation

**Files:**
- Modify: `internal/automations/registry.go` (one map line)
- Modify: `internal/automations/registry_test.go` (want-list 20→21)

**Interfaces:**
- Consumes: `ActionsConsolidate` (Task 2).

- [ ] **Step 1: Update the want-list test (make it fail)**

In `internal/automations/registry_test.go`, add `"actions-consolidate"` to the `want []string` slice (e.g. after `"merge-proposals"`). This makes `len(reg) != len(want)` fail until the registry is updated.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestRegistry -v`
Expected: FAIL — `registry has 20 automations, want 21`.

- [ ] **Step 3: Register the automation**

In `internal/automations/registry.go`, add one line to the `reg` map literal (next to `MergeProposals{}.Name(): MergeProposals{},`):

```go
		ActionsConsolidate{}.Name(): ActionsConsolidate{},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestRegistry -v`
Expected: PASS (registry has 21).

- [ ] **Step 5: Commit**

```bash
git add internal/automations/registry.go internal/automations/registry_test.go
git commit -m "feat(T2): register actions-consolidate automation

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Config seeds (enabled by default)

**Files:**
- Modify: `internal/config/starter.go` (the embedded starter written by `axon init`)
- Modify: `axon.config.example.yaml`

**Interfaces:** none (config data).

- [ ] **Step 1: Add the automation entry to both seeds**

In `internal/config/starter.go`, in the `automations:` block (next to the `merge-proposals:` line ~94-95), add:

```
      actions-consolidate: { enabled: true,  schedule: "0 7 * * *",  model: none,      budget_tokens: 0 }
```

In `axon.config.example.yaml`, in the same `automations:` block (~line 155-156), add the identical line (matching the file's indentation/alignment).

- [ ] **Step 2: Verify config still validates**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestConfigValidateOnExampleConfig -v`
Expected: PASS (the example config parses with the new automation entry).

Also confirm the starter parses:

Run: `env -u FORCE_COLOR go test ./internal/config/ 2>&1 | tail -3`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/config/starter.go axon.config.example.yaml
git commit -m "feat(T2): seed actions-consolidate enabled by default (daily, model:none)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Heartbeat task counter (FR-161)

**Files:**
- Modify: `internal/automations/model.go` (`Heartbeat.Run` — add counter to `line` + `facts`; add `openTaskCounts`)
- Modify: `internal/automations/standard_test.go` (two pinned strings + a counter assertion)

**Interfaces:**
- Consumes: `db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})`; `actions.Bucket`; `actionFromRow` (Task 1); `rc.now()`.
- Produces: an `openTaskCounts(ctx, rc) (open, overdue int)` helper.

- [ ] **Step 1: Update the pinned heartbeat tests (make them fail)**

In `internal/automations/standard_test.go`:
- Line ~387: change `plain := "inbox: 1 · budget day 0% week 0%"` to `plain := "inbox: 1 · tasks: 0 open · budget day 0% week 0%"`.
- Add a new test asserting the counter reflects the index. Append:

```go
func TestHeartbeatTaskCounter(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{})
	if err := db.ReplaceActions(ctx, rc.DB, []db.Action{
		{Hash: "h1", SourcePath: "a.md", Text: "overdue", State: "open", Checkbox: " ", Due: "2000-01-01", Updated: "u"},
		{Hash: "h2", SourcePath: "a.md", Text: "future", State: "open", Checkbox: " ", Due: "2999-01-01", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := Heartbeat{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "tasks: 2 open (1 overdue)") {
		t.Errorf("heartbeat line missing task counter: %q", res.Summary)
	}
}
```

(The `TestHeartbeatWritesDailyNote` assertion on `"inbox: 1"` at line ~138 still passes — `inbox: 1` remains a substring of the new line.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestHeartbeat' -v`
Expected: FAIL — `TestHeartbeatTaskCounter` (no counter) and `TestHeartbeatSynthesis` (plain string mismatch).

- [ ] **Step 3: Wire the counter into `Heartbeat.Run`**

In `internal/automations/model.go`, replace the `line := ...` assignment at line ~152 with:

```go
	open, overdue := openTaskCounts(ctx, rc)
	taskClause := fmt.Sprintf(" · tasks: %d open", open)
	if overdue > 0 {
		taskClause += fmt.Sprintf(" (%d overdue)", overdue)
	}
	line := fmt.Sprintf("inbox: %d%s · budget day %.0f%% week %.0f%%%s", inbox, taskClause, st.Day.Pct, st.Week.Pct, guardSuffix(st))
```

Extend the noteworthy gate + `facts` (line ~160-161) so overdue tasks are worth a synthesis and the model can mention them:

```go
	if modelKey != "" && (inbox > 0 || pendingReview > 0 || overdue > 0 || guardSuffix(st) != "") {
		facts := fmt.Sprintf("%s\ninbox items awaiting triage: %d\nreview-queue proposals pending: %d\nopen tasks: %d (%d overdue)", line, inbox, pendingReview, open, overdue)
```

Add the helper (near the other heartbeat helpers in `model.go`):

```go
// openTaskCounts reports open + overdue action counts from the derived index
// (FR-161). Best-effort: the essential heartbeat never fails on a DB hiccup.
func openTaskCounts(ctx context.Context, rc RunCtx) (open, overdue int) {
	rows, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})
	if err != nil {
		return 0, 0
	}
	today := rc.now()
	for _, r := range rows {
		open++
		if actions.Bucket(actionFromRow(r), today) == "overdue" {
			overdue++
		}
	}
	return open, overdue
}
```

Ensure `internal/automations/model.go` imports `"github.com/jandro-es/axon/internal/actions"` (add it if absent).

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestHeartbeat' -v`
Expected: PASS (counter present; `plain` matches).

- [ ] **Step 5: Commit**

```bash
git add internal/automations/model.go internal/automations/standard_test.go
git commit -m "feat(T2): heartbeat open/overdue task counter from the index (FR-161)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Full green + docs + live smoke

**Files:**
- Modify: `docs/03-requirements.md` (FR-160/161 rows)
- Modify: `docs/06-component-automation-engine.md` (actions-consolidate automation entry)
- Modify: `docs/04-data-model-and-config.md` (`automations:` map line + a narrative bullet)
- Modify: `docs/16-roadmap-1.2.5.md` (T2 marked BUILT)
- Modify: `CLAUDE.md` (FR range → FR-161)
- Modify: `README.md` / `docs/GUIDE.md` (automation count 20 → 21, if they state a count)

**Interfaces:** none (docs + verification).

- [ ] **Step 1: Whole-suite green + lint**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./... && golangci-lint run`
Expected: all pass. Fix any `gofmt`/`goimports`/`vet`/staticcheck drift (run `gofmt -w` on touched files; watch for De-Morgan/emoji-alignment nits as in T1).

- [ ] **Step 2: Write the docs**

- `docs/03-requirements.md`: add two rows —
  - **FR-160** Actions consolidation: a zero-model `actions-consolidate` automation (daily, enabled by default) rendering the index into the `axon:actions` block of `01-Projects/Actions.md` in GTD sections (Overdue/Today/This week/Next-by-project/Waiting/Someday/Done-this-week) as plain `[[source]]` references (no duplicate checkboxes); change-gated on the rendered projection; wikilink-safe `Patch`; DryRun-safe.
  - **FR-161** Heartbeat task counter: the essential heartbeat gains a deterministic `tasks: N open (M overdue)` count sourced from the index (`db.ListActions` + `actions.Bucket`) — the first non-CLI consumer of "one grammar, one truth".
- `docs/06-component-automation-engine.md`: add an `actions-consolidate` entry in the standard-automations list (zero-model, daily, enabled by default; renders the GTD projection; references not checkboxes; change-gated).
- `docs/04-data-model-and-config.md`: add the `actions-consolidate: { enabled: true, schedule: "0 7 * * *", model: none, budget_tokens: 0 }` line to the illustrative `automations:` map and a one-line narrative near `merge-proposals`.
- `docs/16-roadmap-1.2.5.md`: mark the **T2** line ✅ **BUILT 2026-07-10** (FR-160/161, no ADR).
- `CLAUDE.md`: bump the `docs/03` FR range to FR-01…**161**; update the 1.2.5 doc-pack line's "remaining" list (drop T2).
- `README.md` / `docs/GUIDE.md`: if they state an automation count ("Twenty automations"), bump to **Twenty-one** and add `actions-consolidate` to any automation list.

- [ ] **Step 3: Live smoke (real binary, isolated AXON_HOME — never touch :7777)**

```bash
go build -o /tmp/axon-t2 ./cmd/axon
SMOKE=$(mktemp -d)
# Reuse the T1 smoke config shape (port 7788); set vault_path/data_dir under $SMOKE.
/tmp/axon-t2 init --config "$SMOKE/config.yaml"
# seed a spread of tasks across notes (overdue/today/this-week/next/waiting/someday/done),
mkdir -p "$SMOKE/vault/01-Projects"
printf '## Sprint\n- [ ] fix bug 📅 2000-01-01 ⏫\n- [ ] plan 📅 2999-01-01\n- [ ] wait legal #waiting\n- [ ] someday learn rust #someday\n- [x] shipped ✅ '"$(date +%F)"'\n' > "$SMOKE/vault/01-Projects/work.md"
/tmp/axon-t2 reindex --config "$SMOKE/config.yaml"
/tmp/axon-t2 run actions-consolidate --config "$SMOKE/config.yaml"
sed -n '1,60p' "$SMOKE/vault/01-Projects/Actions.md"
/tmp/axon-t2 run actions-consolidate --config "$SMOKE/config.yaml"   # second run → change-gate skip
/tmp/axon-t2 run actions-consolidate --dry-run --config "$SMOKE/config.yaml"
```

Expected: `Actions.md` created with the human preamble + GTD sections, references (`- text — [[work]]`) not checkboxes, correct bucketing, footer; the second run is a change-gate **skip** (no rewrite); `--dry-run` writes nothing. No Claude/Ollama needed (zero-model). Skip scratch cleanup (GateGuard).

- [ ] **Step 4: Commit docs**

```bash
git add docs/ CLAUDE.md README.md
git commit -m "docs(T2): FR-160/161, automation entry, roadmap T2 built

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Read one neighbor first:** `internal/automations/pulse.go` (the clone source) and `pulse_test.go` (the `newRC`/`mustReindex`/`seedActions`-style scaffolding). The plan's assertions are the contract; match the existing harness.
- **Zero-model:** never call `runModel`; `EstimatedTokens: 0`. If you're reaching for the token manager, you've drifted.
- **References, not checkboxes:** the block must contain no `- [ ]`/`- [x]`. A checkbox in the projection would confuse users (and violates constitution §3) even though the T1 parser already skips `axon:actions` blocks.
- **Heartbeat is essential:** `openTaskCounts` must swallow DB errors (return 0,0), never fail the run.
- **GateGuard:** first Write/Edit/Bash each turn triggers a fact-force preamble (comply tersely); `git commit --amend`/`rm -rf` are blocked (follow-up commits; leave scratch dirs).
```
