# T1 — Action Index Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the derived action index — a tolerant Obsidian-Tasks-grammar parser (`internal/actions`), a disposable `actions` SQLite table rebuilt in the reindex transaction, and a read-only `axon actions` CLI.

**Architecture:** A pure leaf package `internal/actions` parses checkbox lines (`Parse`/`Extract`) and computes a stable identity (`Hash`) and a read-time GTD bucket (`Bucket`). `internal/db/actions.go` persists a derived table (delete-all+insert, the `memory_facts` pattern). `internal/core/reindex.go` rebuilds it inside the existing per-note loop + reindex transaction. `axon actions` reads and renders it. Zero model calls, read-only.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite` (FTS5 + float32 vectors), cobra CLI.

**Spec:** `docs/superpowers/specs/2026-07-10-t1-action-index-design.md`

## Global Constraints

- **Cardinal rule 1:** No Claude call bypasses the token manager. *T1 makes zero model calls — nothing reaches Claude or Ollama; no ledger entry.*
- **Cardinal rule 2:** No vault mutation outside wikilink-safe ops. *T1 is read-only — no `vault.write`/`patch`/`move`, no `fs` writes. The one write (completion) is T3/ADR-034, not this slice.*
- **S8:** A fresh clone with all automations off still runs and is useful. *T1 adds a derived index + a read command; nothing is scheduled, nothing spends.*
- **S9:** The vault rebuilds the DB, never the reverse. *The `actions` table is derived; `reindex` rebuilds it from Markdown byte-equivalently. Tested (reindex-twice).*
- **NFR-05:** Note content is data, never instructions. *T1 parses task text as data; it makes no model call.*
- Go: `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green. Wrap errors with `%w`. Propagate `context.Context`.
- Run test suites with `env -u FORCE_COLOR` (the ambient shell exports `FORCE_COLOR=3`).
- Identity hash is **state-independent** (excludes the checkbox marker) but **content-sensitive** (dates/text changes = new identity). Bucket is **read-time**, never stored. Emoji date grammar only (no Dataview `[due:: ]`).
- FR IDs: FR-157 (parser), FR-158 (table + reindex), FR-159 (CLI). New ADR: **ADR-033**. Migration: **`0007_actions.sql`**.
- The ambient GateGuard hook fires a fact-force preamble on the first Write/Edit/Bash each turn and blocks `git commit --amend` / `rm -rf`; comply tersely, use follow-up commits, skip scratch cleanup.

---

### Task 1: Parser core — `Parse`, `Action`, `State`, `Hash` (FR-157)

**Files:**
- Create: `internal/actions/action.go`
- Test: `internal/actions/action_test.go`

**Interfaces:**
- Consumes: stdlib only (`crypto/sha256`, `encoding/hex`, `regexp`, `strings`).
- Produces (for Tasks 2–6): the `Action` struct + `State` type below; `func Parse(line string) (Action, bool)`; `func (a Action) Hash() string`.

- [ ] **Step 1: Write the failing test**

Create `internal/actions/action_test.go`:

```go
package actions

import "testing"

func TestParseStates(t *testing.T) {
	cases := []struct {
		line  string
		ok    bool
		state State
		cbox  string
	}{
		{"- [ ] open task", true, StateOpen, " "},
		{"- [x] done task", true, StateDone, "x"},
		{"- [X] done upper", true, StateDone, "X"},
		{"- [-] cancelled", true, StateCancelled, "-"},
		{"- [/] in progress", true, StateOpen, "/"}, // unknown → open (tolerant)
		{"* [ ] star bullet", true, StateOpen, " "},
		{"+ [ ] plus bullet", true, StateOpen, " "},
		{"  - [ ] indented", true, StateOpen, " "},
		{"not a task", false, "", ""},
		{"- plain bullet", false, "", ""},
		{"# heading", false, "", ""},
	}
	for _, c := range cases {
		a, ok := Parse(c.line)
		if ok != c.ok {
			t.Fatalf("Parse(%q) ok=%v want %v", c.line, ok, c.ok)
		}
		if !ok {
			continue
		}
		if a.State != c.state || a.Checkbox != c.cbox {
			t.Errorf("Parse(%q) state=%q cbox=%q want %q/%q", c.line, a.State, a.Checkbox, c.state, c.cbox)
		}
	}
}

func TestParseFields(t *testing.T) {
	line := "- [ ] Email @office [[Acme]] about #contract 📅 2026-07-15 ⏳ 2026-07-12 🛫 2026-07-10 ⏫"
	a, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if a.Due != "2026-07-15" || a.Scheduled != "2026-07-12" || a.Start != "2026-07-10" {
		t.Errorf("dates: due=%q sched=%q start=%q", a.Due, a.Scheduled, a.Start)
	}
	if a.Priority != "high" {
		t.Errorf("priority=%q want high", a.Priority)
	}
	if len(a.Contexts) != 1 || a.Contexts[0] != "office" {
		t.Errorf("contexts=%v", a.Contexts)
	}
	if len(a.Tags) != 1 || a.Tags[0] != "contract" {
		t.Errorf("tags=%v", a.Tags)
	}
	if a.Project != "Acme" {
		t.Errorf("project=%q want Acme", a.Project)
	}
	if wantIn := "Email"; !contains(a.Text, wantIn) {
		t.Errorf("text=%q should contain %q", a.Text, wantIn)
	}
	if contains(a.Text, "📅") || contains(a.Text, "⏫") {
		t.Errorf("text=%q should have date/priority emoji stripped", a.Text)
	}
}

func TestParseDoneDateAndAlias(t *testing.T) {
	a, _ := Parse("- [x] ship it [[proj|Project X]] ✅ 2026-07-09")
	if a.DoneDate != "2026-07-09" {
		t.Errorf("done date=%q", a.DoneDate)
	}
	if a.Project != "proj" { // alias stripped
		t.Errorf("project=%q want proj", a.Project)
	}
}

func TestHashStateIndependentButContentSensitive(t *testing.T) {
	open, _ := Parse("- [ ] call Bob 📅 2026-07-15")
	open.SourcePath = "Daily/2026-07-10.md"
	done, _ := Parse("- [x] call Bob 📅 2026-07-15")
	done.SourcePath = "Daily/2026-07-10.md"
	if open.Hash() != done.Hash() {
		t.Error("hash must be state-independent ([ ] vs [x] equal)")
	}
	resched, _ := Parse("- [ ] call Bob 📅 2026-07-20")
	resched.SourcePath = "Daily/2026-07-10.md"
	if open.Hash() == resched.Hash() {
		t.Error("hash must change when the line content (due date) changes")
	}
	other := open
	other.SourcePath = "Daily/2026-07-11.md"
	if open.Hash() == other.Hash() {
		t.Error("hash must incorporate source path")
	}
}

func TestParsePathological(t *testing.T) {
	// Must never panic: emoji-dense, huge, no fields.
	Parse("- [ ] " + repeat("📅⏫🔺x@a #b [[c]] ", 500))
	Parse("- [ ]")            // no space after bracket → not a task (no body)
	Parse("- [ ] ")          // empty body
}

// tiny local helpers so the test file is self-contained.
func contains(s, sub string) bool { return len(sub) == 0 || indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -run TestParse -v`
Expected: FAIL — `undefined: Parse` / `undefined: StateOpen`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/actions/action.go`:

```go
// Package actions parses Obsidian-Tasks-grammar checkbox lines into structured
// Actions and computes their GTD status. Pure leaf: stdlib only. It is the one
// task parser in AXON — reindex, the CLI, and later slices all read it.
package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// State is the checkbox-derived, date-independent lifecycle of an action.
type State string

