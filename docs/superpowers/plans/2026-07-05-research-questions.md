# A3 — Standing Research Questions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A weekly `research-questions` automation that answers user-authored standing questions (from `03-Resources/Research Questions.md`) with the grounded `ask` engine, rendering answers + citations + a confidence marker into an `axon:answers` managed block.

**Architecture:** Pure composition — no new engine. A new `internal/automations/researchquestions.go` implements the `Automation` interface: parse questions from the note's human region, call `ask.Ask` per question through the chokepoint (`rc.Manager`), render the results into the `axon:answers` block via `rc.Vault.Patch` (cardinal rule 2). Change-gated on question-list hash ∨ new-sources-this-week. Disabled by default.

**Tech Stack:** Go 1.26; the ADR-016/017 automation framework; `internal/ask` (A1); `vault.FS.Patch` managed blocks; `db.CountSourcesSince`.

## Global Constraints

- Go 1.26+; `gofmt`/`goimports` clean; `go vet` + `golangci-lint` green on touched packages.
- Every model call goes through `rc.Manager` (cardinal rule 1) — satisfied by using `ask.Ask`, which does. Never call an agent directly.
- Never edit the note's human region (cardinal rule 2) — only `rc.Vault.Patch` the `axon:answers` block.
- New IDs: **FR-116, FR-117**; **no new ADR** (verified free: FR-113/114/115 → B1, 116–120 free).
- Default `enabled: false` in both profiles.
- Run every test suite with `env -u FORCE_COLOR go test ...`.
- First file create/edit and first Bash command trip the GateGuard fact-forcing hook — state importers/API/schema/instruction, then retry the identical operation.

---

### Task 1: Parsing + rendering pure helpers

**Files:**
- Create: `internal/automations/researchquestions.go` (helpers + shared consts; the automation type lands here in Task 2)
- Test: `internal/automations/researchquestions_test.go` (create)

**Interfaces:**
- Produces: `rqNotePath` const, `rqAnswersBlock` const, `parseQuestions(body string) []string`, `renderAnswers(results []rqResult, weekLabel string) string`, `rqResult` struct `{Question string; Answer ask.Answer}`, `confidenceMarker(a ask.Answer) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/researchquestions_test.go`:

```go
package automations

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/ask"
)

func TestParseQuestions(t *testing.T) {
	body := `# Research Questions

Some intro prose — not a question? still prose because it is not a list item.

- How does spaced repetition work?
- [ ] What did I decide about SQLite vs Postgres?
- [x] Already answered but still re-checked?
- a plain bullet with no question mark
* Star bullet question here?

<!-- axon:answers:start -->
### Old answer? this line is inside the block and must be ignored
<!-- axon:answers:end -->
`
	got := parseQuestions(body)
	want := []string{
		"How does spaced repetition work?",
		"What did I decide about SQLite vs Postgres?",
		"Already answered but still re-checked?",
		"Star bullet question here?",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d questions %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("q[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseQuestionsEmpty(t *testing.T) {
	if q := parseQuestions("# Research Questions\n\nno list items here.\n"); len(q) != 0 {
		t.Fatalf("want 0 questions, got %v", q)
	}
}

func TestRenderAnswers(t *testing.T) {
	results := []rqResult{
		{Question: "What is X?", Answer: ask.Answer{Text: "X is a thing.", Citations: []string{"a/b", "c/d"}}},
		{Question: "What is Y?", Answer: ask.Answer{Text: "Y is one thing.", Citations: []string{"e/f"}}},
		{Question: "What is Z?", Answer: ask.Answer{Refused: true}},
	}
	out := renderAnswers(results, "2026-07-06")
	if !strings.Contains(out, "### What is X?") || !strings.Contains(out, "✅ **Answered**") ||
		!strings.Contains(out, "[[a/b]], [[c/d]]") {
		t.Fatalf("answered entry malformed:\n%s", out)
	}
	if !strings.Contains(out, "📝 **Tentative**") {
		t.Fatalf("tentative (1 citation) entry missing:\n%s", out)
	}
	if !strings.Contains(out, "### What is Z?") || !strings.Contains(out, "🔍 **Open**") {
		t.Fatalf("open entry missing:\n%s", out)
	}
	if !strings.Contains(out, "_Updated 2026-07-06 · 2 answered · 1 open_") {
		t.Fatalf("footer wrong:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestParseQuestions|TestRenderAnswers' -v`
