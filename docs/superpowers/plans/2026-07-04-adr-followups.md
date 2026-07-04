# ADR Follow-up Slices Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the three ADR-noted follow-ups — link-suggester proposal memory (FR-102), review-queue compaction on resolve (FR-103), SessionEnd capture (FR-104).

**Architecture:** Three independent slices on one branch. Slice 1 generalizes the resurfacer's proposal-memory pattern into shared `internal/automations` helpers and adopts it in the link-suggester. Slice 2 adds a pure `compact()` function to `internal/review`, wired into `mark()` so resolved lines older than 7 days archive to `.axon/review-queue-archive.md` during the rewrite a resolution already performs. Slice 3 adds a `SessionEnd` hook event that records sessions with a sticky `ended` flag so the distiller picks them up without the 30-minute idle wait.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (via existing `internal/db` repositories), stdlib only — no new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-04-adr-followups-design.md`

## Global Constraints

- Branch: `feature/adr-followups` (already created; spec committed).
- Hooks never call a model and never break a session (every failure silent).
- Vault mutations stay wikilink-safe; `.axon/` writes only through `vault.Append` / `vault.RewriteSystemFile`.
- Automations persist nothing in dry-run.
- Run all tests with `env -u FORCE_COLOR` (the ambient shell exports `FORCE_COLOR=3`, which breaks some suites).
- `gofmt` clean; `go vet ./...` green.
- Constants, not config: `proposalMemoryCap = 500`, `archiveAfterDays = 7`, `archivePath = ".axon/review-queue-archive.md"`.

## File Structure

- `internal/automations/helpers.go` — gains `loadProposalMemory` / `saveProposalMemory` + `proposalMemoryCap` (shared by resurfacer and link-suggester).
- `internal/automations/proactive.go` — resurfacer delegates to the shared helpers; its private memory functions are deleted.
- `internal/automations/nomodel.go` — LinkSuggester adopts proposal memory (`link-suggester:proposed`).
- `internal/review/review.go` — `compact()` + archive constants + wiring in `mark()`.
- `internal/db/sessions.go` — `PendingSession.Ended bool`.
- `internal/hooks/hooks.go` — `SessionEnd` event constant + routing; `recordSession` gains an `ended` param with sticky semantics.
- `internal/claudeassets/claudeassets.go` — generated hook settings gain a SessionEnd entry.
- `cmd/axon/hook_cmd.go` — help text mentions SessionEnd.
- `internal/automations/sessionmem.go` — `readySessions` treats ended sessions as immediately ready.
- Docs: `docs/02-architecture.md` (ADR-018/020/021 amendments), `CHANGELOG.md`.

---

### Task 1: Shared proposal memory + link-suggester adoption (FR-102)

**Files:**
- Modify: `internal/automations/helpers.go` (append at end)
- Modify: `internal/automations/proactive.go:143-150, 204, 260, 273-307`
- Modify: `internal/automations/nomodel.go:150-241`
- Test: `internal/automations/standard_test.go` (append), `internal/automations/proactive_test.go` (existing resurfacer tests must stay green)

**Interfaces:**
- Consumes: `db.GetCursor(ctx, q, key) (string, error)` / `db.SetCursor(ctx, q, key, value, updated) error`; `pairKey(a, b string) string` (proactive.go:266 — canonicalizes an unordered pair, unchanged); `RunCtx` fields `DB`, `Log`, `now()`, `DryRun`.
- Produces: `loadProposalMemory(ctx context.Context, rc RunCtx, stateKey string) map[string]bool` and `saveProposalMemory(ctx context.Context, rc RunCtx, stateKey string, proposed map[string]bool)` in package `automations`; state keys `resurfacer:proposed` (unchanged) and `link-suggester:proposed` (new).

- [ ] **Step 1: Write the failing tests**

Append to `internal/automations/standard_test.go`:

```go
// TestLinkSuggesterProposalMemory proves a proposed pair is never re-queued
// (FR-102): the second run finds nothing new, and the memory row persists.
func TestLinkSuggesterProposalMemory(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.md": "# Vector Databases\n\nVector databases index embeddings for similarity search.\n",
		"b.md": "# Semantic Search\n\nSemantic search uses embeddings and vector databases.\n",
	} {
		f := filepath.Join(dir, name)
		_ = os.WriteFile(f, []byte(body), 0o644)
		if _, err := rc.Pipeline.Ingest(ctx, f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
			t.Fatal(err)
		}
	}
	mustReindex(t, rc)

	res, err := LinkSuggester{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Fatal("first run should propose at least one link")
	}
	raw, err := db.GetCursor(ctx, rc.DB, "link-suggester:proposed")
	if err != nil || raw == "" {
		t.Fatalf("proposal memory not persisted: %q, %v", raw, err)
	}

	res2, err := LinkSuggester{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Changes) != 0 {
		t.Fatalf("second run re-proposed: %v", res2.Changes)
	}
}

// TestLinkSuggesterDryRunPersistsNothing: dry-run proposes but leaves no
// memory row, so a later real run still queues the pairs.
func TestLinkSuggesterDryRunPersistsNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.md": "# Vector Databases\n\nVector databases index embeddings for similarity search.\n",
		"b.md": "# Semantic Search\n\nSemantic search uses embeddings and vector databases.\n",
	} {
		f := filepath.Join(dir, name)
		_ = os.WriteFile(f, []byte(body), 0o644)
		if _, err := rc.Pipeline.Ingest(ctx, f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
			t.Fatal(err)
		}
	}
	mustReindex(t, rc)

	rc.DryRun = true
	if _, err := LinkSuggester{}.Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if raw, _ := db.GetCursor(ctx, rc.DB, "link-suggester:proposed"); raw != "" {
		t.Fatalf("dry-run persisted memory: %q", raw)
	}
}

// TestProposalMemoryCap: the shared helper keeps at most proposalMemoryCap
// keys (lexicographic tail, matching the resurfacer's existing behaviour).
func TestProposalMemoryCap(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	m := map[string]bool{}
	for i := 0; i < proposalMemoryCap+50; i++ {
		m[fmt.Sprintf("pair-%04d", i)] = true
	}
	saveProposalMemory(ctx, rc, "test:proposed", m)
	got := loadProposalMemory(ctx, rc, "test:proposed")
	if len(got) != proposalMemoryCap {
		t.Fatalf("cap not enforced: got %d keys", len(got))
	}
}
```

`standard_test.go` already imports `context`, `os`, `path/filepath`, `testing`, and `github.com/jandro-es/axon/internal/ingestion`; add `fmt` and `github.com/jandro-es/axon/internal/db` if not present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestLinkSuggesterProposalMemory|TestLinkSuggesterDryRunPersistsNothing|TestProposalMemoryCap' -v`
Expected: compile FAIL — `undefined: loadProposalMemory`, `undefined: proposalMemoryCap` (and the memory tests would fail even after compile, since Run persists nothing yet).

- [ ] **Step 3: Add the shared helpers**

Append to `internal/automations/helpers.go` (add `encoding/json`, `sort`, `time`, and `github.com/jandro-es/axon/internal/db` to its imports as needed):

```go
// ---- proposal memory (shared by resurfacer + link-suggester, FR-90/FR-102) --

// proposalMemoryCap bounds each automation's persistent proposal memory.
const proposalMemoryCap = 500

// loadProposalMemory reads an automation's proposed-pair memory from its
// automation_state row (empty on any problem — worst case a pair is
// proposed twice).
func loadProposalMemory(ctx context.Context, rc RunCtx, stateKey string) map[string]bool {
	out := map[string]bool{}
	raw, err := db.GetCursor(ctx, rc.DB, stateKey)
	if err != nil || raw == "" {
		return out
	}
	var keys []string
	_ = json.Unmarshal([]byte(raw), &keys)
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// saveProposalMemory persists proposal memory beside the engine cursor,
// capped at the newest entries.
func saveProposalMemory(ctx context.Context, rc RunCtx, stateKey string, proposed map[string]bool) {
	keys := make([]string, 0, len(proposed))
	for k := range proposed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > proposalMemoryCap {
		keys = keys[len(keys)-proposalMemoryCap:]
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, stateKey, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("proposal memory: persist", "key", stateKey, "err", err)
	}
}
```

- [ ] **Step 4: Point the resurfacer at the shared helpers**

In `internal/automations/proactive.go`:

1. Delete `resurfaceMemoryCap = 500` from the const block (line ~148) — the shared `proposalMemoryCap` replaces it. Keep `resurfacerProposedState = "resurfacer:proposed"`.
2. Replace the call sites:
   - Line ~204: `proposed := loadResurfacerMemory(ctx, rc)` → `proposed := loadProposalMemory(ctx, rc, resurfacerProposedState)`
   - Line ~260: `saveResurfacerMemory(ctx, rc, proposed)` → `saveProposalMemory(ctx, rc, resurfacerProposedState, proposed)`
3. Delete the whole `loadResurfacerMemory` and `saveResurfacerMemory` functions (lines ~273-307).
4. Remove now-unused imports if the compiler flags them (`encoding/json` may still be used elsewhere in the file — trust the compiler).

- [ ] **Step 5: Adopt proposal memory in LinkSuggester**

In `internal/automations/nomodel.go`, add a const above the LinkSuggester type (line ~152):

```go
const linkSuggesterProposedState = "link-suggester:proposed"
```

Replace `LinkSuggester.Run` (lines ~177-241) with:

```go
func (l LinkSuggester) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	max := l.MaxSuggestions
	if max <= 0 {
		max = 10
	}
	paths, err := rc.Vault.List(ctx)
	if err != nil {
		return RunResult{}, err
	}
	sort.Strings(paths)

	// Proposal memory (FR-102): pairs already queued once — accepted or
	// dismissed — are never re-proposed. Unordered: direction is noise.
	proposed := loadProposalMemory(ctx, rc, linkSuggesterProposedState)

	type suggestion struct{ from, to string }
	var suggestions []suggestion
	seen := map[string]bool{}

	for _, p := range paths {
		if len(suggestions) >= max {
			break
		}
		n, err := rc.Vault.Read(ctx, p)
		if err != nil || strings.TrimSpace(n.Body) == "" {
			continue
		}
		hits, err := rc.Searcher.Search(ctx, firstWords(n.Body, 40), 5)
		if err != nil {
			continue
		}
		existing := linkTargets(n.Body)
		for _, h := range hits {
			if h.Path == "" || h.Path == p {
				continue
			}
			key := pairKey(p, h.Path)
			if seen[key] || proposed[key] || existing[stripExt(h.Path)] || existing[base(h.Path)] {
				continue
			}
			seen[key] = true
			suggestions = append(suggestions, suggestion{p, h.Path})
			if len(suggestions) >= max {
				break
			}
		}
	}

	if len(suggestions) == 0 {
		return RunResult{Summary: "no new link suggestions"}, nil
	}
	changes := make([]string, len(suggestions))
	for i, s := range suggestions {
		changes[i] = fmt.Sprintf("[[%s]] ↔ [[%s]]", stripExt(s.from), stripExt(s.to))
	}
	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would propose %d link(s)", len(suggestions)), Changes: changes}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n## Link suggestions (%s)\n", rc.now().UTC().Format("2006-01-02 15:04"))
	for _, c := range changes {
		fmt.Fprintf(&b, "- [ ] %s\n", c)
	}
	if err := rc.Vault.Append(".axon/review-queue.md", b.String()); err != nil {
		return RunResult{}, err
	}
	for _, s := range suggestions {
		proposed[pairKey(s.from, s.to)] = true
	}
	saveProposalMemory(ctx, rc, linkSuggesterProposedState, proposed)
	return RunResult{Summary: fmt.Sprintf("proposed %d link(s) in review queue", len(suggestions)), Changes: changes}, nil
}
```

(The only changes from the current body: the `proposed` load, the `key` switching from directional `p + "->" + h.Path` to `pairKey(p, h.Path)`, the `proposed[key]` skip, and the two lines persisting memory after a successful append.)

- [ ] **Step 6: Run the targeted tests**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -v -run 'LinkSuggester|ProposalMemory|Resurfacer'`
Expected: PASS — including the pre-existing resurfacer tests in `proactive_test.go` (behaviour unchanged, same state key).

- [ ] **Step 7: Full package + vet**

Run: `env -u FORCE_COLOR go test ./internal/automations/ && go vet ./internal/automations/`
Expected: PASS, no vet findings.

- [ ] **Step 8: Commit**

```bash
git add internal/automations/helpers.go internal/automations/proactive.go internal/automations/nomodel.go internal/automations/standard_test.go
git commit -m "feat(automations): link-suggester proposal memory via shared helpers (FR-102)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Review-queue compaction on resolve (FR-103)

**Files:**
- Modify: `internal/review/review.go` (constants near line 23; new `compact` function; wiring in `mark()` lines ~207-229)
- Test: `internal/review/review_test.go` (append)

**Interfaces:**
- Consumes: `sectionRe`, `lineRe`, `resolutionRe` (existing regexes in review.go); `vault.FS.Append(rel, content string) error`; `vault.FS.RewriteSystemFile(rel, content string) error`.
- Produces: `compact(content string, now time.Time) (kept, archived string)` (unexported); constants `archivePath = ".axon/review-queue-archive.md"`, `archiveAfterDays = 7`. `mark()` behaviour: archive-append happens before the queue rewrite; the queue is rewritten compacted only when something archived.

- [ ] **Step 1: Write the failing tests**

Append to `internal/review/review_test.go` (add `time` and `fmt` to imports):

```go
func TestCompact(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -10).Format("2006-01-02")  // archivable
	fresh := now.AddDate(0, 0, -2).Format("2006-01-02") // kept
	content := fmt.Sprintf(`
## Link suggestions (2026-06-20 01:00)
- [x] [[a]] ↔ [[b]] — ✓ applied %s
- [x] [[c]] ↔ [[d]] — ✗ dismissed %s

## Inbox triage (2026-06-25 12:30)
- [x] triage [[00-Inbox/idea]] → 02-Areas (tags: t) — ✓ applied %s
- [ ] triage [[00-Inbox/next]] → 02-Areas (tags: t)