const (
	StateOpen      State = "open"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
)

// Action is one parsed checkbox line. Line-local fields come from Parse;
// SourcePath/LineNo/Section/Archived are stamped by Extract.
type Action struct {
	SourcePath string   `json:"source_path"`
	LineNo     int      `json:"line_no"`
	Section    string   `json:"section,omitempty"`
	Text       string   `json:"text"`
	Raw        string   `json:"raw"`
	State      State    `json:"state"`
	Checkbox   string   `json:"checkbox"`
	Priority   string   `json:"priority,omitempty"`
	Due        string   `json:"due,omitempty"`
	Scheduled  string   `json:"scheduled,omitempty"`
	Start      string   `json:"start,omitempty"`
	DoneDate   string   `json:"done_date,omitempty"`
	Project    string   `json:"project,omitempty"`
	Contexts   []string `json:"contexts,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Archived   bool     `json:"archived,omitempty"`
}

var (
	checkboxRe = regexp.MustCompile(`^\s*[-*+] \[(.)\] (.*)$`)
	dueRe      = regexp.MustCompile(`\x{1F4C5}\s*(\d{4}-\d{2}-\d{2})`) // 📅
	schedRe    = regexp.MustCompile(`\x{23F3}\s*(\d{4}-\d{2}-\d{2})`)  // ⏳
	startRe    = regexp.MustCompile(`\x{1F6EB}\s*(\d{4}-\d{2}-\d{2})`) // 🛫
	doneRe     = regexp.MustCompile(`\x{2705}\s*(\d{4}-\d{2}-\d{2})`)  // ✅
	cancelRe   = regexp.MustCompile(`\x{274C}\s*(\d{4}-\d{2}-\d{2})`)  // ❌ (tolerated, value unused)
	contextRe  = regexp.MustCompile(`(?:^|\s)@(\w[\w/-]*)`)
	tagRe      = regexp.MustCompile(`(?:^|\s)#([\w/][\w/-]*)`)
	wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	wsRe       = regexp.MustCompile(`\s+`)
)

var priorityEmoji = []struct{ glyph, word string }{
	{"\U0001F53A", "highest"}, // 🔺
	{"⏫", "high"},        // ⏫
	{"\U0001F53C", "medium"},  // 🔼
	{"⏬", "lowest"},      // ⏬ (check before 🔽 is irrelevant; distinct glyphs)
	{"\U0001F53D", "low"},     // 🔽
}

// Parse turns one line into an Action. ok=false for a non-checkbox line.
func Parse(line string) (Action, bool) {
	m := checkboxRe.FindStringSubmatch(line)
	if m == nil {
		return Action{}, false
	}
	a := Action{Raw: line, Checkbox: m[1]}
	switch m[1] {
	case "x", "X":
		a.State = StateDone
	case "-":
		a.State = StateCancelled
	default:
		a.State = StateOpen // " " and any unknown marker (tolerant)
	}
	body := m[2]
	body = extractDate(body, dueRe, &a.Due)
	body = extractDate(body, schedRe, &a.Scheduled)
	body = extractDate(body, startRe, &a.Start)
	body = extractDate(body, doneRe, &a.DoneDate)
	body = cancelRe.ReplaceAllString(body, "")
	for _, p := range priorityEmoji {
		if strings.Contains(body, p.glyph) {
			a.Priority = p.word
			body = strings.ReplaceAll(body, p.glyph, "")
			break
		}
	}
	for _, cm := range contextRe.FindAllStringSubmatch(body, -1) {
		a.Contexts = append(a.Contexts, cm[1])
	}
	for _, tm := range tagRe.FindAllStringSubmatch(body, -1) {
		a.Tags = append(a.Tags, tm[1])
	}
	if wm := wikilinkRe.FindStringSubmatch(body); wm != nil {
		a.Project = linkTarget(wm[1])
	}
	a.Text = strings.TrimSpace(wsRe.ReplaceAllString(body, " "))
	return a, true
}

// Hash is the stable identity: sha256(source_path + "\n" + normalized body),
// where the body EXCLUDES the checkbox marker (so [ ]→[x] keeps identity) but
// includes dates/text (so a reschedule is a new identity — the T3 stale-hash
// contract). SourcePath must be set (Extract does so).
func (a Action) Hash() string {
	body := a.Raw
	if m := checkboxRe.FindStringSubmatch(body); m != nil {
		body = m[2]
	}
	norm := strings.TrimSpace(wsRe.ReplaceAllString(body, " "))
	sum := sha256.Sum256([]byte(a.SourcePath + "\n" + norm))
	return hex.EncodeToString(sum[:])
}

func extractDate(body string, re *regexp.Regexp, dst *string) string {
	if m := re.FindStringSubmatch(body); m != nil {
		*dst = m[1]
		body = re.ReplaceAllString(body, "")
	}
	return body
}