Expected: FAIL — `parseQuestions`/`renderAnswers`/`rqResult` undefined (build error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/automations/researchquestions.go`:

```go
package automations

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jandro-es/axon/internal/ask"
)

const (
	rqNotePath     = "03-Resources/Research Questions.md"
	rqAnswersBlock = "answers"
	rqMarkerStart  = "<!-- axon:answers:start -->"
)

// rqResult pairs a question with the grounded answer it produced this run.
type rqResult struct {
	Question string
	Answer   ask.Answer
}

// rqItemRe matches a top-level markdown list item, capturing an optional
// checkbox and the item text. Leading indentation excludes nested items.
var rqItemRe = regexp.MustCompile(`^[-*] +(?:\[[ xX]\] +)?(.*\S)\s*$`)

// parseQuestions extracts standing questions from the note's HUMAN region: the
// body above the axon:answers marker. A question is a top-level list item whose
// text ends with '?'. AXON's own answer block is never re-parsed.
func parseQuestions(body string) []string {
	human := body
	if i := strings.Index(body, rqMarkerStart); i >= 0 {
		human = body[:i]
	}
	var out []string
	for _, line := range strings.Split(human, "\n") {
		m := rqItemRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[1])
		if strings.HasSuffix(text, "?") {
			out = append(out, text)
		}
	}
	return out
}

// confidenceMarker derives a coarse confidence from the answer, with no extra
// model call: refused → Open, 1 citation → Tentative, ≥2 → Answered.
func confidenceMarker(a ask.Answer) string {
	switch {
	case a.Refused || a.Text == "":
		return "🔍 **Open**"
	case len(a.Citations) >= 2:
		return "✅ **Answered**"
	default:
		return "📝 **Tentative**"
	}
}

