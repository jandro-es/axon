# Orphan & decay report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give orphaned and dormant notes a presence in the vault, and spend link-suggester's proposal budget on the notes that actually need links.

**Architecture:** Three layers, in dependency order. `internal/db` gains one query (`OrphanNotes`) over the existing `links` table — no schema change. `internal/automations` gains one zero-model built-in (`orphan-report`, the 25th) that renders a managed block, and changes one line of ordering inside the shipped `link-suggester`. Registration surfaces (registry, catalog, seeds, docs) follow.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite` (pure-Go, FTS5), table-driven tests.

**Spec:** `docs/superpowers/specs/2026-08-21-orphan-decay-report-design.md`

## Global Constraints

- **FR IDs:** FR-204 (`db.OrphanNotes` + the `orphan-report` automation), FR-205 (orphan-first ordering in `link-suggester`). Trace every change to one of them.
- **No new ADR.** This slice moves no boundary: no new sink, no model path, no egress, no review kind, no schema. If something in implementation seems to need one, stop and raise it.
- **No migration.** Schema stays `0007`. The `links` and `notes` DDL are untouched.
- **Zero model calls.** `orphan-report` never reaches Claude. It has no `runModel` call and no `tokens.AgentCall`.
- **No new proposals.** `orphan-report` writes exactly one managed block and nothing else. It never appends to `.axon/review-queue.md`. Dormant notes are *reported*, never proposed — the resurfacer owns those.
- **Orphan definition (copied verbatim from the spec):** no resolved inbound link **and** no resolved outbound link. Tag edges (`kind = 'tag'`) do not count. A broken outbound link (`dst_note_id IS NULL`) does **not** rescue a note from orphanhood.
- **Caps are Go consts, not config:** `orphanDormantDays = 180`, `orphanListMax = 50` per section. Query for `orphanListMax+1` so truncation is detectable and renders `- …and N more`.
- **Report target:** `03-Resources/Vault Health.md`, managed block `orphans`. Stub-Create-then-Patch; human prose outside the block is never touched.
- **Built-in count goes 24 → 25.** `registry_test.go`'s `want` list, both seeds (`internal/config/starter.go` and `axon.config.example.yaml` — `seeds_test` checks both), the catalog purposes map, `docs/AUTOMATIONS.md`, and README's "All 24 automations" all move together.
- **Seeded disabled** in both seed files (S8: a fresh clone with every automation off must still be useful).
- **Go hygiene:** `gofmt`/`goimports` clean, `go vet` and `golangci-lint` green, errors wrapped with `%w`, `context.Context` propagated. Run `gofmt -l .` before every commit.
- **Never bind port 7777** in any smoke config — that is the live daemon's port. Smoke work uses 7799.

---

### Task 1: `db.OrphanNotes`

**Files:**
- Modify: `internal/db/notes.go` (add after `NotesUpdatedBefore`)
- Test: `internal/db/notes_test.go` (append)

**Interfaces:**
- Consumes: `db.Queryer2` (`internal/db/notes.go:89`), `db.NoteStamp`, `scanNoteStamps`, `db.InsertNote` + `db.NoteRow`, `db.InsertLink` + `db.LinkRow`, `newMigratedDB(t)` (`internal/db/notes_test.go:9`).
- Produces: `db.OrphanNotes(ctx context.Context, q Queryer2, limit int) ([]NoteStamp, error)`. Tasks 2 and 4 call it.

- [ ] **Step 1: Write the failing test**

First check `LinkRow`'s exact field names so the test compiles:

```bash
grep -n "type LinkRow" -A 8 internal/db/notes.go
```

Then append to `internal/db/notes_test.go` (adjust the `LinkRow` literals if the field names differ from `SrcNoteID`/`DstPath`/`DstNoteID`/`Kind`):

```go
func TestOrphanNotes(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	mk := func(path, updated string) int64 {
		id, err := InsertNote(ctx, d, NoteRow{Path: path, Title: path, Updated: updated})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	link := func(src int64, dstPath string, dst *int64, kind string) {
		if err := InsertLink(ctx, d, LinkRow{SrcNoteID: src, DstPath: dstPath, DstNoteID: dst, Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}

	orphan := mk("Z Orphan.md", "2026-08-01")
	inboundOnly := mk("Inbound Only.md", "2026-07-01")
	outboundOnly := mk("Outbound Only.md", "2026-06-01")
	brokenOnly := mk("Broken Only.md", "2026-05-01")
	taggedOnly := mk("Tagged Only.md", "2026-04-01")
	hub := mk("Hub.md", "2026-03-01")

	link(hub, "Inbound Only", &inboundOnly, "wikilink") // gives inboundOnly an inbound edge
	link(outboundOnly, "Hub", &hub, "wikilink")         // gives outboundOnly a resolved outbound edge
	link(brokenOnly, "Nonexistent", nil, "wikilink")    // broken: dst_note_id IS NULL
	link(taggedOnly, "#some-tag", nil, "tag")           // a tag is not a link to a note

	got, err := OrphanNotes(ctx, d, 0)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(got))
	for i, n := range got {
		paths[i] = n.Path
	}
	// Newest first. hub has an outbound edge; inboundOnly and outboundOnly are
	// connected. Broken-only and tag-only are STILL orphans.
	want := []string{"Z Orphan.md", "Broken Only.md", "Tagged Only.md"}
	if len(paths) != len(want) {
		t.Fatalf("want %v, got %v", want, paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("want %v, got %v", want, paths)
		}
	}
	_ = orphan

	// Limit caps the result, newest first.
	one, err := OrphanNotes(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Path != "Z Orphan.md" {
		t.Fatalf("limit 1: want [Z Orphan.md], got %+v", one)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestOrphanNotes -v`
Expected: FAIL — `undefined: OrphanNotes` (compile error). If it instead fails on `LinkRow` field names, fix the literals from the `grep` in Step 1 and re-run.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/db/notes.go`, immediately after `NotesUpdatedBefore`:

```go
// OrphanNotes lists notes disconnected from the wikilink graph (FR-204):
// no resolved inbound link and no resolved outbound link, newest first.
// limit <= 0 means unlimited.
//
// Two deliberate rules. Tag edges (kind = 'tag') do not count — a tag is not
// a link to a note, and counting them would empty the report. A broken
// outbound link (dst_note_id IS NULL) does not rescue a note from orphanhood:
// it points at nothing that exists, so the note is still disconnected.
func OrphanNotes(ctx context.Context, q Queryer2, limit int) ([]NoteStamp, error) {
	query := `SELECT id, path, COALESCE(title,''), COALESCE(updated,'') FROM notes n
	           WHERE NOT EXISTS (SELECT 1 FROM links l
	                              WHERE l.src_note_id = n.id AND l.kind <> 'tag'
	                                AND l.dst_note_id IS NOT NULL)
	             AND NOT EXISTS (SELECT 1 FROM links l
	                              WHERE l.dst_note_id = n.id AND l.kind <> 'tag')
	           ORDER BY updated DESC, path`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := q.QueryContext(ctx, query+";", args...)
	if err != nil {
		return nil, fmt.Errorf("orphan notes: %w", err)
	}
	defer rows.Close()
	return scanNoteStamps(rows)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ -run TestOrphanNotes -v && gofmt -l internal/db/`
Expected: PASS, `gofmt -l` silent.

- [ ] **Step 5: Commit**

```bash
git add internal/db/notes.go internal/db/notes_test.go
git commit -m "feat(db): OrphanNotes — notes with no resolved links in or out (FR-204)"
```

---

### Task 2: The `orphan-report` automation

**Files:**
- Create: `internal/automations/orphans.go`
- Test: `internal/automations/orphans_test.go`

**Interfaces:**
- Consumes: `db.OrphanNotes` (Task 1), `db.NotesUpdatedBefore(ctx, q, beforeDate, limit)`, `RunCtx` (`.DB`, `.Vault`, `.DryRun`, `.LastCursor`, `.now()`), `Change`, `RunResult`, and the package helpers `stripExt`, `hashShort`, `base`, plus `newRC(t, files)` and `seedNote(t, rc, path, title, updated)` from `standard_test.go` / `recipe_test.go`.
- Produces: `type OrphanReport struct{}` implementing `Automation` with `Name() == "orphan-report"`. Task 3 registers it.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/orphans_test.go`:

```go
package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func TestOrphanReportRendersBothSections(t *testing.T) {
	rc, _ := newRC(t, map[string]string{
		"03-Resources/Vault Health.md": "# Vault Health\n\nMy own prose.\n",
	})
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")
	seedNote(t, rc, "03-Resources/Old Idea.md", "Old Idea", "2020-01-01")

	res, err := (OrphanReport{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Summary, "orphan") {
		t.Fatalf("summary should name the finding: %q", res.Summary)
	}
	n, err := rc.Vault.Read(context.Background(), "03-Resources/Vault Health.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "## Orphans (2)") {
		t.Fatalf("orphan section/count missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "[[03-Resources/Zettelkasten]] (updated 2026-06-20)") {
		t.Fatalf("orphan line shape wrong:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "## Dormant (1)") {
		t.Fatalf("dormant section/count missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "[[03-Resources/Old Idea]] (updated 2020-01-01)") {
		t.Fatalf("dormant line shape wrong:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "My own prose.") {
		t.Fatalf("human prose clobbered:\n%s", n.Body)
	}
	if strings.Count(n.Body, "<!-- axon:orphans:end -->") != 1 {
		t.Fatalf("want exactly one end marker:\n%s", n.Body)
	}
}

func TestOrphanReportCreatesStubWhenAbsent(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")
	if _, err := (OrphanReport{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	n, err := rc.Vault.Read(context.Background(), "03-Resources/Vault Health.md")
	if err != nil {
		t.Fatalf("stub not created: %v", err)
	}
	if !strings.Contains(n.Body, "orphan-report") {
		t.Fatalf("stub preamble should name the automation:\n%s", n.Body)
	}
}

func TestOrphanReportChangeGate(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")

	ch, err := (OrphanReport{}).DetectChange(context.Background(), rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first detect: %+v err=%v", ch, err)
	}
	rc.LastCursor = ch.Cursor
	again, err := (OrphanReport{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Fatalf("unchanged topology must skip, got %+v", again)
	}
	// A new orphan re-arms the gate.
	seedNote(t, rc, "03-Resources/Another.md", "Another", "2026-06-21")
	rearmed, err := (OrphanReport{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !rearmed.Changed {
		t.Fatalf("a new orphan must re-arm the gate, got %+v", rearmed)
	}
}

func TestOrphanReportDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")
	res, err := (OrphanReport{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Summary, "would") {
		t.Fatalf("dry-run summary wrong: %q", res.Summary)
	}
	if rc.Vault.Exists("03-Resources/Vault Health.md") {
		t.Fatal("dry-run must not create the report note")
	}
}

func TestOrphanReportTruncates(t *testing.T) {
	rc, _ := newRC(t, nil)
	for i := 0; i < orphanListMax+5; i++ {
		seedNote(t, rc, "03-Resources/Note"+string(rune('A'+i%26))+string(rune('a'+i/26))+".md", "n", "2026-06-20")
	}
	if _, err := (OrphanReport{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	n, _ := rc.Vault.Read(context.Background(), "03-Resources/Vault Health.md")
	if !strings.Contains(n.Body, "…and ") {
		t.Fatalf("a truncated section must say so:\n%s", n.Body)
	}
}

// The dormant section is informational only — this automation never proposes.
func TestOrphanReportNeverTouchesReviewQueue(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Old Idea.md", "Old Idea", "2020-01-01")
	if _, err := (OrphanReport{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("orphan-report must never write proposals")
	}
	_ = db.NoteStamp{}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/automations/ -run TestOrphanReport -v`
Expected: FAIL — `undefined: OrphanReport` (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/automations/orphans.go`:

```go
package automations

import (
	"context"
	"fmt"
	"strings"

	"github.com/jandro-es/axon/internal/db"
)

// Caps are code, not config (the recipe-caps precedent).
const (
	orphanDormantDays = 180
	orphanListMax     = 50
	orphanReportNote  = "03-Resources/Vault Health.md"
	orphanReportBlock = "orphans"
)

// OrphanReport renders the vault's disconnected and dormant notes into a
// managed block (FR-204). Zero model calls and zero proposals: it makes the
// graph's holes visible in the vault itself, where a human — or a recipe
// reading this note — can act on them. Dormant notes are reported but never
// proposed; proactive's resurfacer owns those on its own ladder.
type OrphanReport struct{}

func (OrphanReport) Name() string    { return "orphan-report" }
func (OrphanReport) Essential() bool { return false }

// render builds the report body. Both sections query one past the cap so
// truncation is detectable and never reads as a complete list.
func (OrphanReport) render(ctx context.Context, rc RunCtx) (string, int, int, error) {
	orphans, err := db.OrphanNotes(ctx, rc.DB, orphanListMax+1)
	if err != nil {
		return "", 0, 0, err
	}
	cutoff := rc.now().UTC().AddDate(0, 0, -orphanDormantDays).Format("2006-01-02")
	dormant, err := db.NotesUpdatedBefore(ctx, rc.DB, cutoff, orphanListMax+1)
	if err != nil {
		return "", 0, 0, err
	}

	var b strings.Builder
	section := func(title, blurb string, rows []db.NoteStamp) {
		shown := rows
		extra := 0
		if len(shown) > orphanListMax {
			extra = len(shown) - orphanListMax
			shown = shown[:orphanListMax]
		}
		fmt.Fprintf(&b, "## %s (%d)\n%s\n\n", title, len(rows), blurb)
		for _, n := range shown {
			fmt.Fprintf(&b, "- [[%s]] (updated %s)\n", stripExt(n.Path), n.Updated)
		}
		if len(shown) == 0 {
			b.WriteString("- none\n")
		}
		if extra > 0 {
			fmt.Fprintf(&b, "- …and %d more\n", extra)
		}
		b.WriteString("\n")
	}
	section("Orphans", "Notes with no links in or out — nothing points here, and this points nowhere.", orphans)
	section("Dormant", fmt.Sprintf("Not edited in %d days. The resurfacer proposes these on its own ladder; this list is for orientation.", orphanDormantDays), dormant)
	return strings.TrimSpace(b.String()), len(orphans), len(dormant), nil
}

// DetectChange hashes the rendered body, so a run where neither set moved
// skips with no work (the RecipeRun change-gate pattern).
func (o OrphanReport) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	body, orphans, dormant, err := o.render(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	cursor := "orphans:" + hashShort(body)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no change in orphans or dormant notes"}, nil
	}
	return Change{
		Changed: true,
		Reason:  fmt.Sprintf("%d orphan(s), %d dormant", orphans, dormant),
		Cursor:  cursor,
	}, nil
}

func (o OrphanReport) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	body, orphans, dormant, err := o.render(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	summary := fmt.Sprintf("%d orphan(s), %d dormant → %s", orphans, dormant, orphanReportNote)
	if rc.DryRun {
		return RunResult{Summary: "would write " + summary, Changes: []string{orphanReportNote}}, nil
	}
	if !rc.Vault.Exists(orphanReportNote) {
		stub := "# " + base(orphanReportNote) + "\n\nMaintained by the \"orphan-report\" automation. " +
			"The section below is rewritten on every run; anything outside it is yours.\n"
		if _, cerr := rc.Vault.Create(orphanReportNote, stub); cerr != nil {
			return RunResult{}, cerr
		}
	}
	if perr := rc.Vault.Patch(ctx, orphanReportNote, orphanReportBlock, body); perr != nil {
		return RunResult{}, perr
	}
	return RunResult{Summary: "wrote " + summary, Changes: []string{orphanReportNote}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/automations/ -run TestOrphanReport -v && gofmt -l internal/automations/`
Expected: PASS for all six tests, `gofmt -l` silent.

Note on the two-note fixture: `newRC` seeds no links, so both seeded notes are orphans — hence `## Orphans (2)`. `Old Idea` (2020) also predates the 180-day cutoff relative to the fixed test clock, so it appears in both sections. That is correct behaviour, not a bug: a note can be both disconnected and dormant.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/orphans.go internal/automations/orphans_test.go
git commit -m "feat(automations): orphan-report renders orphans and dormant notes (FR-204)"
```

---

### Task 3: Register the 25th automation and move every count

**Files:**
- Modify: `internal/automations/registry.go`
- Modify: `internal/automations/catalog.go`
- Modify: `internal/automations/registry_test.go` (the `want` list)
- Modify: `internal/config/starter.go`
- Modify: `axon.config.example.yaml`

**Interfaces:**
- Consumes: `OrphanReport{}` (Task 2).
- Produces: `"orphan-report"` present in `Registry(profile)`, in the catalog purposes map, and seeded disabled in both seed files. Task 4 does not depend on this; Task 5 documents it.

- [ ] **Step 1: Run the invariant tests to see them fail**

Add `"orphan-report"` to the `want` list in `internal/automations/registry_test.go` (the last line of the literal, after `"action-extract"`), then run:

Run: `go test ./internal/automations/ ./internal/config/ -run 'Registry|Seeds|Catalog' -v 2>&1 | tail -20`
Expected: FAIL — the registry has 24 entries but `want` now has 25, and `seeds_test` reports `orphan-report` missing from the starter and the example yaml.

- [ ] **Step 2: Register it**

In `internal/automations/registry.go`, add to the map literal beside its neighbours:

```go
		OrphanReport{}.Name():       OrphanReport{},
```

In `internal/automations/catalog.go`, add to the purposes map:

```go
	"orphan-report":       "Weekly zero-model sweep: renders notes with no links in or out, plus notes dormant past 180 days, into an axon:orphans block in 03-Resources/Vault Health.md. Reports only — it proposes nothing and spends nothing. Disabled by default.",
```

- [ ] **Step 3: Seed it disabled in both seed files**

In `internal/config/starter.go`, beside `merge-proposals` (line ~105), matching the surrounding alignment:

```
      orphan-report:     { enabled: false, schedule: "0 10 * * 1",      model: none,      budget_tokens: 0 }
```

In `axon.config.example.yaml`, beside `merge-proposals` (line ~175):

```
      orphan-report:       { enabled: false, schedule: "0 10 * * 1",     model: none,      budget_tokens: 0 }        # weekly orphan + dormant report → axon:orphans block in 03-Resources/Vault Health.md; zero-model, proposes nothing (E1/FR-204, off by default)
```

- [ ] **Step 4: Run the invariant tests to verify they pass**

Run: `go test ./internal/automations/ ./internal/config/ && gofmt -l internal/automations internal/config`
Expected: PASS for both packages, `gofmt -l` silent.

Then confirm the example config still loads:

Run: `go run ./cmd/axon config validate --config axon.config.example.yaml`
Expected: `✓ OK … is valid`.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/registry.go internal/automations/catalog.go internal/automations/registry_test.go internal/config/starter.go axon.config.example.yaml
git commit -m "feat(automations): register orphan-report as the 25th built-in (FR-204)"
```

---

### Task 4: `link-suggester` becomes orphan-first

**Files:**
- Modify: `internal/automations/nomodel.go` (`LinkSuggester.Run`, the `sort.Strings(paths)` at ~line 192)
- Test: `internal/automations/orphans_test.go` (append — it is the FR-205 regression, and lives with the orphan work)

**Interfaces:**
- Consumes: `db.OrphanNotes` (Task 1), `LinkSuggester{MaxSuggestions int}`, `RunCtx`.
- Produces: no new exported symbols. This is the last behaviour task.

- [ ] **Step 1: Write the failing test**

Append to `internal/automations/orphans_test.go`:

```go
// FR-205: link-suggester used to walk the vault alphabetically and stop at
// MaxSuggestions, so an orphan late in the alphabet never received proposals.
// Orphans must now come first.
func TestLinkSuggesterVisitsOrphansFirst(t *testing.T) {
	files := map[string]string{
		"Zzz Orphan.md": "quantum entanglement notes about physics and measurement\n",
	}
	// Several connected notes that sort BEFORE the orphan alphabetically.
	for _, n := range []string{"Aaa", "Bbb", "Ccc", "Ddd"} {
		files[n+".md"] = "physics and measurement notes linking [[Hub]]\n"
	}
	files["Hub.md"] = "the hub note about physics\n"
	rc, _ := newRC(t, files)

	ctx := context.Background()
	// Index the vault so the orphan query and the searcher both see it.
	reindexForTest(t, rc)

	res, err := (LinkSuggester{MaxSuggestions: 2}).Run(ctx, rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	joined := strings.Join(res.Changes, "\n")
	if !strings.Contains(joined, "Zzz Orphan") {
		t.Fatalf("the orphan must be proposed first, got:\n%s\nsummary=%s", joined, res.Summary)
	}
}
```

This test needs the vault indexed. Check whether the package already has a helper:

```bash
grep -rn "func reindexForTest\|Reindex(" internal/automations/*_test.go | head -3
```

If no helper exists, add one to `internal/automations/orphans_test.go` that inserts the note rows and links directly, mirroring `seedNote` — the orphan query reads the `notes`/`links` tables, not the filesystem:

```go
// reindexForTest gives the DB a view of the vault files newRC wrote: every
// file becomes a notes row, and a [[Wikilink]] in the body becomes a links row.
func reindexForTest(t *testing.T, rc RunCtx) {
	t.Helper()
	ctx := context.Background()
	paths, err := rc.Vault.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, p := range paths {
		id, err := db.InsertNote(ctx, rc.DB, db.NoteRow{Path: p, Title: p, Updated: "2026-06-20"})
		if err != nil {
			t.Fatal(err)
		}
		ids[stripExt(p)] = id
	}
	for _, p := range paths {
		n, err := rc.Vault.Read(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`\[\[([^\]]+)\]\]`).FindAllStringSubmatch(n.Body, -1) {
			target := m[1]
			var dst *int64
			if id, ok := ids[target]; ok {
				dst = &id
			}
			if err := db.InsertLink(ctx, rc.DB, db.LinkRow{
				SrcNoteID: ids[stripExt(p)], DstPath: target, DstNoteID: dst, Kind: "wikilink",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
```

Add `"regexp"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/automations/ -run TestLinkSuggesterVisitsOrphansFirst -v`
Expected: FAIL — the orphan sorts last alphabetically, the budget of 2 is spent on `Aaa`/`Bbb`, so `Zzz Orphan` never appears in `res.Changes`.

- [ ] **Step 3: Write minimal implementation**

In `internal/automations/nomodel.go`, replace `sort.Strings(paths)` inside `LinkSuggester.Run` with a call to a new helper, and add the helper below `Run`:

```go
	paths = orphansFirst(ctx, rc, paths)
```

```go
// orphansFirst orders the scan so disconnected notes are visited before
// connected ones (FR-205). link-suggester stops at MaxSuggestions, so a purely
// lexical walk spent its whole budget on early-alphabet notes and never
// reached an orphan — exactly the notes that most need a link. Ordering only:
// the budget, the proposal memory and the accept path are unchanged.
//
// A failing orphan query degrades to plain lexical order rather than taking
// out the run: link suggestions are advisory.
func orphansFirst(ctx context.Context, rc RunCtx, paths []string) []string {
	sort.Strings(paths)
	orphans, err := db.OrphanNotes(ctx, rc.DB, 0)
	if err != nil || len(orphans) == 0 {
		return paths
	}
	rank := make(map[string]int, len(orphans))
	for i, o := range orphans {
		rank[o.Path] = i
	}
	first := make([]string, 0, len(orphans))
	rest := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := rank[p]; ok {
			first = append(first, p)
			continue
		}
		rest = append(rest, p)
	}
	sort.SliceStable(first, func(i, j int) bool { return rank[first[i]] < rank[first[j]] })
	return append(first, rest...)
}
```

Confirm `internal/automations/nomodel.go` already imports `"github.com/jandro-es/axon/internal/db"`; add it if not.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/automations/ -v && gofmt -l internal/automations/ && go vet ./internal/automations/`
Expected: PASS for the whole package — the pre-existing link-suggester tests must still pass, since ordering does not change which pairs are legal, only which are reached first.

- [ ] **Step 5: Full gate, then commit**

```bash
go build ./... && go test ./... && golangci-lint run
git add internal/automations/nomodel.go internal/automations/orphans_test.go
git commit -m "fix(automations): link-suggester visits orphans first, not alphabetically (FR-205)"
```

Expected: whole suite green, 0 lint issues.

---

### Task 5: Documentation, requirements, and both roadmaps

**Files:**
- Modify: `docs/03-requirements.md` (FR-204/FR-205 rows + a section header)
- Modify: `docs/06-component-automation-engine.md`, `docs/AUTOMATIONS.md`
- Modify: `README.md` ("All 24 automations" → 25)
- Modify: `docs/19-roadmap-second-brain.md` (E1 shipped, with the reframing)
- Modify: `docs/20-roadmap-ai-os.md` (C2 P3 closed by composition; C2 header)
- Modify: `CHANGELOG.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: the final shapes from Tasks 1–4. No code.
- Produces: nothing consumed by a later task.

- [ ] **Step 1: Add the FR rows to `docs/03-requirements.md`**

After the FR-203 row, add a new section and the two rows:

```markdown
### Orphan & decay report (docs/19 E1) *(built 2026-08-21)*

FR-204…FR-205 graduate `docs/19` E1 and close `docs/20` C2 Priority 3 by
composition; spec in
`docs/superpowers/specs/2026-08-21-orphan-decay-report-design.md`. No ADR (no
boundary moves), no migration.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-204 | S | **Orphan query + `orphan-report` automation.** `db.OrphanNotes(ctx, q, limit)` returns notes disconnected from the wikilink graph — no resolved inbound **and** no resolved outbound link — newest first, as the existing `NoteStamp`. Tag edges (`kind = 'tag'`) do not count, and a broken outbound link (`dst_note_id IS NULL`) does not rescue a note from orphanhood. The 25th built-in, **`orphan-report`**, is zero-model and proposes nothing: it renders orphans plus notes dormant past `orphanDormantDays` (180, a Go const, as is the per-section cap `orphanListMax` = 50, queried +1 so a truncated section renders `- …and N more`) into the `axon:orphans` managed block of `03-Resources/Vault Health.md`, stub-Creating the note when absent and never touching human prose. The change-gate cursor hashes the rendered body, so an unmoved graph skips with no work; dry-run reports counts and writes nothing. Dormant notes are **reported, never proposed** — `proactive`'s resurfacer owns those. Seeded **disabled** in both the starter and the example config. |
| FR-205 | S | **`link-suggester` visits orphans first.** `LinkSuggester.Run` previously walked `rc.Vault.List` in lexical order and stopped at `MaxSuggestions` (default 10), so it spent its whole budget on early-alphabet notes and never reached an orphan sorting late — the notes most needing links were the least likely to get proposals. Scan order is now orphan-first (query order), then the remainder lexically. Ordering only: the budget, the proposal memory (`linkSuggesterProposedState`), the `pair` line format and the accept path are all unchanged, so the review queue's weekly volume is unaffected. A failing orphan query degrades to plain lexical order rather than failing the run — link suggestions are advisory. |
```

- [ ] **Step 2: Update the automation docs and README**

- `docs/AUTOMATIONS.md`: add an `orphan-report` row to the table (weekly, zero-model, disabled by default, writes `axon:orphans` in `03-Resources/Vault Health.md`, proposes nothing), and add a sentence to the `link-suggester` row noting it now visits orphans first.
- `docs/06-component-automation-engine.md`: add `orphan-report` wherever the standard set is enumerated.
- `README.md`: `All 24 automations` → `All 25 automations`.

Verify no stale count survives:

```bash
grep -rn "24 automations\|all 24" README.md docs/ | grep -v superpowers/specs | grep -v superpowers/plans
```

Expected: no hits (specs and plans are historical records and keep their original numbers).

- [ ] **Step 3: Update both roadmaps**

In `docs/19-roadmap-second-brain.md`, mark E1 shipped and record the reframing honestly:

```markdown
### E1 — Orphan & decay report with proposals (S) · **SHIPPED 2026-08-21 — FR-204/FR-205** (spec: `docs/superpowers/specs/2026-08-21-orphan-decay-report-design.md`)

**Shipped smaller than specified, on purpose.** Reading the code first showed
the proposal half was already built: `link-suggester` proposes `pair` items
from search neighbours and `merge-proposals` proposes `merge` items from
cosine similarity, both with proposal memory and both accepting through
shipped paths. A third proposer would have meant three dedup stores that can
disagree. What was actually missing was (a) any Go-side notion of an orphan —
they were computed only in the SPA — and (b) that `link-suggester` scanned
lexically and stopped at its budget, so orphans late in the alphabet were
never reached. E1 therefore shipped as a zero-model **report** plus an
**ordering fix**, not as a new proposal sweep. The `archive candidate` kind
was rejected as the entry predicted: it is delete-shaped, and there is no
`vault.delete`.
```

In `docs/20-roadmap-ai-os.md`, mark C2 Priority 3 closed:

```markdown
**Priority 3 — link topology / orphans. CLOSED 2026-08-21 by composition, not
built.** This priority existed only because nothing rendered orphans into a
note. `docs/19` E1 now does: `orphan-report` maintains an `axon:orphans` block
in `03-Resources/Vault Health.md`, so a recipe reaches orphans today with an
ordinary `note` reader and no new vocabulary. That was this section's own
stated preference ("Prefer that order").
```

Also update C2's heading so it no longer advertises an open remainder, and
recheck the sequencing sketch for any line that still lists E1 or C2 P3 as
pending.

- [ ] **Step 4: CHANGELOG and CLAUDE.md**

Add to `CHANGELOG.md` under `[Unreleased]` → `### Added`:

```markdown
- **Orphaned and dormant notes now have a place in the vault.** (FR-204,
  FR-205; no ADR, no schema change; graduating `docs/19` E1 and closing
  `docs/20` C2 P3 by composition.) A new zero-model automation,
  `orphan-report` (the 25th, disabled by default), maintains an `axon:orphans`
  block in `03-Resources/Vault Health.md` listing notes with no links in or
  out plus notes untouched for 180 days. It proposes nothing and spends
  nothing — dormant-note proposals stay with the resurfacer.
- **`link-suggester` now visits orphans first.** It previously scanned the
  vault alphabetically and stopped at its proposal budget, so the notes most
  in need of links were the least likely to receive suggestions. Same budget,
  same proposals, spent where the graph is actually broken.
```

In `CLAUDE.md`, update the FR range to `FR-01…FR-205`, and add a line recording E1 alongside the recipes entry, noting built-ins are now 25.

- [ ] **Step 5: Final gate and commit**

```bash
gofmt -l . && go build ./... && go test ./... && golangci-lint run
git add -A
git commit -m "docs: FR-204/FR-205, orphan-report in the automation set, E1 + C2 P3 closed"
```

Expected: everything green.

---

### Task 6: Live smoke in an isolated environment

**Files:**
- Create: `<scratchpad>/orphan-smoke/` (throwaway — never committed)

**Interfaces:**
- Consumes: the built `axon` binary and every change from Tasks 1–5.
- Produces: a verification record for the completion report.

- [ ] **Step 1: Build the binary**

```bash
cd web && npm run build && cd /Users/jandro/Projects/axon
go build -o /private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/orphan-smoke/axon ./cmd/axon
```

The `cd web` persists across the compound command — `cd` back to the repo root by absolute path before `go build`, or the build resolves `./cmd/axon` under `web/`.

- [ ] **Step 2: Build the smoke config from the shipped example, then re-port it**

Never hand-roll a smoke config — validation requires the full field set.

```bash
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/orphan-smoke
mkdir -p "$S/home/profiles/personal" "$S/vault/03-Resources" "$S/vault/01-Projects"
cp axon.config.example.yaml "$S/home/config.yaml"
sed -i '' 's/port: 7777/port: 7799/g' "$S/home/config.yaml"
grep -rn 7777 "$S" && echo "STOP: 7777 still present" || echo "port clean"
```

`port: 7777` appears in **both** the personal and work profiles, and it is the live daemon's port. Re-check with that `grep` before any `axon start`. The `mkdir -p` of the data dir matters: without it SQLite fails with `unable to open database file (14)`.

Then edit `$S/home/config.yaml`: set the personal profile's `vault_path` to `$S/vault` and its `data_dir` to `$S/home/profiles/personal`, and set `orphan-report: { enabled: true, … }`.

- [ ] **Step 3: Seed a vault with a real orphan, a linked pair, and a dormant note**

```bash
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/orphan-smoke
printf '# Zzz Orphan\n\nNothing points here and this points nowhere.\n' > "$S/vault/03-Resources/Zzz Orphan.md"
printf '# Hub\n\nSee [[Spoke]].\n' > "$S/vault/03-Resources/Hub.md"
printf '# Spoke\n\nBack to [[Hub]].\n' > "$S/vault/03-Resources/Spoke.md"
printf -- '---\nupdated: 2020-01-01\n---\n\n# Old Idea\n\nDormant.\n' > "$S/vault/03-Resources/Old Idea.md"
export AXON_HOME="$S/home"
"$S/axon" reindex --config "$S/home/config.yaml"
```

- [ ] **Step 4: Run the report and check five properties**

```bash
"$S/axon" run orphan-report --config "$S/home/config.yaml"
cat "$S/vault/03-Resources/Vault Health.md"
```

Verify: (1) `Zzz Orphan` appears under Orphans; (2) `Hub` and `Spoke` do **not** — they link to each other; (3) `Old Idea` appears under Dormant; (4) `.axon/review-queue.md` was not created (`ls "$S/vault/.axon/"` — the automation proposes nothing); (5) a second `axon run orphan-report` reports a change-gate skip.

Then prove the report tracks the graph: add a link to the orphan, reindex, re-run, and confirm it leaves the Orphans list.

```bash
printf '# Hub\n\nSee [[Spoke]] and [[Zzz Orphan]].\n' > "$S/vault/03-Resources/Hub.md"
"$S/axon" reindex --config "$S/home/config.yaml"
"$S/axon" run orphan-report --config "$S/home/config.yaml"
grep -c "Zzz Orphan" "$S/vault/03-Resources/Vault Health.md"
```

Expected: `0` in the Orphans section (it now has an inbound link).

- [ ] **Step 5: Check FR-205 and the daemon path**

Revert `Hub.md` so the orphan is orphaned again, reindex, then:

```bash
"$S/axon" run link-suggester --config "$S/home/config.yaml"
grep -n "Zzz Orphan" "$S/vault/.axon/review-queue.md"
```

Expected: the orphan appears among the queued `pair` proposals — the FR-205 fix, live.

Then confirm the daemon schedules the new automation and exits cleanly:

```bash
"$S/axon" automations --config "$S/home/config.yaml" | grep orphan-report
"$S/axon" doctor --config "$S/home/config.yaml" | tail -5
"$S/axon" start --config "$S/home/config.yaml"
```

For `axon start`, confirm the log contains **`daemon running`** — not merely `scheduled …`. A bind failure prints the banner and schedules everything before dying, which reads as success if you only grep for `scheduled`. Confirm the live daemon on 7777 is untouched (`lsof -ti :7777`), then SIGTERM and confirm a clean exit with the pidfile removed.

- [ ] **Step 6: Record the result and clean up**

Delete `$S`. Note the verified properties in the completion report; nothing from the smoke directory is committed.

---

## Self-Review

**Spec coverage:** the orphan query and its two judgment calls → Task 1; the automation, its caps, truncation, change-gate, stub-create, dry-run and the never-propose rule → Task 2; the 24 → 25 count across registry, catalog, both seeds and the invariant test → Task 3; FR-205's ordering plus the degrade-to-lexical fallback → Task 4; FR rows, both roadmaps, CHANGELOG and CLAUDE.md → Task 5; live smoke with the 7777 guard and the `daemon running` check → Task 6. The out-of-scope items (duplicate proposer, `archive` kind, dormant proposals, an `orphan_notes` recipe reader, a dashboard panel) appear in no task, correctly. The spec's "Why no ADR" section is reflected in the Global Constraints rather than a task, which is right — it is a constraint, not work.

**Type consistency:** `OrphanNotes(ctx, q, limit) ([]NoteStamp, error)` defined in Task 1, called in Task 2 (`orphanListMax+1`) and Task 4 (`0`). `NotesUpdatedBefore(ctx, q, beforeDate, limit)` — the four-arg form shipped in the recipe-vocabulary slice — called in Task 2. `OrphanReport` with `Name()`, `Essential()`, `DetectChange`, `Run` and the private `render` defined in Task 2 and registered under that exact name in Task 3. `orphanDormantDays` / `orphanListMax` / `orphanReportNote` / `orphanReportBlock` declared once in Task 2 and referenced by those names in Tasks 2, 3 and 5. `orphansFirst` is private to Task 4 and called from one place.

**Known fragility, flagged rather than hidden:** Task 4's test needs the DB to reflect the vault, and the plan does not assume a reindex helper exists — Step 1 greps for one and supplies a fallback that seeds `notes` and `links` rows directly. If the package turns out to have a real indexing helper, prefer it over the fallback.