## Capture (2026-07-03 22:38)
- [x] captured meeting-notes.txt → [[03-Resources/Knowledge/meeting-notes]] (original: x.txt)
`, old, fresh, old)

	kept, archived := compact(content, now)

	for _, want := range []string{"✗ dismissed " + fresh, "- [ ] triage [[00-Inbox/next]]", "captured meeting-notes.txt", "## Inbox triage", "## Capture"} {
		if !strings.Contains(kept, want) {
			t.Errorf("kept missing %q\nkept:\n%s", want, kept)
		}
	}
	for _, gone := range []string{"✓ applied " + old, "## Link suggestions (2026-06-20 01:00)"} {
		if strings.Contains(kept, gone) {
			t.Errorf("kept still contains %q\nkept:\n%s", gone, kept)
		}
	}
	// Archived lines carry their section headers.
	for _, want := range []string{"## Link suggestions (2026-06-20 01:00)", "[[a]] ↔ [[b]] — ✓ applied " + old, "## Inbox triage (2026-06-25 12:30)", "triage [[00-Inbox/idea]]"} {
		if !strings.Contains(archived, want) {
			t.Errorf("archived missing %q\narchived:\n%s", want, archived)
		}
	}
	// The resolved-but-dateless capture line is never archived (no guesswork).
	if strings.Contains(archived, "captured meeting-notes.txt") {
		t.Error("dateless resolved line must not be archived")
	}
}

func TestCompactNothingToArchive(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	content := "\n## Link suggestions (2026-07-04 01:00)\n- [ ] [[a]] ↔ [[b]]\n"
	_, archived := compact(content, now)
	if archived != "" {
		t.Fatalf("archived should be empty, got %q", archived)
	}
}

// TestDismissCompactsOldResolved: resolving an item also archives resolved
// lines past the threshold, drops their emptied section, and leaves the
// remaining items' IDs stable (FR-103).
func TestDismissCompactsOldResolved(t *testing.T) {
	v := vault.NewFS(t.TempDir())
	if err := os.MkdirAll(filepath.Join(v.Root(), ".axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	queue := fmt.Sprintf(`
## Link suggestions (2026-06-20 01:00)
- [x] [[old-a]] ↔ [[old-b]] — ✓ applied %s

## Resurfaced connections (2026-07-04 07:00)
- [ ] resurface [[dormant]] — related to recent [[current]] (sim 0.82, dormant since 2026-01-14)
- [ ] resurface [[other]] — related to recent [[current]] (sim 0.80, dormant since 2026-02-01)
`, old)
	if err := os.WriteFile(filepath.Join(v.Root(), ".axon", "review-queue.md"), []byte(queue), 0o644); err != nil {
		t.Fatal(err)
	}

	items := mustLoad(t, v)
	target := findKind(items, "resurface")
	if target == nil {
		t.Fatal("no pending resurface item")
	}
	other := ""
	for _, it := range items {
		if it.Kind == "resurface" && it.ID != target.ID {
			other = it.ID
		}
	}

	if _, err := Dismiss(context.Background(), v, target.ID); err != nil {
		t.Fatal(err)
	}

	// Old resolved line + its section moved to the archive.
	arch, err := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue-archive.md"))
	if err != nil {
		t.Fatalf("archive not written: %v", err)
	}
	if !strings.Contains(string(arch), "[[old-a]] ↔ [[old-b]]") || !strings.Contains(string(arch), "## Link suggestions (2026-06-20 01:00)") {
		t.Fatalf("archive content wrong:\n%s", arch)
	}
	qdata, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if strings.Contains(string(qdata), "old-a") || strings.Contains(string(qdata), "## Link suggestions (2026-06-20 01:00)") {
		t.Fatalf("queue not compacted:\n%s", qdata)
	}
	// The just-dismissed line (today's date, < 7 days) is still visible.
	if !strings.Contains(string(qdata), "✗ dismissed") {
		t.Fatalf("fresh resolution vanished:\n%s", qdata)
	}

	// The surviving pending item keeps its identity across the compaction.
	after := mustLoad(t, v)
	found := false
	for _, it := range after {
		if it.ID == other && !it.Checked {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending item lost or ID changed after compaction: %+v", after)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/review/ -run 'TestCompact|TestDismissCompacts' -v`
Expected: compile FAIL — `undefined: compact`.

- [ ] **Step 3: Implement compact + constants**

In `internal/review/review.go`, extend the const at line 23:

```go
const (
	queuePath = ".axon/review-queue.md"
	// archivePath receives resolved lines older than archiveAfterDays when a
	// resolution rewrites the queue (FR-103). Append-only, human-prunable,
	// parsed by nothing.
	archivePath      = ".axon/review-queue-archive.md"
	archiveAfterDays = 7
)
```

Add near `resolutionRe` (line ~184):

```go
// resolvedDateRe extracts the resolution date mark() appends.
var resolvedDateRe = regexp.MustCompile(` — [✓✗] (?:applied|dismissed) (\d{4}-\d{2}-\d{2})$`)
```

Add the pure function after `mark()` at the end of the file (`time` is already imported):