func linkTarget(s string) string {
	if i := strings.IndexByte(s, '|'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/actions/action.go internal/actions/action_test.go
git commit -m "feat(T1): tolerant Obsidian-Tasks checkbox parser + stable hash (FR-157)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: `Bucket` — read-time GTD status (FR-157)

**Files:**
- Modify: `internal/actions/action.go` (add `Bucket` + `hasTag`)
- Test: `internal/actions/bucket_test.go` (create)

**Interfaces:**
- Consumes: the `Action` struct + `State` consts from Task 1; `time`.
- Produces (for Tasks 3–6): `func Bucket(a Action, today time.Time) string` → one of `overdue|today|scheduled|next|waiting|someday|done|cancelled`.

- [ ] **Step 1: Write the failing test**

Create `internal/actions/bucket_test.go`:

```go
package actions

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestBucketPrecedence(t *testing.T) {
	today := day("2026-07-10")
	cases := []struct {
		name string
		a    Action
		want string
	}{
		{"done wins", Action{State: StateDone, Due: "2026-07-01"}, "done"},
		{"cancelled", Action{State: StateCancelled}, "cancelled"},
		{"someday over overdue", Action{State: StateOpen, Tags: []string{"someday"}, Due: "2026-07-01"}, "someday"},
		{"waiting over overdue", Action{State: StateOpen, Tags: []string{"waiting"}, Due: "2026-07-01"}, "waiting"},
		{"overdue", Action{State: StateOpen, Due: "2026-07-09"}, "overdue"},
		{"today", Action{State: StateOpen, Due: "2026-07-10"}, "today"},
		{"scheduled future start", Action{State: StateOpen, Start: "2026-07-20"}, "scheduled"},
		{"scheduled future sched", Action{State: StateOpen, Scheduled: "2026-07-20"}, "scheduled"},
		{"next (no dates)", Action{State: StateOpen}, "next"},
		{"next (future due only)", Action{State: StateOpen, Due: "2026-07-20"}, "next"},
	}
	for _, c := range cases {
		if got := Bucket(c.a, today); got != c.want {
			t.Errorf("%s: Bucket=%q want %q", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -run TestBucket -v`
Expected: FAIL — `undefined: Bucket`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/actions/action.go` (and add `"time"` to the import block):

```go
// Bucket resolves the single GTD bucket by precedence:
// done > cancelled > someday > waiting > overdue > today > scheduled > next.
// Date fields are compared lexically against today (YYYY-MM-DD). Read-time only —
// never persisted, so it can't go stale at midnight.
func Bucket(a Action, today time.Time) string {
	switch a.State {
	case StateDone:
		return "done"
	case StateCancelled:
		return "cancelled"
	}
	t := today.Format("2006-01-02")
	switch {
	case hasTag(a.Tags, "someday"):
		return "someday"
	case hasTag(a.Tags, "waiting"):
		return "waiting"
	case a.Due != "" && a.Due < t:
		return "overdue"
	case a.Due == t:
		return "today"
	case a.Start > t || a.Scheduled > t:
		return "scheduled"
	default:
		return "next"
	}
}

func hasTag(tags []string, want string) bool {
	for _, tg := range tags {
		if strings.EqualFold(tg, want) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -v`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
git add internal/actions/action.go internal/actions/bucket_test.go
git commit -m "feat(T1): read-time GTD bucket precedence (FR-157)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: `Extract` — whole-note walk (FR-157)

**Files:**
- Create: `internal/actions/extract.go`
- Test: `internal/actions/extract_test.go`

**Interfaces:**
- Consumes: `Parse` + `Action` from Task 1.
- Produces (for Task 5): `func Extract(sourcePath, body string, archived bool) []Action` — parses every checkbox line, tracks the nearest heading as `Section`, skips fenced code and `axon:actions` blocks, stamps `SourcePath`/`LineNo`/`Section`/`Archived`.

- [ ] **Step 1: Write the failing test**

Create `internal/actions/extract_test.go`:

```go
package actions

import "testing"

func TestExtract(t *testing.T) {
	body := "intro line\n" +
		"## Work\n" +
		"- [ ] first task 📅 2026-07-15\n" +
		"- [x] done task\n" +
		"```\n" +
		"- [ ] fenced not a task\n" +
		"```\n" +
		"## Home\n" +
		"- [ ] second task\n" +
		"<!-- axon:actions:start -->\n" +
		"- [ ] projection reference (must be skipped)\n" +
		"<!-- axon:actions:end -->\n" +
		"- [ ] third task\n"
	got := Extract("Daily/2026-07-10.md", body, false)
	if len(got) != 4 {
		t.Fatalf("got %d actions, want 4: %+v", len(got), got)
	}
	if got[0].Section != "Work" || got[0].SourcePath != "Daily/2026-07-10.md" {
		t.Errorf("action0 section/path = %q/%q", got[0].Section, got[0].SourcePath)
	}
	if got[3].Section != "Home" {
		t.Errorf("action3 section = %q want Home (third task after the skipped block)", got[3].Section)
	}
	for _, a := range got {
		if contains(a.Text, "fenced") || contains(a.Text, "projection") {
			t.Errorf("leaked a skipped line: %q", a.Text)
		}
	}
	// LineNo must be the real body index (used for ordering/display).
	if got[0].LineNo != 2 {
		t.Errorf("action0 LineNo = %d want 2", got[0].LineNo)
	}
}

func TestExtractArchivedFlag(t *testing.T) {
	got := Extract("04-Archive/old.md", "- [ ] archived task\n", true)
	if len(got) != 1 || !got[0].Archived {
		t.Fatalf("expected 1 archived action, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -run TestExtract -v`
Expected: FAIL — `undefined: Extract`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/actions/extract.go`:

```go
package actions

import (
	"regexp"
	"strings"
)

var headingRe = regexp.MustCompile(`^#{1,6}\s+(.*\S)\s*$`)

// Extract parses every checkbox line in a note body. It tracks the nearest
// heading as Section, skips fenced code blocks and the axon:actions projection
// block (constitution §3), and stamps note-level fields on each Action.
func Extract(sourcePath, body string, archived bool) []Action {
	var out []Action
	section := ""
	inFence, fenceTok := false, ""
	inActions := false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, "<!-- axon:actions:start -->"):
			inActions = true
			continue
		case strings.Contains(trimmed, "<!-- axon:actions:end -->"):
			inActions = false
			continue
		}
		if inActions {
			continue
		}
		if tok := fenceToken(trimmed); tok != "" {
			switch {
			case !inFence:
				inFence, fenceTok = true, tok
			case tok == fenceTok:
				inFence, fenceTok = false, ""
			}
			continue
		}
		if inFence {
			continue
		}
		if hm := headingRe.FindStringSubmatch(line); hm != nil {
			section = hm[1]
			continue
		}
		if a, ok := Parse(line); ok {
			a.SourcePath = sourcePath
			a.LineNo = i
			a.Section = section
			a.Archived = archived
			out = append(out, a)
		}
	}
	return out
}

func fenceToken(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/actions/extract.go internal/actions/extract_test.go
git commit -m "feat(T1): whole-note action extraction (headings, fences, projection skip) (FR-157)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Derived table — `0007_actions.sql` + `internal/db/actions.go` (FR-158)

**Files:**
- Create: `internal/db/migrations/0007_actions.sql`
- Create: `internal/db/actions.go`
- Test: `internal/db/actions_test.go`

**Interfaces:**
- Consumes (existing in `db`): `Execer`, `Queryer`, `Queryer2` interfaces; `nullify(string) any`; `boolInt(bool) int`; the test helper `newMigratedDB(t)` (from `internal/db`'s existing test files).
- Produces (for Tasks 5–6): the `db.Action` row struct; `ReplaceActions(ctx, Execer, []Action) error`; `ListActions(ctx, Queryer2, ListActionsOpts) ([]Action, error)`; `ActionStateCounts(ctx, Queryer) (total, open, done, cancelled, archived int, err error)`; `ListActionsOpts{SourcePath, State string; IncludeAll bool}`.

- [ ] **Step 1: Write the failing test**

Create `internal/db/actions_test.go`:

```go
package db

import (
	"context"
	"testing"
)

func TestReplaceAndListActions(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)
	rows := []Action{
		{Hash: "h1", SourcePath: "01-Projects/a.md", LineNo: 3, Section: "Todo",
			Text: "call bob", Raw: "- [ ] call bob 📅 2026-07-15", State: "open",
			Checkbox: " ", Due: "2026-07-15", Contexts: []string{"phone"},
			Tags: []string{"waiting"}, Updated: "2026-07-10T00:00:00Z"},
		{Hash: "h2", SourcePath: "01-Projects/a.md", LineNo: 5, Text: "ship",
			Raw: "- [x] ship", State: "done", Checkbox: "x", DoneDate: "2026-07-09",
			Updated: "2026-07-10T00:00:00Z"},
		{Hash: "h3", SourcePath: "04-Archive/old.md", LineNo: 1, Text: "old",
			Raw: "- [ ] old", State: "open", Checkbox: " ", Archived: true,
			Updated: "2026-07-10T00:00:00Z"},
	}
	if err := ReplaceActions(ctx, d.DB(), rows); err != nil {
		t.Fatal(err)
	}
	// default (archived excluded)
	got, err := ListActions(ctx, d.DB(), ListActionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d actions (archived should be excluded), want 2", len(got))
	}
	if got[0].Tags == nil || got[0].Tags[0] != "waiting" || got[0].Contexts[0] != "phone" {
		t.Errorf("json arrays lost: tags=%v contexts=%v", got[0].Tags, got[0].Contexts)
	}
	// IncludeAll
	all, _ := ListActions(ctx, d.DB(), ListActionsOpts{IncludeAll: true})
	if len(all) != 3 {
		t.Fatalf("IncludeAll got %d, want 3", len(all))
	}
	// state filter
	open, _ := ListActions(ctx, d.DB(), ListActionsOpts{State: "open"})
	if len(open) != 1 {
		t.Fatalf("state=open got %d, want 1 (non-archived)", len(open))
	}
	// counts
	total, o, done, cancelled, archived, err := ActionStateCounts(ctx, d.DB())
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || o != 2 || done != 1 || cancelled != 0 || archived != 1 {
		t.Errorf("counts total=%d open=%d done=%d cancelled=%d archived=%d", total, o, done, cancelled, archived)
	}
	// replace is delete-all
	if err := ReplaceActions(ctx, d.DB(), rows[:1]); err != nil {
		t.Fatal(err)
	}
	again, _ := ListActions(ctx, d.DB(), ListActionsOpts{IncludeAll: true})
	if len(again) != 1 {
		t.Fatalf("after replace got %d, want 1", len(again))
	}
}
```

> **Note:** if the `internal/db` test helper is named differently than `newMigratedDB` or `d.DB()` differs, adapt to the existing helper (grep `func newMigratedDB` / how other `_test.go` in `internal/db` obtain the `*sql.DB`). Per the R1 memory the helper is `newMigratedDB`.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestReplaceAndListActions -v`
Expected: FAIL — `undefined: Action` / `no such table: actions`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/db/migrations/0007_actions.sql`:

```sql
-- 0007_actions — 1.2.5 T1 (ADR-033). A DERIVED, disposable projection of the
-- checkbox lines across the vault: reindex delete-all+inserts these rows from
-- Markdown (the vault is the source of truth, ADR-011). Never authoritative.
CREATE TABLE actions (
  id           INTEGER PRIMARY KEY,
  hash         TEXT NOT NULL,
  source_path  TEXT NOT NULL,
  line_no      INTEGER NOT NULL,
  section      TEXT,
  text         TEXT NOT NULL,
  raw          TEXT NOT NULL,
  state        TEXT NOT NULL,
  checkbox     TEXT NOT NULL,
  priority     TEXT,
  due          TEXT,
  scheduled    TEXT,
  start        TEXT,
  done_date    TEXT,
  project      TEXT,
  contexts     TEXT,
  tags         TEXT,
  archived     INTEGER NOT NULL DEFAULT 0,
  updated      TEXT NOT NULL
);
CREATE INDEX idx_actions_open   ON actions(state) WHERE state = 'open';
CREATE INDEX idx_actions_source ON actions(source_path);
```

Create `internal/db/actions.go`:

```go
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Action is one row of the derived actions index (ADR-033). A re-derivable
// projection of a checkbox line; reindex delete-all+inserts these from Markdown,
// so they are disposable (S9).
type Action struct {
	ID         int64
	Hash       string
	SourcePath string
	LineNo     int
	Section    string
	Text       string
	Raw        string
	State      string
	Checkbox   string
	Priority   string
	Due        string
	Scheduled  string
	Start      string
	DoneDate   string
	Project    string
	Contexts   []string
	Tags       []string
	Archived   bool
	Updated    string
}

// ListActionsOpts filters ListActions. Zero value = all non-archived actions.
type ListActionsOpts struct {
	SourcePath string // "" = any
	State      string // "" = any
	IncludeAll bool   // false = exclude archived rows
}

// ReplaceActions rebuilds the whole actions table: delete every row then insert
// in caller order (reindex passes them by source_path then line_no) so the
// projection stays in step with the vault and reindex is row-for-row
// deterministic (S9).
func ReplaceActions(ctx context.Context, q Execer, as []Action) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM actions;"); err != nil {
		return fmt.Errorf("clear actions: %w", err)
	}
	for _, a := range as {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO actions
			   (hash, source_path, line_no, section, text, raw, state, checkbox,
			    priority, due, scheduled, start, done_date, project, contexts, tags,
			    archived, updated)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);`,
			a.Hash, a.SourcePath, a.LineNo, nullify(a.Section), a.Text, a.Raw,
			a.State, a.Checkbox, nullify(a.Priority), nullify(a.Due),
			nullify(a.Scheduled), nullify(a.Start), nullify(a.DoneDate),
			nullify(a.Project), marshalStrings(a.Contexts), marshalStrings(a.Tags),
			boolInt(a.Archived), a.Updated); err != nil {
			return fmt.Errorf("insert action %q: %w", a.Text, err)
		}
	}
	return nil
}

// ListActions returns actions filtered by opts, ordered by source_path, line_no.
func ListActions(ctx context.Context, q Queryer2, opts ListActionsOpts) ([]Action, error) {
	query := `SELECT id, hash, source_path, line_no, COALESCE(section,''), text, raw,
	                 state, checkbox, COALESCE(priority,''), COALESCE(due,''),
	                 COALESCE(scheduled,''), COALESCE(start,''), COALESCE(done_date,''),
	                 COALESCE(project,''), COALESCE(contexts,'[]'), COALESCE(tags,'[]'),
	                 archived, updated
	            FROM actions`
	var conds []string
	var args []any
	if opts.SourcePath != "" {
		conds = append(conds, "source_path = ?")
		args = append(args, opts.SourcePath)
	}
	if opts.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, opts.State)
	}
	if !opts.IncludeAll {
		conds = append(conds, "archived = 0")
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY source_path, line_no;"
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

// ActionStateCounts reports date-independent counts for doctor.
func ActionStateCounts(ctx context.Context, q Queryer) (total, open, done, cancelled, archived int, err error) {
	var t, o, d, c, a sql.NullInt64
	err = q.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        SUM(CASE WHEN state='open' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN state='done' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN state='cancelled' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END)
		   FROM actions;`).Scan(&t, &o, &d, &c, &a)
	if err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("action counts: %w", err)
	}
	return int(t.Int64), int(o.Int64), int(d.Int64), int(c.Int64), int(a.Int64), nil
}

func scanActions(rows *sql.Rows) ([]Action, error) {
	var out []Action
	for rows.Next() {
		var a Action
		var archived int
		var ctxJSON, tagJSON string
		if err := rows.Scan(&a.ID, &a.Hash, &a.SourcePath, &a.LineNo, &a.Section,
			&a.Text, &a.Raw, &a.State, &a.Checkbox, &a.Priority, &a.Due,
			&a.Scheduled, &a.Start, &a.DoneDate, &a.Project, &ctxJSON, &tagJSON,
			&archived, &a.Updated); err != nil {
			return nil, err
		}
		a.Archived = archived != 0
		a.Contexts = unmarshalStrings(ctxJSON)
		a.Tags = unmarshalStrings(tagJSON)
		out = append(out, a)
	}
	return out, rows.Err()
}

// marshalStrings/unmarshalStrings store a []string as a JSON array column
// (mirrors how notes.go stores tags inline).
func marshalStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalStrings(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
```

> If `internal/db` already defines `marshalStrings`/`unmarshalStrings`, delete these two and reuse the existing ones (a build error `redeclared` tells you). As of this plan, `notes.go` marshals tags inline with no shared helper, so these are new.

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestReplaceAndListActions -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/0007_actions.sql internal/db/actions.go internal/db/actions_test.go
git commit -m "feat(T1): derived actions table + repository (FR-158)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Reindex — `rebuildActions` in-tx (FR-158)

**Files:**
- Modify: `internal/core/reindex.go` (add `rebuildActions`, call it before `tx.Commit`)
- Test: `internal/core/reindex_actions_test.go` (create)

**Interfaces:**
- Consumes: `actions.Extract` (Task 3); `db.Action`, `db.ReplaceActions`, `db.ListActions` (Task 4); the existing `notes []*parsed` slice (`parsed.row db.NoteRow`, `parsed.body string`) and `nowStamp()` in `reindex.go`; `db.DBTX`.
- Produces: rows in the `actions` table after `Reindex`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/reindex_actions_test.go` (mirror an existing `internal/core` reindex test's vault+DB setup helpers; grep the package for how other reindex tests build a temp vault + open the DB — e.g. `TestReindex...`):

```go
package core

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func TestReindexBuildsActions(t *testing.T) {
	ctx := context.Background()
	v, sqlDB, cleanup := newReindexFixture(t) // existing helper in this package's tests
	defer cleanup()

	writeNote(t, v, "01-Projects/proj.md", "## Todo\n- [ ] alpha 📅 2026-07-15\n- [x] beta ✅ 2026-07-09\n")
	writeNote(t, v, "04-Archive/old.md", "- [ ] gamma\n")

	if _, err := Reindex(ctx, ReindexDeps{Vault: v, DB: sqlDB}); err != nil { // adapt to the real Reindex signature
		t.Fatal(err)
	}
	all, err := db.ListActions(ctx, sqlDB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d actions, want 3", len(all))
	}
	var archivedSeen bool
	for _, a := range all {
		if a.SourcePath == "04-Archive/old.md" && a.Archived {
			archivedSeen = true
		}
	}
	if !archivedSeen {
		t.Error("archived note's action not flagged archived")
	}

	// S9: reindex again → byte-identical row set (hash/state/dates stable).
	before := actionSig(all)
	if _, err := Reindex(ctx, ReindexDeps{Vault: v, DB: sqlDB}); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListActions(ctx, sqlDB, db.ListActionsOpts{IncludeAll: true})
	if actionSig(after) != before {
		t.Error("reindex is not byte-equivalent (S9)")
	}
}

func actionSig(as []db.Action) string {
	s := ""
	for _, a := range as {
		s += a.SourcePath + "|" + a.Hash + "|" + a.State + "|" + a.Due + "\n"
	}
	return s
}
```

> Adapt `newReindexFixture`/`writeNote`/`ReindexDeps`/`Reindex(...)` to the **actual** helpers and signature in `internal/core` (read one existing reindex test first — the R1 memory notes reindex tests use real SQLite + a real vault). The behavioral assertions (3 actions, archived flag, S9 byte-equivalence) are the contract; the scaffolding matches whatever the package already uses.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestReindexBuildsActions -v`
Expected: FAIL — `actions` table populated 0 rows (or compile error until wired).

- [ ] **Step 3: Write minimal implementation**

In `internal/core/reindex.go`, add the import `"github.com/jandro-es/axon/internal/actions"` and (if not present) `"strings"`. Add the call immediately **before** the `rebuildMemoryFacts(...)` call (which sits just before `tx.Commit()`):

```go
	if err := rebuildActions(ctx, tx, notes, nowStamp()); err != nil {
		return res, err
	}
```

Add the function (near `rebuildMemoryFacts`):

```go
// rebuildActions replaces the derived actions index from the checkbox lines in
// every note (ADR-033). Runs inside the reindex tx: a failure rolls back with
// the rest, leaving the prior index intact (S9). Read-only over Markdown — no
// vault write, no model call.
func rebuildActions(ctx context.Context, tx db.DBTX, notes []*parsed, now string) error {
	var rows []db.Action
	for _, n := range notes {
		archived := strings.HasPrefix(n.row.Path, "04-Archive/")
		for _, a := range actions.Extract(n.row.Path, n.body, archived) {
			rows = append(rows, db.Action{
				Hash: a.Hash(), SourcePath: a.SourcePath, LineNo: a.LineNo,
				Section: a.Section, Text: a.Text, Raw: a.Raw,
				State: string(a.State), Checkbox: a.Checkbox, Priority: a.Priority,
				Due: a.Due, Scheduled: a.Scheduled, Start: a.Start, DoneDate: a.DoneDate,
				Project: a.Project, Contexts: a.Contexts, Tags: a.Tags,
				Archived: a.Archived, Updated: now,
			})
		}
	}
	return db.ReplaceActions(ctx, tx, rows)
}
```

> `notes` is ordered by `v.List` (sorted paths) and `Extract` returns line order, so `rows` — and thus the inserted table — is deterministic (S9).

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestReindexBuildsActions -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/reindex.go internal/core/reindex_actions_test.go
git commit -m "feat(T1): rebuild actions index in the reindex transaction (FR-158)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: CLI — `axon actions` (FR-159)

**Files:**
- Create: `cmd/axon/actions_cmd.go`
- Modify: `cmd/axon/root.go` (register `newActionsCmd(gf)`)
- Test: `cmd/axon/actions_cmd_test.go`

**Interfaces:**
- Consumes: `loadProfileDeps(gf, true) (*profileDeps, error)` with `deps.db (*sql.DB)` and `deps.close()`; `db.ListActions`/`db.ListActionsOpts`/`db.Action` (Task 4); `actions.Bucket`/`actions.Action`/`actions.State` (Tasks 1–2); `tui.Interactive`/`tui.Table`, `ui.For` (see `related_cmd.go`); the CLI test harness `run(t, args...)` + `writeTempConfig(t, dir)` (from `cmd/axon/cli_test.go`).
- Produces: the `actions` subcommand.

- [ ] **Step 1: Write the failing test**

Create `cmd/axon/actions_cmd_test.go` (mirror `related_cmd_test.go`'s harness usage):

```go
package main

import (
	"encoding/json"
	"testing"
)

func TestActionsCmdJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := writeTempConfig(t, dir)
	mustRun(t, "init", "--config", cfg)

	// Seed a note with a spread of tasks, then reindex.
	writeVaultNote(t, dir, "01-Projects/p.md",
		"## Todo\n- [ ] overdue one 📅 2000-01-01\n- [ ] someday one #someday\n- [x] done one\n")
	mustRun(t, "reindex", "--config", cfg)

	out := mustRun(t, "actions", "--json", "--config", cfg)
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	// done excluded by default; overdue + someday remain.
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (open only): %s", len(items), out)
	}
	var buckets []string
	for _, it := range items {
		buckets = append(buckets, it["bucket"].(string))
	}
	if !hasStr(buckets, "overdue") || !hasStr(buckets, "someday") {
		t.Errorf("buckets=%v want overdue+someday", buckets)
	}
}

func hasStr(ss []string, w string) bool {
	for _, s := range ss {
		if s == w {
			return true
		}
	}
	return false
}
```

> Use the package's real CLI-test helpers. `mustRun`/`writeVaultNote` here are placeholders for whatever `cmd/axon/cli_test.go` exposes (`run(...)` returning output+err, and a note-writing helper). Read `related_cmd_test.go` + `cli_test.go` first and match them exactly; the assertions (default excludes done; `bucket` present in JSON) are the contract.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestActionsCmdJSON -v`
Expected: FAIL — `unknown command "actions"`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/axon/actions_cmd.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// bucketOrder is the GTD engage order for display + the `week` aggregate slot.
var bucketOrder = map[string]int{
	"overdue": 0, "today": 1, "week": 2, "next": 3,
	"waiting": 4, "someday": 5, "scheduled": 6, "done": 7, "cancelled": 8,
}

type actionItem struct {
	db.Action
	Bucket string `json:"bucket"`
}

func newActionsCmd(gf *globalFlags) *cobra.Command {
	var status, project, context string
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List and filter actions (tasks) across the vault — no model call (FR-159)",
		Long: "Lists checkbox tasks parsed from the whole vault, grouped by GTD bucket\n" +
			"(overdue/today/next/waiting/someday/…). Read-only, zero tokens.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			rows, err := db.ListActions(cmd.Context(), deps.db, db.ListActionsOpts{IncludeAll: all})
			if err != nil {
				return err
			}
			today := time.Now()
			items := make([]actionItem, 0, len(rows))
			for _, r := range rows {
				b := actions.Bucket(toActionValue(r), today)
				if !all && (b == "done" || b == "cancelled") {
					continue
				}
				items = append(items, actionItem{r, b})
			}
			items, err = filterActions(items, status, project, context, today)
			if err != nil {
				return err
			}
			sort.SliceStable(items, func(i, j int) bool {
				if bi, bj := bucketOrder[items[i].Bucket], bucketOrder[items[j].Bucket]; bi != bj {
					return bi < bj
				}
				return items[i].Due < items[j].Due
			})

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			renderActions(out, rows, items, today)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter: a bucket (overdue|today|scheduled|next|waiting|someday|done|cancelled) or open|week")
	cmd.Flags().StringVar(&project, "project", "", "filter by project (wikilink target or source-path substring)")
	cmd.Flags().StringVar(&context, "context", "", "filter by @context (without the @)")
	cmd.Flags().BoolVar(&all, "all", false, "include done, cancelled and archived actions")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit results as JSON")
	return cmd
}

func toActionValue(r db.Action) actions.Action {
	return actions.Action{
		SourcePath: r.SourcePath, Section: r.Section, Text: r.Text, Raw: r.Raw,
		State: actions.State(r.State), Checkbox: r.Checkbox, Priority: r.Priority,
		Due: r.Due, Scheduled: r.Scheduled, Start: r.Start, DoneDate: r.DoneDate,
		Project: r.Project, Contexts: r.Contexts, Tags: r.Tags, Archived: r.Archived,
	}
}

func filterActions(items []actionItem, status, project, ctx string, today time.Time) ([]actionItem, error) {
	valid := map[string]bool{"overdue": true, "today": true, "scheduled": true,
		"next": true, "waiting": true, "someday": true, "done": true,
		"cancelled": true, "open": true, "week": true}
	if status != "" && !valid[status] {
		return nil, fmt.Errorf("unknown --status %q (want one of: overdue today scheduled next waiting someday done cancelled open week)", status)
	}
	weekEnd := today.AddDate(0, 0, 7).Format("2006-01-02")
	tStr := today.Format("2006-01-02")
	out := items[:0]
	for _, it := range items {
		if status != "" {
			switch status {
			case "open": // already open-only unless --all; keep
			case "week":
				if !(it.Due != "" && it.Due >= tStr && it.Due <= weekEnd) {
					continue
				}
			default:
				if it.Bucket != status {
					continue
				}
			}
		}
		if project != "" && it.Project != project && !strings.Contains(it.SourcePath, project) {
			continue
		}
		if ctx != "" && !hasCtx(it.Contexts, ctx) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func hasCtx(cs []string, want string) bool {
	for _, c := range cs {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}

func renderActions(out interface { Write([]byte) (int, error) }, all []db.Action, items []actionItem, today time.Time) {
	sty := ui.For(out)
	c := summarizeActions(all, today)
	fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf(
		"Open %d · Overdue %d · Today %d · Waiting %d · Someday %d · Done(7d) %d",
		c["open"], c["overdue"], c["today"], c["waiting"], c["someday"], c["done7"])))
	if len(items) == 0 {
		fmt.Fprintf(out, "%s\n", sty.Dim("no matching actions"))
		return
	}
	if tui.Interactive(out) {
		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{it.Bucket, it.Due, it.Text, it.SourcePath})
		}
		tui.Table(out, []string{"BUCKET", "DUE", "TASK", "SOURCE"}, rows)
		return
	}
	for _, it := range items {
		due := it.Due
		if due == "" {
			due = "—"
		}
		fmt.Fprintf(out, "%s  %s  %s %s\n",
			sty.Bold(fmt.Sprintf("%-9s", it.Bucket)), sty.Dim(due),
			it.Text, sty.Dim("("+it.SourcePath+")"))
	}
}

func summarizeActions(rows []db.Action, today time.Time) map[string]int {
	c := map[string]int{}
	weekAgo := today.AddDate(0, 0, -7).Format("2006-01-02")
	for _, r := range rows {
		switch b := actions.Bucket(toActionValue(r), today); b {
		case "done":
			if r.DoneDate >= weekAgo {
				c["done7"]++
			}
		case "cancelled":
			// not counted
		default:
			c["open"]++
			c[b]++
		}
	}
	return c
}
```

> **Type note:** if `ui.For`/`tui.*` expect `io.Writer`, change `renderActions`'s `out` param to `io.Writer` (import `io`) — match `related_cmd.go`, which passes `cmd.OutOrStdout()` directly. The inline interface above is only to avoid an extra import if the package already avoids `io`; prefer `io.Writer` to match the existing file.

Then in `cmd/axon/root.go`, add `newActionsCmd(gf)` to the data-command `root.AddCommand(...)` line (next to `newSearchCmd`, `newRelatedCmd`):

```go
	root.AddCommand(newIngestCmd(gf), newSearchCmd(gf), newAskCmd(gf), newRelatedCmd(gf), newActionsCmd(gf), newStatusCmd(gf), newSubscribeCmd(gf))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestActionsCmdJSON -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/actions_cmd.go cmd/axon/root.go cmd/axon/actions_cmd_test.go
git commit -m "feat(T1): axon actions CLI (FR-159)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: Doctor — `actionsCheck`

**Files:**
- Modify: `internal/core/doctor.go` (add `actionsCheck`, append to `Doctor`)
- Test: `internal/core/actions_doctor_test.go` (create)

**Interfaces:**
- Consumes: `config.ResolvedPaths` (`paths.DBPath`); `db.ActionStateCounts` (Task 4); the `Check{Name, Status, Detail}` + `StatusOK` types; `sql.Open("sqlite", …)` (as `memoryFactsCheck` does).
- Produces: an advisory `actions` check in doctor output.

- [ ] **Step 1: Write the failing test**

Create `internal/core/actions_doctor_test.go` (mirror `merge_doctor_test.go`/`related_doctor_test.go` for how they build `paths`/populate the DB):

```go
package core

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func TestActionsCheck(t *testing.T) {
	paths, sqlDB := newDoctorFixture(t) // existing helper pattern: resolved paths + migrated DB at paths.DBPath
	ctx := context.Background()
	_ = db.ReplaceActions(ctx, sqlDB, []db.Action{
		{Hash: "h", SourcePath: "a.md", LineNo: 1, Text: "x", Raw: "- [ ] x",
			State: "open", Checkbox: " ", Updated: "2026-07-10T00:00:00Z"},
	})
	c := actionsCheck(paths)
	if c.Status != StatusOK {
		t.Errorf("status=%v want StatusOK", c.Status)
	}
	if !contains(c.Detail, "1 action") {
		t.Errorf("detail=%q should report the count", c.Detail)
	}
}
```

> Adapt `newDoctorFixture`/`contains` to the package's existing doctor-test scaffolding (read `merge_doctor_test.go`). The contract: `StatusOK` always, detail names the count.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestActionsCheck -v`
Expected: FAIL — `undefined: actionsCheck`.

- [ ] **Step 3: Write minimal implementation**

In `internal/core/doctor.go`, add (near `memoryFactsCheck`):

```go
// actionsCheck reports the derived action-index size. Advisory (always StatusOK):
// the index is read-only and rebuilt from Markdown by reindex.
func actionsCheck(paths config.ResolvedPaths) Check {
	const name = "actions"
	ctx := context.Background()
	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{name, StatusOK, "no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{name, StatusOK, "database not readable; skipped"}
	}
	defer func() { _ = d.Close() }()

	total, open, done, cancelled, _, err := db.ActionStateCounts(ctx, d)
	if err != nil {
		return Check{name, StatusOK, "actions not counted; skipped"}
	}
	return Check{name, StatusOK,
		fmt.Sprintf("%d actions indexed (%d open / %d done / %d cancelled)", total, open, done, cancelled)}
}
```

Then append it in `Doctor(...)` alongside the other profile-scoped advisory checks (next to `mergeCheck`/`relatedCheck`):

```go
	checks = append(checks, actionsCheck(paths))
```

> Use the same `paths` variable name the surrounding `Doctor` body uses for `config.ResolvedPaths` (grep `mergeCheck(` / `relatedCheck(` call sites for the exact argument).

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestActionsCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/actions_doctor_test.go
git commit -m "feat(T1): advisory doctor actions check

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 8: Full green + docs + live smoke

**Files:**
- Modify: `docs/03-requirements.md` (FR-157/158/159 rows)
- Modify: `docs/02-architecture.md` (ADR-033)
- Modify: `docs/04-data-model-and-config.md` (`actions` DDL block)
- Modify: `docs/16-roadmap-1.2.5.md` (T1 line marked BUILT)
- Modify: `CLAUDE.md` (FR range → FR-159, ADR range → ADR-033, migration note)

**Interfaces:** none (docs + verification).

- [ ] **Step 1: Whole-suite green + lint**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./... && golangci-lint run`
Expected: all pass. Fix any `gofmt`/`goimports`/`go vet` drift (e.g. the inline-interface note in Task 6 → `io.Writer`).

- [ ] **Step 2: Write the docs**

- `docs/03-requirements.md`: add three rows in the FR table —
  - **FR-157** Action grammar & parser: tolerant Obsidian-Tasks emoji-grammar checkbox parser (`internal/actions`); the single structured task parser.
  - **FR-158** Derived action index: disposable `actions` SQLite table rebuilt in the reindex transaction from Markdown (S9); read-time GTD bucket.
  - **FR-159** `axon actions` CLI: list/filter/count actions, `--json`, zero model calls.
- `docs/02-architecture.md`: add **ADR-033 — Action model** (status: built). Context: tasks scattered across the vault as checkbox lines; decision: Obsidian-Tasks emoji grammar is the single source of truth, AXON adds conventions (`#someday`/`#waiting`/`@context`) not syntax; a derived disposable `actions` table (the `memory_facts`/ADR-028 precedent) rebuilt in the reindex tx; identity = `sha256(path + state-independent normalized line)`; read-time bucket (no midnight writer); tolerant 3-state parsing; index all-but-system-dirs with `04-Archive/` flagged. Consequences: the one write (completion) gets its own ADR-034 in T3; T1 is read-only. Follow the ADR format of ADR-028/032.
- `docs/04-data-model-and-config.md`: add the `actions` `CREATE TABLE` block (from Task 4) to §2 with a "DERIVED (ADR-033); reindex delete-all+inserts from Markdown" comment, next to the `memory_facts` block.
- `docs/16-roadmap-1.2.5.md`: mark the **T1** line ✅ **BUILT 2026-07-10** with the final IDs (FR-157/158/159, ADR-033).
- `CLAUDE.md`: bump the doc-pack line ranges — `docs/02` ADR-001…**033**, `docs/03` FR-01…**159**; note migration `0007_actions.sql`.

- [ ] **Step 3: Live smoke (real binary, isolated AXON_HOME — never touch :7777)**

```bash
go build -o /tmp/axon-t1 ./cmd/axon
export AXON_HOME=$(mktemp -d)
/tmp/axon-t1 init --yes 2>/dev/null || /tmp/axon-t1 init
# seed a spread of tasks into the scratch vault (path from the printed config), then:
/tmp/axon-t1 reindex
/tmp/axon-t1 actions
/tmp/axon-t1 actions --status overdue
/tmp/axon-t1 actions --json | head -40
/tmp/axon-t1 reindex && /tmp/axon-t1 actions --json > /tmp/a1.json
/tmp/axon-t1 reindex && /tmp/axon-t1 actions --json > /tmp/a2.json
diff /tmp/a1.json /tmp/a2.json && echo "S9 OK: byte-identical"
/tmp/axon-t1 doctor | grep -i actions
```

Expected: counts header + bucketed list; `--status overdue` filters; `--json` carries `bucket`; the two reindexes produce identical JSON (S9); doctor shows the `actions` line. No Claude/Ollama needed (zero model calls). Skip scratch cleanup (GateGuard blocks `rm -rf`).

- [ ] **Step 4: Commit docs**

```bash
git add docs/ CLAUDE.md
git commit -m "docs(T1): FR-157/158/159, ADR-033, actions DDL, roadmap T1 built

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Read one neighbor first** for each package's test scaffolding: `internal/db`'s `newMigratedDB`; `internal/core`'s reindex + doctor test helpers; `cmd/axon`'s `cli_test.go` (`run`/`writeTempConfig`). The plan's assertions are the contract; match the harness the package already uses rather than inventing helpers.
- **No embeddings.** The `actions` table has no vector column and no `EmbedPending…` pass — do not add one (a "similar tasks" feature is explicitly out of scope).
- **Read-only.** No task in T1 writes to the vault. If you find yourself calling `vault.Patch`/`Create`/`os.WriteFile` on vault content, you've drifted into T2/T3.
- **GateGuard:** first Write/Edit/Bash each turn triggers a fact-force preamble (comply tersely); `git commit --amend` and `rm -rf` are blocked (use follow-up commits; leave scratch dirs).
```