// renderAnswers builds the axon:answers block content: one entry per question
// in order, plus a footer with counts. The block is rebuilt whole each run.
func renderAnswers(results []rqResult, weekLabel string) string {
	var b strings.Builder
	answered := 0
	for _, r := range results {
		fmt.Fprintf(&b, "### %s\n", r.Question)
		if r.Answer.Refused || r.Answer.Text == "" {
			b.WriteString("🔍 **Open** — no grounded answer in the vault yet; will re-attempt next week.\n\n")
			continue
		}
		answered++
		cites := make([]string, len(r.Answer.Citations))
		for i, c := range r.Answer.Citations {
			cites[i] = "[[" + c + "]]"
		}
		fmt.Fprintf(&b, "%s · sources: %s\n\n%s\n\n", confidenceMarker(r.Answer), strings.Join(cites, ", "), strings.TrimSpace(r.Answer.Text))
	}
	fmt.Fprintf(&b, "_Updated %s · %d answered · %d open_", weekLabel, answered, len(results)-answered)
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestParseQuestions|TestRenderAnswers' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/researchquestions.go internal/automations/researchquestions_test.go
git commit -m "feat(automations): research-questions parse + render helpers (FR-117)"
```

---

### Task 2: The `research-questions` automation + registration

**Files:**
- Modify: `internal/automations/researchquestions.go` (add the `ResearchQuestions` type)
- Modify: `internal/automations/registry.go` (register)
- Modify: `internal/automations/catalog.go` (description)
- Test: `internal/automations/researchquestions_test.go` (add automation tests)

**Interfaces:**
- Consumes: `ask.Ask`, `ask.Deps`, `vault.FS.Read/Exists/Patch`, `db.CountSourcesSince`, `weekStart(rc)` (existing in `model.go`), `parseQuestions`, `renderAnswers` (Task 1).
- Produces: `ResearchQuestions` struct implementing `Automation` (`Name/Essential/DetectChange/Run`).

- [ ] **Step 1: Write the failing test**

Add to `internal/automations/researchquestions_test.go` (add `context`, `github.com/jandro-es/axon/internal/agent`, `github.com/jandro-es/axon/internal/config` to imports):

```go
func TestResearchQuestionsAbsentNoteNoOp(t *testing.T) {
	rc, _ := newRC(t, map[string]string{}) // no research-questions note
	ch, err := ResearchQuestions{}.DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Changed {
		t.Fatalf("absent note should not be a change: %+v", ch)
	}
	res, err := ResearchQuestions{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run on absent note errored: %v", err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("absent note run should write nothing: %+v", res)
	}
}

func TestResearchQuestionsAnswersAndPreservesHuman(t *testing.T) {
	note := "# Research Questions\n\n- What is spaced repetition?\n- What is quantum flavour physics?\n\n<!-- axon:answers:start -->\n<!-- axon:answers:end -->\n"
	rc, fake := newRC(t, map[string]string{
		rqNotePath: note,
		"03-Resources/Knowledge/Spaced Repetition.md": "---\ntitle: Spaced Repetition\n---\n\nSpaced repetition schedules reviews at increasing intervals to fight forgetting.\n",
	})
	mustReindex(t, rc)
	fake.RespondFn = func(req agent.Request) (string, error) {
		if strings.Contains(strings.ToLower(req.Messages[len(req.Messages)-1].Content), "spaced repetition") {
			return "Spaced repetition schedules reviews at increasing intervals. [[03-Resources/Knowledge/Spaced Repetition]]", nil
		}
		return "NOT_FOUND", nil
	}

	if _, err := ResearchQuestions{}.Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	out, err := rc.Vault.Read(context.Background(), rqNotePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Body, "- What is spaced repetition?") ||
		!strings.Contains(out.Body, "- What is quantum flavour physics?") {
		t.Fatalf("human questions altered:\n%s", out.Body)
	}
	if !strings.Contains(out.Body, "### What is spaced repetition?") ||
		!strings.Contains(out.Body, "[[03-Resources/Knowledge/Spaced Repetition]]") {
		t.Fatalf("grounded answer missing:\n%s", out.Body)
	}
	if !strings.Contains(out.Body, "### What is quantum flavour physics?") ||
		!strings.Contains(out.Body, "🔍 **Open**") {
		t.Fatalf("open entry missing:\n%s", out.Body)
	}
}

func TestResearchQuestionsDryRunWritesNothing(t *testing.T) {
	note := "# RQ\n\n- What is X?\n\n<!-- axon:answers:start -->\n<!-- axon:answers:end -->\n"
	rc, _ := newRC(t, map[string]string{rqNotePath: note})
	mustReindex(t, rc)
	rc.DryRun = true
	res, err := ResearchQuestions{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "would answer") {
		t.Fatalf("dry-run summary = %q", res.Summary)
	}
	out, _ := rc.Vault.Read(context.Background(), rqNotePath)
	if strings.Contains(out.Body, "###") {
		t.Fatalf("dry-run wrote answers:\n%s", out.Body)
	}
}

func TestResearchQuestionsRegistered(t *testing.T) {
	if _, err := Get(config.Profile{}, "research-questions"); err != nil {
		t.Fatalf("research-questions not registered: %v", err)
	}
}
```

> `newRC`, `mustReindex` are in `standard_test.go`; `agent.Fake.RespondFn` has signature `func(req Request) (string, error)` (confirm in `internal/agent/fake.go` — if it takes/returns differently, adapt the closure).

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestResearchQuestions -v`
Expected: FAIL — `ResearchQuestions` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/automations/researchquestions.go` (extend imports with `context`, `crypto/sha256`, `encoding/hex`, `time`, `github.com/jandro-es/axon/internal/db`):

```go
// ResearchQuestions answers user-authored standing questions from the whole
// vault (A3, FR-116/117): grounded ask per question, rendered into the
// axon:answers managed block. Feature is off until the note exists with a
// question; the human region is never edited (cardinal rule 2).
type ResearchQuestions struct{}

func (ResearchQuestions) Name() string    { return "research-questions" }
func (ResearchQuestions) Essential() bool { return false }

func (ResearchQuestions) questions(ctx context.Context, rc RunCtx) ([]string, error) {
	if !rc.Vault.Exists(rqNotePath) {
		return nil, nil
	}
	n, err := rc.Vault.Read(ctx, rqNotePath)
	if err != nil {
		return nil, err
	}
	return parseQuestions(n.Body), nil
}

func (r ResearchQuestions) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	qs, err := r.questions(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	if len(qs) == 0 {
		return Change{Changed: false, Reason: "no research questions"}, nil
	}
	since := weekStart(rc).Format(time.RFC3339)
	n, err := db.CountSourcesSince(ctx, rc.DB, since)
	if err != nil {
		return Change{}, err
	}
	sum := sha256.Sum256([]byte(strings.Join(qs, "\n")))
	cursor := fmt.Sprintf("%s:%s:%d", hex.EncodeToString(sum[:8]), weekStart(rc).Format("2006-01-02"), n)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "questions + sources unchanged"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d question(s), %d source(s) this week", len(qs), n), Cursor: cursor}, nil
}

func (r ResearchQuestions) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	qs, err := r.questions(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	if len(qs) == 0 {
		return RunResult{Summary: "no research questions (feature inactive)"}, nil
	}
	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would answer %d research question(s)", len(qs)), Changes: []string{rqNotePath}}, nil
	}

	deps := ask.Deps{Searcher: rc.Searcher, Manager: rc.Manager, Config: rc.Config}
	results := make([]rqResult, 0, len(qs))
	answered, est := 0, 0
	for _, q := range qs {
		a, aerr := ask.Ask(ctx, deps, q, 0)
		if aerr != nil {
			// Unexpected transport error: treat as open, keep going.
			a = ask.Answer{Refused: true, Reason: aerr.Error()}
		}
		est += a.Tokens
		if !a.Refused && a.Text != "" {
			answered++
		}
		results = append(results, rqResult{Question: q, Answer: a})
	}

	block := renderAnswers(results, weekStart(rc).Format("2006-01-02"))
	if err := rc.Vault.Patch(ctx, rqNotePath, rqAnswersBlock, block); err != nil {
		return RunResult{}, err
	}
	return RunResult{
		Summary:         fmt.Sprintf("answered %d/%d research question(s)", answered, len(qs)),
		Changes:         []string{rqNotePath},
		EstimatedTokens: est,
	}, nil
}
```

Register in `internal/automations/registry.go` (in the map literal):

```go
		ResearchQuestions{}.Name(): ResearchQuestions{},