```go
// compact splits queue content into what stays and what archives: resolved
// lines older than archiveAfterDays move out, grouped under their original
// section header; section headers left with no items are dropped from the
// kept content. Pending lines, fresh resolutions, and resolved lines whose
// date does not parse are kept — never archive on guesswork.
func compact(content string, now time.Time) (kept, archived string) {
	cutoff := now.AddDate(0, 0, -archiveAfterDays)
	type section struct {
		header  string
		keep    []string
		archive []string
	}
	cur := &section{} // preamble: lines before the first header
	sections := []*section{cur}
	for _, line := range strings.Split(content, "\n") {
		if sectionRe.MatchString(line) {
			cur = &section{header: line}
			sections = append(sections, cur)
			continue
		}
		if m := lineRe.FindStringSubmatch(line); m != nil && m[1] == "x" {
			if dm := resolvedDateRe.FindStringSubmatch(m[2]); dm != nil {
				if d, derr := time.Parse("2006-01-02", dm[1]); derr == nil && d.Before(cutoff) {
					cur.archive = append(cur.archive, line)
					continue
				}
			}
		}
		cur.keep = append(cur.keep, line)
	}

	var keepB, archB strings.Builder
	for _, s := range sections {
		if len(s.archive) > 0 {
			if s.header != "" {
				archB.WriteString(s.header + "\n")
			}
			archB.WriteString(strings.Join(s.archive, "\n") + "\n")
		}
		hasItem := false
		for _, l := range s.keep {
			if lineRe.MatchString(l) {
				hasItem = true
				break
			}
		}
		if s.header != "" && !hasItem {
			continue // emptied section: drop header and residual blanks
		}
		if s.header != "" {
			keepB.WriteString(s.header + "\n")
		}
		if len(s.keep) > 0 {
			keepB.WriteString(strings.Join(s.keep, "\n") + "\n")
		}
	}
	return keepB.String(), archB.String()
}
```

- [ ] **Step 4: Wire compaction into mark()**

In `mark()` (review.go:208-229), replace the block between the content check and `RewriteSystemFile`:

```go
	content := strings.Replace(string(data), it.Line, newLine, 1)
	if content == string(data) {
		return Item{}, fmt.Errorf("item %s: queue line changed underneath — reload", it.ID)
	}
	// Compaction (FR-103): resolved lines past the threshold move to the
	// archive during the rewrite this resolution already performs.
	// Archive-append precedes the queue rewrite — a crash between the two
	// duplicates an archive line at worst, never loses one.
	if kept, archived := compact(content, time.Now().UTC()); archived != "" {
		stamp := "\n<!-- archived " + time.Now().UTC().Format(time.RFC3339) + " -->\n"
		if err := v.Append(archivePath, stamp+archived); err != nil {
			return Item{}, err
		}
		content = kept
	}
	if err := v.RewriteSystemFile(queuePath, content); err != nil {
		return Item{}, err
	}
```

- [ ] **Step 5: Run the package tests**

Run: `env -u FORCE_COLOR go test ./internal/review/ -v`
Expected: PASS — all pre-existing parse/accept/dismiss tests plus the three new ones.

- [ ] **Step 6: Vet + dependent packages**

Run: `go vet ./internal/review/ && env -u FORCE_COLOR go test ./internal/dashboard/`
Expected: PASS (the dashboard review handlers consume `Load`/`Accept`/`Dismiss`, whose signatures are unchanged).

- [ ] **Step 7: Commit**