```

Add a description in `internal/automations/catalog.go` (match the neighbouring format):

```go
	"research-questions": "Weekly: answers standing questions in 03-Resources/Research Questions.md from the vault, grounded, into an axon:answers block.",
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestResearchQuestions|TestParseQuestions|TestRenderAnswers' -v`
Expected: PASS.
Then `env -u FORCE_COLOR go test ./internal/automations/` — if a test asserts the count of registered automations or catalog entries, bump it by 1.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/researchquestions.go internal/automations/registry.go internal/automations/catalog.go internal/automations/researchquestions_test.go
git commit -m "feat(automations): research-questions automation + registration (FR-116)"
```

---

### Task 3: Config defaults (disabled) in example + starter + work profile

**Files:**
- Modify: `axon.config.example.yaml` (personal profile automations + `work` profile override)
- Modify: `internal/config/starter.go` (starter automation block)
- Test: the nearest config test (add a parse+default assertion)

**Interfaces:** config only — the key must match `Name()` = `research-questions`.

- [ ] **Step 1: Write the failing test**

Add to the existing config test file a check that the example config parses and the automation is present-and-disabled. First confirm the loader the other config tests use (e.g. `Load`/`LoadFile`); mirror it:

```go
func TestResearchQuestionsConfigDisabledByDefault(t *testing.T) {
	cfg, err := Load("../../axon.config.example.yaml", "") // match the real loader signature
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	p := cfg.Profiles["personal"]
	rq, ok := p.Automations["research-questions"]
	if !ok {
		t.Fatal("research-questions missing from example personal profile")
	}
	if rq.Enabled {
		t.Fatal("research-questions must default disabled")
	}
	if rq.Schedule == "" {
		t.Fatal("research-questions needs a schedule")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestResearchQuestionsConfig -v`
Expected: FAIL — key absent.

- [ ] **Step 3: Write minimal implementation**

In `axon.config.example.yaml`, add to the **personal** profile's `automations:` map (next to `knowledge-digest`):

```yaml
      research-questions: { enabled: false, schedule: "30 8 * * 1", model: synthesis, budget_tokens: 150_000 }
```

In the **work** profile's automation overrides (next to `knowledge-digest: { enabled: false }`):

```yaml
      research-questions: { enabled: false }
```

In `internal/config/starter.go`'s `starterTemplate`, add the same personal line to its `automations:` block.

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestResearchQuestionsConfig -v`
Expected: PASS.
Then `env -u FORCE_COLOR go test ./internal/config/` — example/starter round-trip tests stay green.

- [ ] **Step 5: Commit**

```bash
git add axon.config.example.yaml internal/config/starter.go internal/config/
git commit -m "feat(config): research-questions automation defaults (disabled) (FR-116)"
```

---

### Task 4: Scaffold an inert template note

**Files:**
- Create: a note asset under `internal/scaffold/assets/...` mapping to `03-Resources/Research Questions.md`
- Modify: `internal/scaffold/scaffold.go` only if scaffolding is explicit-list-driven (confirm first)
- Test: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Inspect, then write the failing test**

Read `internal/scaffold/scaffold.go` + `scaffold_test.go` to learn the asset→vault mechanism and the test entrypoint. Then add:

```go
func TestScaffoldWritesResearchQuestions(t *testing.T) {
	dir := t.TempDir()
	// invoke the scaffold entrypoint used by the other tests (match its signature)
	// e.g. if Scaffold(dir) or WriteScaffold(v):
	//   v := vault.NewFS(dir); if err := Scaffold(context.Background(), v); err != nil { t.Fatal(err) }
	data, err := os.ReadFile(filepath.Join(dir, "03-Resources", "Research Questions.md"))
	if err != nil {
		t.Fatalf("template not scaffolded: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "<!-- axon:answers:start -->") || !strings.Contains(s, "<!-- axon:answers:end -->") {
		t.Fatalf("answers block missing:\n%s", s)
	}
	if !strings.Contains(s, "```") { // examples fenced → not live questions
		t.Fatalf("examples not fenced:\n%s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/scaffold/ -run TestScaffoldWritesResearchQuestions -v`
Expected: FAIL — file absent.

- [ ] **Step 3: Write minimal implementation**

Create the asset file (path per `scaffold.go`'s convention) with this content:

````markdown
---
title: Research Questions
type: note
---

# Research Questions

Keep standing questions here — write each as a list item ending in "?". The
`research-questions` automation (disabled by default; enable it in config) tries
to answer the open ones every week from your vault, and re-attempts them as your
notes grow. It never edits above this line — answers appear in the block below.

Examples (delete the fence and write your own as real list items):

```
- What did I conclude about spaced repetition?
- How do my project notes connect to my reading highlights?
```

<!-- axon:answers:start -->
<!-- axon:answers:end -->
````

Wire it so it lands at `03-Resources/Research Questions.md`, following the exact mechanism `scaffold.go` already uses (embedded-FS subtree → vault-folder mapping, or an explicit asset list). If the scaffolder maps asset subtrees automatically, placing the file at the matching asset path needs no `scaffold.go` change — confirm by reading it first.

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/scaffold/ -run TestScaffoldWritesResearchQuestions -v`
Expected: PASS. Then `env -u FORCE_COLOR go test ./internal/scaffold/` — if a test asserts the scaffolded file/dir count, bump it.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/
git commit -m "feat(scaffold): inert Research Questions template note (FR-117)"
```

---

### Task 5: Docs — FR-116/117, roadmap, CHANGELOG

**Files:**
- Modify: `docs/03-requirements.md` (FR-116/117 after FR-115)
- Modify: `docs/14-roadmap-1.1.md` (mark A3 built; correct provisional FR-113 → FR-116/117)
- Modify: `CHANGELOG.md`

- [ ] **Step 1** — Add FR-116/117 rows to `docs/03-requirements.md` (1.1 table after FR-115), matching the FR-mapping section of `docs/superpowers/specs/2026-07-05-research-questions-design.md`.

- [ ] **Step 2** — In `docs/14-roadmap-1.1.md`, mark the A3 line `*(built)*` and change `provisional FR-113` → `FR-116/117 (no ADR)`.

- [ ] **Step 3** — Add a CHANGELOG "Added" entry under Unreleased: "Standing research questions (FR-116/117) — a weekly `research-questions` automation answers questions kept in `03-Resources/Research Questions.md` from the vault (grounded `ask`), into an `axon:answers` block; disabled by default, off until the note has a question."

- [ ] **Step 4: Verify + commit**

Run: `grep -n "FR-116\|FR-117" docs/03-requirements.md && grep -n "A3" docs/14-roadmap-1.1.md`
Expected: matches present.

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: FR-116/117 standing research questions; roadmap A3 built"
```

---

## Final verification (before finishing the branch)

- [ ] `env -u FORCE_COLOR go test ./...` — whole suite green.
- [ ] `env -u FORCE_COLOR go vet ./...` — clean.
- [ ] `golangci-lint run ./internal/automations/... ./internal/config/... ./internal/scaffold/...` — green.
- [ ] `go build ./cmd/axon` — one static binary.
- [ ] **Live smoke** in the isolated scratch env: create `03-Resources/Research Questions.md` with one answerable + one unanswerable question; enable `research-questions` in the scratch config; run the automation once; confirm the `axon:answers` block gains a cited `✅/📝` entry + a `🔍 Open` entry, the human bullets are untouched, and a second run rebuilds (no duplication). Delete the note → automation no-ops.
- [ ] Then invoke **superpowers:finishing-a-development-branch** (standing choice: merge to main + push).

## Self-Review

- **Spec coverage:** FR-116 (automation: parse → grounded ask → render, change-gate, chokepoint, deferral-safe) → Tasks 1,2; FR-117 (note contract, clean disable, dry-run, cardinal-rule-2, inert scaffold) → Tasks 1,2,4. Config → Task 3. Docs → Task 5. Every spec test bullet maps to a Task 1/2/4 test.
- **Placeholder scan:** all code steps carry full code. Tasks 3 and 4 contain explicit "confirm the loader signature / read scaffold.go first" inspect-then-implement steps because those seams must be matched exactly, not guessed — the content to write is fully specified; only the call/placement is confirmed against reality.
- **Type consistency:** `parseQuestions(string) []string`, `renderAnswers([]rqResult, string) string`, `rqResult{Question string; Answer ask.Answer}`, `confidenceMarker(ask.Answer) string`, and `ResearchQuestions` implementing `Name/Essential/DetectChange/Run` are used identically across tasks. `ask.Ask(ctx, ask.Deps{...}, q, 0)` matches the A1 signature; `rc.Vault.Patch(ctx, rqNotePath, rqAnswersBlock, block)` matches `vault.FS.Patch`.