```bash
git add internal/review/review.go internal/review/review_test.go
git commit -m "feat(review): compact resolved queue sections into archive on resolve (FR-103)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: SessionEnd capture (FR-104)

**Files:**
- Modify: `internal/db/sessions.go:14-17`
- Modify: `internal/hooks/hooks.go:32-37, 87-91, 284-316`
- Modify: `internal/claudeassets/claudeassets.go:176-183`
- Modify: `internal/claudeassets/claudeassets_test.go:91`
- Modify: `cmd/axon/hook_cmd.go:17-18`
- Modify: `internal/automations/sessionmem.go:55-69`
- Test: `internal/hooks/hooks_test.go` (append), `internal/automations/sessionmem_test.go` (append)

**Interfaces:**
- Consumes: `db.PendingSession`, `db.LoadPendingSessions`, `db.SavePendingSessions`, `db.SessionPendingKey`, `db.SetCursor`; hooks test helpers `testDeps(t)` and `stopPayload(sessionID, transcriptPath string)`; automations test helper `newRC(t, files)`.
- Produces: `db.PendingSession.Ended bool` (JSON `ended,omitempty` — legacy and not-ended rows serialize identically); hooks constant `SessionEnd = "SessionEnd"`; `recordSession(ctx, in, deps, ended bool)` (unexported, sticky-ended semantics); `readySessions` returns ended sessions regardless of idle time.

- [ ] **Step 1: Write the failing tests**

Append to `internal/hooks/hooks_test.go`:

```go
// TestSessionEndRecordsEnded: SessionEnd records like Stop but with a sticky
// ended flag (FR-104); the capture_sessions gate applies.
func TestSessionEndRecordsEnded(t *testing.T) {
	deps, _ := testDeps(t)
	res, err := Handle(context.Background(), SessionEnd, stopPayload("sess-end", "/tmp/t.jsonl"), deps)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("session end: %v %d", err, res.ExitCode)
	}
	pending, _ := db.LoadPendingSessions(context.Background(), deps.DB)
	p, ok := pending["sess-end"]
	if !ok || !p.Ended || p.TranscriptPath != "/tmp/t.jsonl" {
		t.Fatalf("pending = %+v", pending)
	}

	// A later Stop refreshes LastStop but never clears the end marker.
	if _, err := Handle(context.Background(), Stop, stopPayload("sess-end", "/tmp/t.jsonl"), deps); err != nil {
		t.Fatal(err)
	}
	pending, _ = db.LoadPendingSessions(context.Background(), deps.DB)
	if !pending["sess-end"].Ended {
		t.Fatal("Ended must be sticky across a later Stop")
	}

	// Toggle off: SessionEnd records nothing.
	deps2, _ := testDeps(t)
	f := false
	deps2.Memory.CaptureSessions = &f
	_, _ = Handle(context.Background(), SessionEnd, stopPayload("sess-off", "/tmp/t.jsonl"), deps2)
	pending2, _ := db.LoadPendingSessions(context.Background(), deps2.DB)
	if len(pending2) != 0 {
		t.Fatal("toggle off must not record")
	}
}
```

Append to `internal/automations/sessionmem_test.go` (add `slices` to imports if missing):

```go
// TestReadySessionsEndedImmediately: an ended session is ready with no idle
// wait; a fresh non-ended one still waits out the threshold; legacy state
// rows without the ended field keep working (FR-104).
func TestReadySessionsEndedImmediately(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	now := rc.now().UTC()
	pending := map[string]db.PendingSession{
		"ended-fresh": {TranscriptPath: "/tmp/a.jsonl", LastStop: now.Format(time.RFC3339), Ended: true},
		"idle-fresh":  {TranscriptPath: "/tmp/b.jsonl", LastStop: now.Format(time.RFC3339)},
		"idle-old":    {TranscriptPath: "/tmp/c.jsonl", LastStop: now.Add(-40 * time.Minute).Format(time.RFC3339)},
	}
	if err := db.SavePendingSessions(ctx, rc.DB, pending, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	ready, _, err := SessionDistill{}.readySessions(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ended-fresh", "idle-old"}; !slices.Equal(ready, want) {
		t.Fatalf("ready = %v, want %v", ready, want)
	}

	// Legacy row (no ended field in the JSON) unmarshals as not-ended.
	legacy := `{"legacy-old":{"transcript_path":"/tmp/l.jsonl","last_stop":"` +
		now.Add(-40*time.Minute).Format(time.RFC3339) + `"}}`
	if err := db.SetCursor(ctx, rc.DB, db.SessionPendingKey, legacy, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	ready, _, err = SessionDistill{}.readySessions(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"legacy-old"}; !slices.Equal(ready, want) {
		t.Fatalf("legacy ready = %v, want %v", ready, want)
	}
}
```

In `internal/claudeassets/claudeassets_test.go:91`, extend the event list:

```go
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/hooks/ ./internal/automations/ ./internal/claudeassets/ 2>&1 | tail -20`
Expected: compile FAIL in hooks (`undefined: SessionEnd`) and automations (`unknown field Ended`); claudeassets test FAIL (missing SessionEnd hook entry).

- [ ] **Step 3: Add the Ended field**

In `internal/db/sessions.go`:

```go
// PendingSession is one recorded session awaiting distillation.
type PendingSession struct {
	TranscriptPath string `json:"transcript_path"`
	LastStop       string `json:"last_stop"` // RFC3339
	// Ended marks a SessionEnd-recorded session (FR-104): immediately ready
	// to distill, no idle wait. Sticky; absent in legacy rows (= false).
	Ended bool `json:"ended,omitempty"`
}
```

- [ ] **Step 4: Route SessionEnd in hooks**

In `internal/hooks/hooks.go`:

1. Const block (line 32):

```go
const (
	SessionStart = "SessionStart"
	PreToolUse   = "PreToolUse"
	PostToolUse  = "PostToolUse"
	Stop         = "Stop"
	SessionEnd   = "SessionEnd"
)
```

2. Dispatch (line ~87):

```go
	case Stop:
		return stop(ctx, in, deps), nil
	case SessionEnd:
		// Terminal event: record only (sticky ended flag), no advisory output.
		recordSession(ctx, in, deps, true)
		return Result{ExitCode: 0}, nil
```

3. `stop()` (line ~284) passes the new parameter — only its first line changes:

```go
	recordSession(ctx, in, deps, false)
```

4. `recordSession` (line ~295) gains sticky-ended semantics — signature, the `p` construction, and the sticky guard change; the gate checks, eviction loop, and save stay verbatim:

```go
func recordSession(ctx context.Context, in Input, deps Deps, ended bool) {
	if deps.DB == nil || !deps.Memory.SessionCaptureEnabled() ||
		in.SessionID == "" || in.TranscriptPath == "" {
		return
	}
	pending, err := db.LoadPendingSessions(ctx, deps.DB)
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := db.PendingSession{TranscriptPath: in.TranscriptPath, LastStop: now, Ended: ended}
	if prev, ok := pending[in.SessionID]; ok && prev.Ended {
		p.Ended = true // sticky: a later Stop never clears an end marker
	}
	pending[in.SessionID] = p
	for len(pending) > sessionPendingCap {
		oldestID, oldest := "", ""
		for id, pp := range pending {
			if oldest == "" || pp.LastStop < oldest {
				oldestID, oldest = id, pp.LastStop
			}
		}
		delete(pending, oldestID)
	}
	_ = db.SavePendingSessions(ctx, deps.DB, pending, now)
}
```

- [ ] **Step 5: Wire the generated settings + help text**

In `internal/claudeassets/claudeassets.go` (line ~181):

```go
			"Stop":         mk("Stop"),
			"SessionEnd":   mk("SessionEnd"),
```

In `cmd/axon/hook_cmd.go` (line 17), update the help text:

```go
		Long: "Thin handler for Claude Code hooks (SessionStart, PreToolUse, PostToolUse,\n" +
			"Stop, SessionEnd). Reads the hook JSON on stdin and emits the decision/context on stdout.\n" +
```

- [ ] **Step 6: Ended sessions are immediately ready**

In `internal/automations/sessionmem.go`, `readySessions` (line ~55):

```go
func (SessionDistill) readySessions(ctx context.Context, rc RunCtx) ([]string, map[string]db.PendingSession, error) {
	pending, err := db.LoadPendingSessions(ctx, rc.DB)
	if err != nil {
		return nil, nil, err
	}
	cutoff := rc.now().UTC().Add(-sessionIdleMinutes * time.Minute)
	var ready []string
	for id, p := range pending {
		if p.Ended {
			// SessionEnd fired (FR-104): no idle wait.
			ready = append(ready, id)
			continue
		}
		if t, terr := time.Parse(time.RFC3339, p.LastStop); terr == nil && t.Before(cutoff) {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	return ready, pending, nil
}
```

- [ ] **Step 7: Run the tests**

Run: `env -u FORCE_COLOR go test ./internal/hooks/ ./internal/automations/ ./internal/claudeassets/ ./internal/db/ ./cmd/...`
Expected: PASS — new tests plus every pre-existing Stop/session/assets test.

- [ ] **Step 8: Vet + full suite**

Run: `go vet ./... && env -u FORCE_COLOR go test ./...`
Expected: PASS everywhere.

- [ ] **Step 9: Commit**

```bash
git add internal/db/sessions.go internal/hooks/hooks.go internal/hooks/hooks_test.go internal/claudeassets/claudeassets.go internal/claudeassets/claudeassets_test.go cmd/axon/hook_cmd.go internal/automations/sessionmem.go internal/automations/sessionmem_test.go
git commit -m "feat(hooks): SessionEnd capture — sticky ended flag, no idle wait (FR-104)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Docs — ADR amendments + CHANGELOG

**Files:**
- Modify: `docs/02-architecture.md:251` (ADR-018 trade-offs), `docs/02-architecture.md:241` (ADR-020 trade-offs), `docs/02-architecture.md:235` (ADR-021 Why)
- Modify: `CHANGELOG.md` (top unreleased section, same block as the FR-100/101 entries)

**Interfaces:**
- Consumes: nothing from code — pure docs.
- Produces: the contract docs reflect built state; no ADR still calls these follow-ups open.

- [ ] **Step 1: Amend ADR-018 trade-offs (docs/02-architecture.md:251)**

Replace the sentence `Link-suggester still lacks proposal memory — adopting it there is a noted follow-up.` with:

```
The link-suggester adopted the same proposal memory (FR-102, `link-suggester:proposed`, shared helpers), closing the noted follow-up.
```

- [ ] **Step 2: Amend ADR-020 trade-offs (docs/02-architecture.md:241)**

After `Pre-upgrade freeform triage lines stay dismiss-only.` insert:

```
Resolved lines older than 7 days compact into `.axon/review-queue-archive.md` on the next resolution write (FR-103), closing the noted future slice.
```

- [ ] **Step 3: Amend ADR-021 (docs/02-architecture.md:235)**

Replace `SessionEnd wiring (Stop + idleness suffices; revisit-able);` with:

```
SessionEnd wiring (initially deferred; adopted as FR-104 — SessionEnd marks a session immediately ready via a sticky `ended` flag, the idle threshold remaining the crash fallback);
```

- [ ] **Step 4: CHANGELOG entry**

Add to the top section of `CHANGELOG.md`, above the conditional-feed-polling bullet, following the existing bullet style:

```markdown
- **ADR follow-up slices (FR-102…FR-104)** — the link-suggester now remembers
  what it proposed (`link-suggester:proposed`, shared proposal-memory helpers
  with the resurfacer): a dismissed suggestion stays dismissed and embedding
  growth stops re-queuing the same pairs. Resolved review-queue lines older
  than 7 days compact into `.axon/review-queue-archive.md` whenever a
  resolution rewrites the queue (archive-append before rewrite; emptied
  section headers dropped; pending lines untouched). And the generated hook
  settings wire `SessionEnd`: cleanly-ended sessions distill on the next
  tick via a sticky `ended` flag instead of waiting out the 30-minute idle
  heuristic, which stays as the crash fallback. Closes the last ADR-noted
  follow-ups (ADR-018/020/021).
```

- [ ] **Step 5: Commit**

```bash
git add docs/02-architecture.md CHANGELOG.md
git commit -m "docs: FR-102..104 built — ADR-018/020/021 follow-ups closed

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Live smoke (scratch env)

**Files:** none in-repo — everything under the session scratchpad; binary built from the branch.

**Interfaces:**
- Consumes: the built `axon` binary; `axon init`, `axon ingest`, `axon run <automation>`, `axon hook <event>`, `axon doctor`; a running local Ollama (for ingest embeddings — required by the link-suggester smoke).
- Produces: verified end-to-end behaviour for the three slices; evidence quoted in the final report.

- [ ] **Step 1: Build and provision an isolated profile**

```bash
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/84f7638b-ccf6-4b6b-872c-136d5674130c/scratchpad/followups-smoke
mkdir -p "$S/vault"
go build -o "$S/axon" ./cmd/axon
```

Write `$S/config.yaml` (adapt from `axon.config.example.yaml`: `vault_path: $S/vault`, `data_dir: $S/data`, `embeddings.provider: ollama`, automations off except what the smoke runs), then:

```bash
"$S/axon" init --config "$S/config.yaml"
```

Expected: init completes idempotently; `$S/data/axon.db` exists.

- [ ] **Step 2: Smoke FR-102 (proposal memory)**

```bash
printf '# Vector Databases\n\nVector databases index embeddings for similarity search.\n' > "$S/a.md"
printf '# Semantic Search\n\nSemantic search uses embeddings and vector databases.\n' > "$S/b.md"
"$S/axon" ingest "$S/a.md" --config "$S/config.yaml"
"$S/axon" ingest "$S/b.md" --config "$S/config.yaml"
"$S/axon" run link-suggester --config "$S/config.yaml"   # proposes N ≥ 1
"$S/axon" run link-suggester --config "$S/config.yaml"   # second run: nothing new
sqlite3 "$S/data/axon.db" "select value from automation_state where key='link-suggester:proposed'"
```

Expected: first run proposes ≥ 1 pair into `$S/vault/.axon/review-queue.md`; second reports "no new link suggestions" (or skips on the unchanged cursor — either path proves no duplicate); the state row holds a JSON array of pair keys.

- [ ] **Step 3: Smoke FR-103 (compaction)**

Append a back-dated resolved section plus a pending line to `$S/vault/.axon/review-queue.md`, resolve the pending one through the review path (dashboard `POST /api/review/action` with `Content-Type: application/json` + `X-Axon-Review: 1` against the running daemon, the same route the SPA uses), then:

```bash
cat "$S/vault/.axon/review-queue-archive.md"                  # back-dated line + header + stamp
grep -c 'old-a' "$S/vault/.axon/review-queue.md" || true      # 0 — compacted away
```

Expected: archive file exists with the stamped block; queue no longer contains the old section; the freshly resolved line is still in the queue.

- [ ] **Step 4: Smoke FR-104 (SessionEnd)**

```bash
echo '{"hook_event_name":"SessionEnd","session_id":"smoke-1","transcript_path":"/tmp/x.jsonl"}' | \
  "$S/axon" hook SessionEnd --config "$S/config.yaml"
sqlite3 "$S/data/axon.db" "select value from automation_state where key='session-distill:pending'"
```

Expected: the pending row contains `"smoke-1"` with `"ended":true`. (Distillation itself needs a Claude call — out of smoke scope; readiness is covered by unit tests.)

- [ ] **Step 5: Doctor + cleanup**

```bash
"$S/axon" doctor --config "$S/config.yaml"
rm -rf "$S"
```

Expected: doctor passes (warnings acceptable for absent optional services); scratch removed.

---

## Self-Review Notes

- Spec coverage: FR-102 → Task 1; FR-103 → Task 2; FR-104 → Task 3; spec's Docs section → Task 4; spec's live-smoke section → Task 5. The spec's "resurfacer behaviour unchanged" requirement is carried by the pre-existing `proactive_test.go` suite running green in Task 1 Step 6.
- Type consistency: `loadProposalMemory`/`saveProposalMemory` signatures match between Task 1's helper definition and both call sites; `recordSession(ctx, in, deps, ended bool)` matches its two callers; `compact(content string, now time.Time) (kept, archived string)` matches test and wiring.
- The `ended` JSON tag uses `omitempty` — legacy rows and new not-ended rows serialize identically, satisfying the spec's "no migration" requirement in both directions.
