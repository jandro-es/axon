# R1 — Temporal Memory Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Evolve AXON's append-only dated memory log into semantic facts that carry machine-readable validity intervals, with a derived (re-buildable) SQLite fact index, interval-aware supersedence, and SessionStart injection that prefers currently-valid facts.

**Architecture:** The `02-Areas/Profile/MEMORY.md` `axon:memory` managed block stays the single source of truth; the grammar gains a backward-compatible interval extension (open facts date-stamped `valid_from`; superseded facts tombstoned `- ~~…~~ (until DATE; superseded by "…")`). A new `memory_facts` SQLite table is a *derived*, disposable projection rebuilt from the block during `axon reindex` (read-only Markdown→DB, never writes the vault). Consolidation (`memory-distill`) drops from the `synthesis` to the `routine` model tier and promotes interval-bearing facts; the C1 reconcile Accept path now closes the superseded fact's interval in place.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite` (pure-Go SQLite + FTS5, float32 BLOB vectors), existing `internal/identity`, `internal/vault`, `internal/db`, `internal/core`, `internal/automations`, `internal/review` packages. No new third-party dependency.

## Global Constraints

Every task's requirements implicitly include this section. Copied from the spec (`docs/superpowers/specs/2026-07-07-temporal-memory-design.md`) and CLAUDE.md:

- **Cardinal rule 1 — no Claude call bypasses the token manager.** R1 adds no new model call. Consolidation stays the single `memory-distill` call, moved `synthesis → routine` tier. The fact index is built by pure parse/vector code — no Claude path.
- **Cardinal rule 2 — no vault mutation that isn't wikilink-safe.** Every memory write remains a `vault.Patch` into the `axon:memory` block. There is no `vault.delete`; superseded facts are tombstoned in place with an explicit interval, never removed.
- **S9 — the vault rebuilds the DB, never the reverse.** `memory_facts` is derived and disposable. `axon reindex` rebuilds it row-for-row from the block. The reindex fact-rebuild pass **never writes to the vault** — assert the MEMORY.md bytes are unchanged after reindex.
- **S8 — all-off still useful.** With `memory-distill` disabled, facts are still authored (onboarding, agentic remember), still parse with intervals, still filter correctly on injection; only auto-consolidation stops.
- **No new config key.** The tier change rides the existing `models.routine` field (`internal/config/types.go` `ModelsConfig`). Do not add a config field.
- **NFR-05 — data not commands.** Activity + current facts reach the model through `ingestion.NeutralizeDelimiters` (unchanged).
- **Idiomatic Go.** Wrap errors with `%w`; `context.Context` is the first arg on every I/O call; small interfaces defined at the consumer (`db.Execer`, `db.Queryer`, `db.Queryer2`, `db.DBTX`).
- **Tests run with `env -u FORCE_COLOR`.** `db.Open` does NOT auto-migrate — call `db.Migrate` separately. In-memory test DBs use `db.Open(db.MemoryDSN)` then `db.Migrate`.
- **Every commit message ends with:** `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`

---

## File Structure

- `internal/identity/fact.go` — **new.** `Fact` value type + `ParseFact` (the grammar parser). Pure, no I/O.
- `internal/identity/fact_test.go` — **new.** Table-driven `ParseFact` tests.
- `internal/identity/remember.go` — **modify.** Add `ValidFrom` to `Entry`; extend `FormatEntry` to prefer `ValidFrom`; change `tombstone` signature to `(line, date, supersededBy string)`; `Reconcile` closes the interval.
- `internal/identity/remember_format_test.go` — **new.** `FormatEntry`/`tombstone` round-trip tests.
- `internal/identity/render.go` — **modify.** `RecentEntries` filters to open facts (skip struck / closed).
- `internal/db/migrations/0005_memory_facts.sql` — **new.** The derived table.
- `internal/db/memory.go` — **new.** `MemoryFact`, `ReplaceMemoryFacts`, `OpenFacts`, `MemoryFactCounts`, embedding-backfill helpers.
- `internal/db/memory_test.go` — **new.** Repository round-trip tests.
- `internal/core/reindex.go` — **modify.** Add `rebuildMemoryFacts` inside the reindex transaction.
- `internal/core/reindex_memory_test.go` — **new.** Rebuild determinism + vault-bytes-unchanged tests.
- `internal/core/reembed.go` — **modify.** Add `EmbedPendingMemoryFacts` (best-effort, post-txn).
- `internal/core/reembed_memory_test.go` — **new.** Embedding backfill tests.
- `cmd/axon/reindex_cmd.go` — **modify.** Wire `EmbedPendingMemoryFacts` next to `ReembedPending`.
- `internal/automations/memory.go` — **modify.** Flip `synthesis → routine`; promote facts with `[fact]` + `ValidFrom` + `[[source]]`; delegate `memoryEntryText` to `ParseFact`.
- `internal/automations/memory_interval_test.go` — **new.** Tier + interval-promotion tests.
- `internal/core/doctor.go` — **modify.** Add `memoryFactsCheck`.
- `internal/core/doctor_memory_test.go` — **new.** Doctor check tests.
- `internal/review/review.go` — **verify only, no change** (the Accept `reconcile` case already passes `oldText=it.Target, newText=it.Note`).

---

## Requirement → Task map

| FR | Requirement | Task(s) |
|----|-------------|---------|
| FR-134 | Interval-bearing fact grammar (open + closed forms, backward compatible) | 1, 2 |
| FR-135 | Derived `memory_facts` index rebuilt read-only during reindex; doctor reports counts | 5, 6, 8 |
| FR-136 | Interval-aware supersedence via `Reconcile`; `memory-distill` at routine tier | 3, 7 |
| FR-137 | SessionStart injection prefers currently-valid facts, no DB dependency | 4 |

---

### Task 1: Fact grammar — `ParseFact` + `Fact` type

**Files:**
- Create: `internal/identity/fact.go`
- Test: `internal/identity/fact_test.go`

**Interfaces:**
- Consumes: nothing (pure, leaf).
- Produces:
  ```go
  type Fact struct {
      Text         string // the bare fact text
      Kind         string // fact|decision|lesson|preference|"" (untyped)
      Source       string // raw source as written: a [[wikilink]] or a plain token
      ValidFrom    string // YYYY-MM-DD (the leading date), "" if none
      ValidUntil   string // YYYY-MM-DD or "" (open)
      SupersededBy string // new-fact text (quotes sanitized) or "" (unknown/none)
      Struck       bool
  }
  func ParseFact(line string) (Fact, bool)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/identity/fact_test.go`:

```go
package identity

import "testing"

func TestParseFact(t *testing.T) {
	tests := []struct {
		name string
		line string
		ok   bool
		want Fact
	}{
		{
			name: "open fact with kind and wikilink source",
			line: "- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])",
			ok:   true,
			want: Fact{Text: "Lives in Tokyo", Kind: "fact", Source: "[[2026-07-05]]", ValidFrom: "2026-07-05"},
		},
		{
			name: "open fact with token source, no kind",
			line: "- 2026-06-01 — Prefers Go for daemons (source: session)",
			ok:   true,
			want: Fact{Text: "Prefers Go for daemons", Source: "session", ValidFrom: "2026-06-01"},
		},
		{
			name: "untyped legacy line (no kind, no source)",
			line: "- 2026-06-01 — An unrelated fact",
			ok:   true,
			want: Fact{Text: "An unrelated fact", ValidFrom: "2026-06-01"},
		},
		{
			name: "closed fact — new interval form",
			line: `- ~~2026-07-05 — Lives in Tokyo~~ (until 2026-08-01; superseded by "Lives in Osaka")`,
			ok:   true,
			want: Fact{Text: "Lives in Tokyo", ValidFrom: "2026-07-05", ValidUntil: "2026-08-01", SupersededBy: "Lives in Osaka", Struck: true},
		},
		{
			name: "closed fact — legacy tombstone form, source inside strike",
			line: "- ~~2026-06-01 — Prefers Go for daemons (source: session)~~ (superseded 2026-07-05)",
			ok:   true,
			want: Fact{Text: "Prefers Go for daemons", Source: "session", ValidFrom: "2026-06-01", ValidUntil: "2026-07-05", Struck: true},
		},
		{
			name: "decision kind",
			line: "- 2026-07-01 — Adopt ADR-028 [decision] (source: reconcile)",
			ok:   true,
			want: Fact{Text: "Adopt ADR-028", Kind: "decision", Source: "reconcile", ValidFrom: "2026-07-01"},
		},
		{
			name: "embedded quotes in superseded-by are preserved verbatim",
			line: `- ~~2026-07-05 — Old~~ (until 2026-08-01; superseded by "New 'quoted' text")`,
			ok:   true,
			want: Fact{Text: "Old", ValidFrom: "2026-07-05", ValidUntil: "2026-08-01", SupersededBy: "New 'quoted' text", Struck: true},
		},
		{
			name: "non-entry line",
			line: "## Memory",
			ok:   false,
		},
		{
			name: "blank line",
			line: "   ",
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseFact(tt.line)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Fatalf("ParseFact(%q)\n got  %+v\n want %+v", tt.line, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/identity/ -run TestParseFact`
Expected: FAIL — `undefined: ParseFact` / `undefined: Fact`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/identity/fact.go`:

```go
package identity

import (
	"regexp"
	"strings"
)

// Fact is the parsed view of one axon:memory line (ADR-028). Struck marks a
// tombstoned (superseded) fact; ValidUntil/SupersededBy are set only when Struck.
// Source is stored raw as written (a [[wikilink]] or a plain token) so
// ParseFact(FormatEntry(e)) round-trips.
type Fact struct {
	Text         string
	Kind         string // fact|decision|lesson|preference|"" (untyped)
	Source       string
	ValidFrom    string // YYYY-MM-DD (the leading date), "" if none
	ValidUntil   string // YYYY-MM-DD or "" (open)
	SupersededBy string // new-fact text (quotes sanitized) or "" (unknown/none)
	Struck       bool
}

var (
	factUntilRe      = regexp.MustCompile(`^\(until (\d{4}-\d{2}-\d{2}); superseded by "(.*)"\)$`)
	factSupersededRe = regexp.MustCompile(`^\(superseded (\d{4}-\d{2}-\d{2})\)$`)
	factDateRe       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ParseFact parses one "- …" memory line into a Fact. Legacy lines (no [fact]
// kind, bare "(superseded DATE)" tombstones) parse correctly. Returns ok=false
// for a non-entry line (blank, or without the "- " bullet prefix).
func ParseFact(line string) (Fact, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "- ") {
		return Fact{}, false
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "- "))
	if s == "" {
		return Fact{}, false
	}
	var f Fact
	if strings.HasPrefix(s, "~~") {
		rest := s[len("~~"):]
		j := strings.Index(rest, "~~")
		if j < 0 {
			return Fact{}, false
		}
		inner := rest[:j]
		annotation := strings.TrimSpace(rest[j+len("~~"):])
		f.Struck = true
		if m := factUntilRe.FindStringSubmatch(annotation); m != nil {
			f.ValidUntil, f.SupersededBy = m[1], m[2]
		} else if m := factSupersededRe.FindStringSubmatch(annotation); m != nil {
			f.ValidUntil = m[1]
		}
		parseFactBody(inner, &f)
		return f, true
	}
	parseFactBody(s, &f)
	return f, true
}

// parseFactBody fills Text/Kind/Source/ValidFrom from an open-fact body of the
// form "DATE — text [kind] (source: SRC)". Every trailing element is optional.
func parseFactBody(body string, f *Fact) {
	body = strings.TrimSpace(body)
	if i := strings.Index(body, " — "); i >= 0 {
		if cand := strings.TrimSpace(body[:i]); factDateRe.MatchString(cand) {
			f.ValidFrom = cand
			body = strings.TrimSpace(body[i+len(" — "):])
		}
	}
	if i := strings.LastIndex(body, " (source:"); i >= 0 {
		if end := strings.LastIndex(body, ")"); end > i {
			f.Source = strings.TrimSpace(body[i+len(" (source:") : end])
			body = strings.TrimSpace(body[:i])
		}
	}
	if strings.HasSuffix(body, "]") {
		if i := strings.LastIndex(body, " ["); i >= 0 {
			f.Kind = body[i+len(" [") : len(body)-1]
			body = strings.TrimSpace(body[:i])
		}
	}
	f.Text = strings.TrimSpace(body)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/identity/ -run TestParseFact`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/identity/fact.go internal/identity/fact_test.go
git commit -m "feat(identity): ParseFact grammar for interval-bearing memory facts (FR-134)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `FormatEntry` + `tombstone` interval extension

**Files:**
- Modify: `internal/identity/remember.go:12-18` (`Entry`), `:93-96` (`tombstone`), `:98-112` (`FormatEntry`)
- Test: `internal/identity/remember_format_test.go`

**Interfaces:**
- Consumes: `Fact`, `ParseFact` (Task 1).
- Produces:
  ```go
  type Entry struct { Text, Kind, Source, Date, ValidFrom string } // ValidFrom added
  func FormatEntry(e Entry) string
  func tombstone(line, date, supersededBy string) string // signature changed
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/identity/remember_format_test.go`:

```go
package identity

import (
	"strings"
	"testing"
)

func TestFormatEntryRoundTrip(t *testing.T) {
	e := Entry{Text: "Lives in Tokyo", Kind: "fact", Source: "[[2026-07-05]]", ValidFrom: "2026-07-05"}
	line := FormatEntry(e)
	if line != "- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])" {
		t.Fatalf("FormatEntry = %q", line)
	}
	f, ok := ParseFact(line)
	if !ok || f.Text != e.Text || f.Kind != e.Kind || f.Source != e.Source || f.ValidFrom != e.ValidFrom {
		t.Fatalf("round-trip lost fields: %+v", f)
	}
}

func TestFormatEntryValidFromPreferredOverDate(t *testing.T) {
	// ValidFrom wins as the leading date when both are set.
	line := FormatEntry(Entry{Text: "x", Date: "2026-01-01", ValidFrom: "2026-07-05"})
	if !strings.HasPrefix(line, "- 2026-07-05 — ") {
		t.Fatalf("ValidFrom should be the leading date: %q", line)
	}
}

func TestFormatEntryFallsBackToDate(t *testing.T) {
	// Existing callers set only Date; output must be unchanged.
	line := FormatEntry(Entry{Text: "x", Date: "2026-01-01"})
	if line != "- 2026-01-01 — x" {
		t.Fatalf("Date fallback broken: %q", line)
	}
}

func TestTombstoneIntervalForm(t *testing.T) {
	line := "- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])"
	got := tombstone(line, "2026-08-01", `Lives in "Osaka"`)
	want := `- ~~2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])~~ (until 2026-08-01; superseded by "Lives in 'Osaka'")`
	if got != want {
		t.Fatalf("tombstone interval form:\n got  %q\n want %q", got, want)
	}
	f, ok := ParseFact(got)
	if !ok || !f.Struck || f.ValidUntil != "2026-08-01" || f.SupersededBy != "Lives in 'Osaka'" {
		t.Fatalf("tombstone did not round-trip: %+v", f)
	}
}

func TestTombstoneLegacyFallback(t *testing.T) {
	// Empty superseded-by keeps the legacy form so hand-authored tombstones and
	// existing tests stay valid.
	got := tombstone("- 2026-06-01 — Prefers Go for daemons (source: session)", "2026-07-05", "")
	want := "- ~~2026-06-01 — Prefers Go for daemons (source: session)~~ (superseded 2026-07-05)"
	if got != want {
		t.Fatalf("legacy fallback:\n got  %q\n want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/identity/ -run 'TestFormatEntry|TestTombstone'`
Expected: FAIL — `tombstone` currently takes 2 args (compile error `too many arguments`), `Entry` has no `ValidFrom`.

- [ ] **Step 3: Write minimal implementation**

In `internal/identity/remember.go`, add `ValidFrom` to `Entry`:

```go
// Entry is a durable memory record to append.
type Entry struct {
	Text   string // the fact/decision/lesson (single line)
	Kind   string // optional: fact | decision | lesson | preference
	Source string // optional provenance, e.g. "session", "reconcile", or a [[wikilink]]
	Date   string // YYYY-MM-DD; defaults to today (UTC) if empty
	// ValidFrom, when set, is the fact's leading date (valid_from). It takes
	// precedence over Date for the emitted line; existing callers that set only
	// Date are unaffected.
	ValidFrom string
}
```

Replace `FormatEntry`:

```go
// FormatEntry renders a single MEMORY bullet: "- DATE — text [kind] (source: …)".
// DATE is ValidFrom when set, else Date — the leading date is the fact's valid_from.
func FormatEntry(e Entry) string {
	// Collapse internal newlines so one entry stays one line (the block is parsed
	// line-by-line and injected verbatim).
	text := strings.Join(strings.Fields(e.Text), " ")
	date := e.ValidFrom
	if date == "" {
		date = e.Date
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- %s — %s", date, text)
	if k := strings.TrimSpace(e.Kind); k != "" {
		fmt.Fprintf(&b, " [%s]", k)
	}
	if s := strings.TrimSpace(e.Source); s != "" {
		fmt.Fprintf(&b, " (source: %s)", s)
	}
	return b.String()
}
```

Replace `tombstone`:

```go
// tombstone strikes a memory entry line and closes its validity interval,
// preserving the dated fact for audit while marking it inactive. When
// supersededBy is non-empty it emits the interval-explicit form
// "- ~~<inner>~~ (until DATE; superseded by \"<supersededBy>\")" with quotes
// sanitized to ' so the annotation cannot break parsing; when it is empty it
// falls back to the legacy "(superseded DATE)" form.
func tombstone(line, date, supersededBy string) string {
	inner := strings.TrimPrefix(strings.TrimSpace(line), "- ")
	if supersededBy == "" {
		return fmt.Sprintf("- ~~%s~~ (superseded %s)", inner, date)
	}
	sb := strings.ReplaceAll(supersededBy, `"`, "'")
	return fmt.Sprintf("- ~~%s~~ (until %s; superseded by \"%s\")", inner, date, sb)
}
```

Update the sole existing caller in `Reconcile` (line 79) so it compiles — pass `""` for now (Task 3 gives it the real value):

```go
			entries[i] = tombstone(line, date, "")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/identity/`
Expected: PASS (new tests green; the existing `TestReconcileTombstonesOldAndPrependsNew` still passes because `Reconcile` still emits the legacy `(superseded …)` form until Task 3).

- [ ] **Step 5: Commit**

```bash
git add internal/identity/remember.go internal/identity/remember_format_test.go
git commit -m "feat(identity): Entry.ValidFrom + interval tombstone form (FR-134)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `Reconcile` closes the interval

**Files:**
- Modify: `internal/identity/remember.go:59-89` (`Reconcile` body only; signature unchanged)
- Test: `internal/identity/remember_reconcile_test.go` (add cases; keep existing)

**Interfaces:**
- Consumes: `tombstone(line, date, supersededBy)` (Task 2), `FormatEntry` (Task 2), `ParseFact` (Task 1).
- Produces: `func Reconcile(ctx context.Context, v *vault.FS, oldText, newText, date string) (bool, error)` — **signature unchanged.**

- [ ] **Step 1: Write the failing test**

Append to `internal/identity/remember_reconcile_test.go`:

```go
func TestReconcileClosesIntervalWithSupersededBy(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "Lives in Tokyo", Kind: "fact", Source: "session", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	matched, err := Reconcile(ctx, v, "Lives in Tokyo", "Lives in Osaka", "2026-08-01")
	if err != nil || !matched {
		t.Fatalf("Reconcile matched=%v err=%v", matched, err)
	}
	body, _ := readBody(ctx, v, MemoryPath)
	block := extractBlock(body, MemoryBlock)

	// The old fact is tombstoned with the interval + superseded-by pointer.
	if !strings.Contains(block, `(until 2026-08-01; superseded by "Lives in Osaka")`) {
		t.Fatalf("interval not closed:\n%s", block)
	}
	// Parse the closed line and assert the interval fields.
	var closed, open bool
	for _, line := range parseEntries(block) {
		f, _ := ParseFact(line)
		if f.Struck && f.ValidUntil == "2026-08-01" && f.SupersededBy == "Lives in Osaka" {
			closed = true
		}
		if !f.Struck && f.Text == "Lives in Osaka" && f.ValidFrom == "2026-08-01" && f.Source == "reconcile" {
			open = true
		}
	}
	if !closed || !open {
		t.Fatalf("closed=%v open=%v\n%s", closed, open, block)
	}
	// New fact is prepended above the tombstone (newest-first).
	if strings.Index(block, "Lives in Osaka (source: reconcile)") > strings.Index(block, "~~2026-07-05") {
		t.Fatal("new open fact should be prepended above the superseded one")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/identity/ -run TestReconcileClosesInterval`
Expected: FAIL — block still contains legacy `(superseded 2026-08-01)`, not the interval form.

- [ ] **Step 3: Write minimal implementation**

In `internal/identity/remember.go`, update the `Reconcile` loop to pass `newText` as the superseded-by pointer, and prepend the new fact as an explicit open fact (source `reconcile`, `ValidFrom = date`):

```go
	entries := parseEntries(extractBlock(body, MemoryBlock))
	matched := false
	for i, line := range entries {
		if !matched && strings.Contains(line, oldText) && !strings.Contains(line, "~~") {
			entries[i] = tombstone(line, date, newText)
			matched = true
		}
	}
	newEntry := FormatEntry(Entry{Text: newText, Source: "reconcile", ValidFrom: date})
	all := append([]string{newEntry}, entries...) // newest first
```

(Leave the rest of `Reconcile` — the `Patch` call and `return matched, nil` — unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/identity/`
Expected: PASS for the new case. NOTE: the pre-existing `TestReconcileTombstonesOldAndPrependsNew` asserts `(superseded 2026-07-05)` — update its assertion to the interval form in the same step:

```go
	if !strings.Contains(block, "~~2026-06-01 — Prefers Go for daemons") ||
		!strings.Contains(block, `(until 2026-07-05; superseded by "Uses Rust for daemons")`) {
		t.Fatalf("old entry not tombstoned with interval:\n%s", block)
	}
```

Re-run: `env -u FORCE_COLOR go test ./internal/identity/` → PASS.

- [ ] **Step 5: Verify the review Accept path still compiles unchanged**

Run: `env -u FORCE_COLOR go build ./internal/review/`
Expected: PASS. `review.Accept` calls `identity.Reconcile(ctx, v, it.Target, it.Note, ...)` (oldText=`it.Target`, newText=`it.Note`) — signature and arg order are unchanged, so no edit is required. Do not modify `internal/review/review.go`.

- [ ] **Step 6: Commit**

```bash
git add internal/identity/remember.go internal/identity/remember_reconcile_test.go
git commit -m "feat(identity): Reconcile closes the superseded fact's interval (FR-136)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Injection prefers open facts

**Files:**
- Modify: `internal/identity/render.go:83-98` (`RecentEntries`)
- Test: `internal/identity/render_open_test.go`

**Interfaces:**
- Consumes: `ParseFact` (Task 1), `parseEntries`, `extractBlock`, `readBody` (existing, `render.go`).
- Produces: `func RecentEntries(ctx context.Context, v *vault.FS, n int) ([]string, error)` — now returns only **open** facts (not struck, no `valid_until`), newest-first, capped at n. Signature unchanged.

- [ ] **Step 1: Write the failing test**

Create `internal/identity/render_open_test.go`:

```go
package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestRecentEntriesExcludesSupersededFacts(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "Lives in Tokyo", Kind: "fact", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(ctx, v, "Lives in Tokyo", "Lives in Osaka", "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	got, err := RecentEntries(ctx, v, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Only the current open fact is injected; the closed one is excluded.
	if len(got) != 1 || !strings.Contains(got[0], "Lives in Osaka") {
		t.Fatalf("RecentEntries = %v, want only the open Osaka fact", got)
	}
	for _, line := range got {
		if strings.Contains(line, "~~") || strings.Contains(line, "until ") {
			t.Fatalf("a closed fact leaked into injection: %q", line)
		}
	}
}

func TestRecentEntriesIncludesLegacyUntypedLines(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "An unrelated fact", Date: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	got, _ := RecentEntries(ctx, v, 10)
	if len(got) != 1 || !strings.Contains(got[0], "An unrelated fact") {
		t.Fatalf("legacy untyped line should be included: %v", got)
	}
}

func TestRecentEntriesEmptyBlockUnchanged(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	got, err := RecentEntries(ctx, v, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty block should yield no entries: %v, %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/identity/ -run TestRecentEntries`
Expected: FAIL on `TestRecentEntriesExcludesSupersededFacts` — the current `RecentEntries` returns both the open and the struck line (`len(got) == 2`).

- [ ] **Step 3: Write minimal implementation**

Replace `RecentEntries` in `internal/identity/render.go`:

```go
// RecentEntries returns up to n newest currently-valid entries (newest first)
// from the axon:memory managed block in MEMORY.md. Superseded/closed facts —
// struck lines and any with a valid_until — are excluded so SessionStart
// injection prefers currently-valid facts (FR-137). Legacy untyped lines (no
// kind, no interval) are open and included. Pure block parse, no DB dependency.
func RecentEntries(ctx context.Context, v *vault.FS, n int) ([]string, error) {
	if n <= 0 {
		n = 10
	}
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return nil, err
	}
	all := parseEntries(extractBlock(body, MemoryBlock))
	open := make([]string, 0, len(all))
	for _, line := range all {
		f, ok := ParseFact(line)
		if !ok || f.Struck || f.ValidUntil != "" {
			continue
		}
		open = append(open, line)
	}
	if len(open) > n {
		open = open[:n]
	}
	return open, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/identity/ ./internal/automations/`
Expected: PASS. (The `automations` package also calls `RecentEntries`; running it confirms consolidation still sees the facts it needs — its tests seed only open facts.)

- [ ] **Step 5: Commit**

```bash
git add internal/identity/render.go internal/identity/render_open_test.go
git commit -m "feat(identity): SessionStart injection prefers currently-valid facts (FR-137)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: DB migration + `memory_facts` repository

**Files:**
- Create: `internal/db/migrations/0005_memory_facts.sql`
- Create: `internal/db/memory.go`
- Test: `internal/db/memory_test.go`

**Interfaces:**
- Consumes: `db.Execer`, `db.Queryer`, `db.Queryer2` (existing, `notes.go`); `EncodeVector`/`DecodeVector` (existing, `vector.go`).
- Produces:
  ```go
  type MemoryFact struct {
      ID           int64
      Text         string
      Kind         string
      Source       string
      ValidFrom    string
      ValidUntil   string
      SupersededBy string
      Struck       bool
      Embedding    []float32
      LineNo       int
      Updated      string
  }
  func ReplaceMemoryFacts(ctx context.Context, q Execer, facts []MemoryFact) error
  func OpenFacts(ctx context.Context, q Queryer2) ([]MemoryFact, error)
  func MemoryFactCounts(ctx context.Context, q Queryer) (total, open, superseded int, err error)
  ```

- [ ] **Step 1: Create the migration**

Create `internal/db/migrations/0005_memory_facts.sql`:

```sql
-- 0005_memory_facts — R1 temporal memory (ADR-028). A DERIVED, disposable
-- projection of the axon:memory block: reindex delete-all+inserts these rows
-- from Markdown (the vault is the source of truth, ADR-011). Never authoritative.

CREATE TABLE memory_facts (
  id            INTEGER PRIMARY KEY,
  text          TEXT NOT NULL,
  kind          TEXT,
  source        TEXT,
  valid_from    TEXT NOT NULL,
  valid_until   TEXT,
  superseded_by TEXT,
  struck        INTEGER NOT NULL DEFAULT 0,
  embedding     BLOB,
  line_no       INTEGER,
  updated       TEXT NOT NULL
);

CREATE INDEX idx_memory_facts_open ON memory_facts(valid_until) WHERE valid_until IS NULL;
```

- [ ] **Step 2: Write the failing test**

Create `internal/db/memory_test.go`:

```go
package db

import (
	"context"
	"testing"
)

func TestReplaceMemoryFactsRoundTrip(t *testing.T) {
	ctx := context.Background()
	d, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := Migrate(d); err != nil {
		t.Fatal(err)
	}

	facts := []MemoryFact{
		{Text: "Lives in Osaka", Kind: "fact", Source: "[[2026-08-01]]", ValidFrom: "2026-08-01", LineNo: 0, Updated: "2026-08-01"},
		{Text: "Lives in Tokyo", Kind: "fact", Source: "session", ValidFrom: "2026-07-05", ValidUntil: "2026-08-01", SupersededBy: "Lives in Osaka", Struck: true, LineNo: 1, Updated: "2026-08-01"},
	}
	if err := ReplaceMemoryFacts(ctx, d, facts); err != nil {
		t.Fatal(err)
	}

	total, open, superseded, err := MemoryFactCounts(ctx, d)
	if err != nil || total != 2 || open != 1 || superseded != 1 {
		t.Fatalf("counts = (%d,%d,%d) err=%v, want (2,1,1)", total, open, superseded, err)
	}

	got, err := OpenFacts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "Lives in Osaka" || got[0].ValidUntil != "" || got[0].Struck {
		t.Fatalf("OpenFacts = %+v, want only the open Osaka fact", got)
	}
	if got[0].Kind != "fact" || got[0].Source != "[[2026-08-01]]" || got[0].ValidFrom != "2026-08-01" {
		t.Fatalf("open fact fields lost: %+v", got[0])
	}
}

func TestReplaceMemoryFactsIsDeleteAllThenInsert(t *testing.T) {
	ctx := context.Background()
	d, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	first := []MemoryFact{{Text: "old", ValidFrom: "2026-01-01", LineNo: 0, Updated: "2026-01-01"}}
	if err := ReplaceMemoryFacts(ctx, d, first); err != nil {
		t.Fatal(err)
	}
	// A second replace fully supersedes the first — no accumulation, exact set.
	second := []MemoryFact{{Text: "new", ValidFrom: "2026-02-02", LineNo: 0, Updated: "2026-02-02"}}
	if err := ReplaceMemoryFacts(ctx, d, second); err != nil {
		t.Fatal(err)
	}
	total, _, _, _ := MemoryFactCounts(ctx, d)
	if total != 1 {
		t.Fatalf("total after re-replace = %d, want 1 (delete-all+insert)", total)
	}
	open, _ := OpenFacts(ctx, d)
	if len(open) != 1 || open[0].Text != "new" {
		t.Fatalf("re-replace did not reproduce exact set: %+v", open)
	}
}
```

The tests open the DB with the inline `Open(MemoryDSN)` + `Migrate` form used by the existing `internal/db/*_test.go` files (this package has no shared migrated-DB helper).

- [ ] **Step 3: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestReplaceMemoryFacts`
Expected: FAIL — `undefined: MemoryFact`, `undefined: ReplaceMemoryFacts`, etc.

- [ ] **Step 4: Write minimal implementation**

Create `internal/db/memory.go`:

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
)

// MemoryFact is one row of the derived memory_facts index — a re-derivable
// projection of a single axon:memory block line (ADR-028). The vault is the
// source of truth (ADR-011); reindex delete-all+inserts these rows from the
// block, so they are disposable.
type MemoryFact struct {
	ID           int64
	Text         string
	Kind         string
	Source       string
	ValidFrom    string
	ValidUntil   string
	SupersededBy string
	Struck       bool
	Embedding    []float32
	LineNo       int
	Updated      string
}

// ReplaceMemoryFacts rebuilds the whole memory_facts table in one pass: it
// deletes every row then inserts facts in the given order (callers pass them
// ordered by block position via LineNo). The block is small, so a full replace
// keeps the projection exactly in step with the Markdown and makes reindex
// row-for-row deterministic (S9). An embedding is written when non-nil, else the
// column is left NULL.
func ReplaceMemoryFacts(ctx context.Context, q Execer, facts []MemoryFact) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM memory_facts;"); err != nil {
		return fmt.Errorf("clear memory_facts: %w", err)
	}
	for _, f := range facts {
		var emb []byte
		if len(f.Embedding) > 0 {
			emb = EncodeVector(f.Embedding)
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO memory_facts
			   (text, kind, source, valid_from, valid_until, superseded_by, struck, embedding, line_no, updated)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			f.Text, nullify(f.Kind), nullify(f.Source), f.ValidFrom,
			nullify(f.ValidUntil), nullify(f.SupersededBy), boolInt(f.Struck),
			emb, f.LineNo, f.Updated); err != nil {
			return fmt.Errorf("insert memory fact %q: %w", f.Text, err)
		}
	}
	return nil
}

// OpenFacts returns the currently-valid facts (not struck, no valid_until) in
// block order (line_no). Powers R2/R8 retrieval; SessionStart injection parses
// the block directly and does not use this.
func OpenFacts(ctx context.Context, q Queryer2) ([]MemoryFact, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, text, COALESCE(kind,''), COALESCE(source,''), valid_from,
		        COALESCE(valid_until,''), COALESCE(superseded_by,''), struck,
		        embedding, COALESCE(line_no,0), updated
		   FROM memory_facts
		  WHERE valid_until IS NULL AND struck = 0
		  ORDER BY line_no;`)
	if err != nil {
		return nil, fmt.Errorf("open facts: %w", err)
	}
	defer rows.Close()
	return scanMemoryFacts(rows)
}

// MemoryFactCounts reports total, open and superseded fact counts for doctor.
func MemoryFactCounts(ctx context.Context, q Queryer) (total, open, superseded int, err error) {
	var t, o, s sql.NullInt64
	err = q.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        SUM(CASE WHEN valid_until IS NULL AND struck = 0 THEN 1 ELSE 0 END),
		        SUM(CASE WHEN valid_until IS NOT NULL OR struck = 1 THEN 1 ELSE 0 END)
		   FROM memory_facts;`).Scan(&t, &o, &s)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("memory fact counts: %w", err)
	}
	return int(t.Int64), int(o.Int64), int(s.Int64), nil
}

func scanMemoryFacts(rows *sql.Rows) ([]MemoryFact, error) {
	var out []MemoryFact
	for rows.Next() {
		var f MemoryFact
		var struck int
		var emb []byte
		if err := rows.Scan(&f.ID, &f.Text, &f.Kind, &f.Source, &f.ValidFrom,
			&f.ValidUntil, &f.SupersededBy, &struck, &emb, &f.LineNo, &f.Updated); err != nil {
			return nil, err
		}
		f.Struck = struck != 0
		if len(emb) > 0 {
			v, err := DecodeVector(emb)
			if err != nil {
				return nil, err
			}
			f.Embedding = v
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// nullify maps "" to a NULL string arg so optional columns store NULL, not "".
func nullify(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/db/`
Expected: PASS. (Also confirms `TestOpenAndMigrateInMemory`/upgrade tests accept the new `0005` migration.)

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0005_memory_facts.sql internal/db/memory.go internal/db/memory_test.go
git commit -m "feat(db): derived memory_facts index + repository (FR-135)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: `rebuildMemoryFacts` in reindex + best-effort embedding backfill

**Files:**
- Modify: `internal/core/reindex.go:37-160` (add `rebuildMemoryFacts` call before `tx.Commit()`, add the function)
- Modify: `internal/core/reembed.go` (add `EmbedPendingMemoryFacts`)
- Modify: `internal/db/memory.go` (add `MemoryFactsMissingEmbedding`, `SetMemoryFactEmbedding`)
- Modify: `cmd/axon/reindex_cmd.go:62-99` (wire `EmbedPendingMemoryFacts` after `ReembedPending`)
- Test: `internal/core/reindex_memory_test.go`, `internal/core/reembed_memory_test.go`

**Interfaces:**
- Consumes: `identity.RecentEntries`? No — reindex needs **all** block lines (including struck) to project every fact. Use a raw read: `identity.ParseFact` (Task 1) over the block lines. Because `RecentEntries` now filters to open facts (Task 4), reindex reads the block via `identity.BlockLines` (added below).
- Produces:
  ```go
  // internal/identity/render.go
  func BlockLines(ctx context.Context, v *vault.FS) ([]string, error) // ALL "- " lines, block order

  // internal/db/memory.go
  func MemoryFactsMissingEmbedding(ctx context.Context, q Queryer2) ([]MemoryFact, error)
  func SetMemoryFactEmbedding(ctx context.Context, q Execer, id int64, vec []float32) error

  // internal/core/reembed.go
  func EmbedPendingMemoryFacts(ctx context.Context, sqlDB *sql.DB, embedder embeddings.Provider) (int, error)
  ```

- [ ] **Step 1: Add `identity.BlockLines` (raw, all lines)**

`RecentEntries` now filters to open facts, but the index must project **every** fact (open and superseded). Add a raw accessor to `internal/identity/render.go`:

```go
// BlockLines returns every "- " entry line of the axon:memory block in block
// order (newest-first), unfiltered — struck and open alike. reindex projects
// all of them into memory_facts; RecentEntries (open-only) is for injection.
func BlockLines(ctx context.Context, v *vault.FS) ([]string, error) {
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return nil, err
	}
	return parseEntries(extractBlock(body, MemoryBlock)), nil
}
```

- [ ] **Step 2: Write the failing reindex test**

Create `internal/core/reindex_memory_test.go`:

```go
package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
)

const memoryFixture = `---
title: "Durable memory"
type: memory
---

## Memory

<!-- axon:memory:start -->
- 2026-08-01 — Lives in Osaka [fact] (source: [[2026-08-01]])
- ~~2026-07-05 — Lives in Tokyo [fact] (source: session)~~ (until 2026-08-01; superseded by "Lives in Osaka")
- not a real entry, hand-edited garbage that will not ParseFact cleanly ]]]
<!-- axon:memory:end -->
`

func TestReindexRebuildsMemoryFacts(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	d := migratedDB(t)

	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}

	total, open, superseded, err := db.MemoryFactCounts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	// Two parseable facts (one open, one closed). The "- not a real entry…" line
	// DOES parse as an untyped open fact (leading text, no date) — but has no
	// interval and is not struck, so it counts as open. To make the test precise
	// we assert only the well-formed rows below.
	if superseded != 1 {
		t.Fatalf("superseded = %d, want 1", superseded)
	}
	if total < 2 || open < 1 {
		t.Fatalf("counts total=%d open=%d, want >=2 total and >=1 open", total, open)
	}

	facts, err := db.OpenFacts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	var sawOsaka bool
	for _, f := range facts {
		if f.Text == "Lives in Osaka" && f.ValidFrom == "2026-08-01" && f.Kind == "fact" {
			sawOsaka = true
		}
	}
	if !sawOsaka {
		t.Fatalf("open Osaka fact missing: %+v", facts)
	}
}

func TestReindexNeverWritesTheVault(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	abs := filepath.Join(v.Root(), filepath.FromSlash(identity.MemoryPath))
	before, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	d := migratedDB(t)
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("reindex mutated MEMORY.md (S9 violation):\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestReindexMemoryFactsAreDeterministic(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	d1 := migratedDB(t)
	if _, err := Reindex(ctx, v, d1); err != nil {
		t.Fatal(err)
	}
	first, _ := db.OpenFacts(ctx, d1)

	// Delete-DB (fresh) → reindex reproduces identical open rows (S9).
	d2 := migratedDB(t)
	if _, err := Reindex(ctx, v, d2); err != nil {
		t.Fatal(err)
	}
	second, _ := db.OpenFacts(ctx, d2)

	if len(first) != len(second) {
		t.Fatalf("row count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Text != second[i].Text || first[i].ValidFrom != second[i].ValidFrom ||
			first[i].LineNo != second[i].LineNo || first[i].ValidUntil != second[i].ValidUntil {
			t.Fatalf("row %d differs: %+v vs %+v", i, first[i], second[i])
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestReindex.*Memory`
Expected: FAIL — `db.MemoryFactCounts` returns 0 rows (reindex does not yet build the fact index); `identity.BlockLines` undefined until Step 1 is saved.

- [ ] **Step 4: Wire `rebuildMemoryFacts` into `Reindex`**

In `internal/core/reindex.go`, add the `identity` import and call `rebuildMemoryFacts` inside the transaction, immediately before `if err := tx.Commit()`:

```go
	// Rebuild the derived memory fact index from the axon:memory block (ADR-028).
	// Read-only Markdown→DB: this NEVER writes to the vault (S9).
	if err := rebuildMemoryFacts(ctx, v, tx); err != nil {
		return res, err
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit reindex: %w", err)
	}
```

Add the function at the end of `reindex.go`:

```go
// rebuildMemoryFacts projects every axon:memory block line into the derived
// memory_facts table inside the reindex transaction. It reads MEMORY.md and
// ParseFacts each bullet; unparseable lines are skipped (they are surfaced by
// doctor, never indexed). Embeddings are left NULL here and backfilled
// best-effort after the transaction (EmbedPendingMemoryFacts). Read-only w.r.t.
// the vault.
func rebuildMemoryFacts(ctx context.Context, v *vault.FS, tx db.DBTX) error {
	lines, err := identity.BlockLines(ctx, v)
	if err != nil {
		return err
	}
	facts := make([]db.MemoryFact, 0, len(lines))
	now := nowStamp()
	for i, line := range lines {
		f, ok := identity.ParseFact(line)
		if !ok {
			continue
		}
		facts = append(facts, db.MemoryFact{
			Text: f.Text, Kind: f.Kind, Source: f.Source,
			ValidFrom: f.ValidFrom, ValidUntil: f.ValidUntil,
			SupersededBy: f.SupersededBy, Struck: f.Struck,
			LineNo: i, Updated: now,
		})
	}
	return db.ReplaceMemoryFacts(ctx, tx, facts)
}
```

Add `"github.com/jandro-es/axon/internal/identity"` to the `reindex.go` import block.

- [ ] **Step 5: Run the reindex tests**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestReindex.*Memory`
Expected: PASS.

- [ ] **Step 6: Write the failing embedding-backfill test**

Create `internal/core/reembed_memory_test.go`:

```go
package core

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/identity"
)

func TestEmbedPendingMemoryFacts(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	d := migratedDB(t)
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}

	// Nil embedder is a no-op (Ollama down → embeddings stay NULL, best-effort).
	if n, err := EmbedPendingMemoryFacts(ctx, d, nil); err != nil || n != 0 {
		t.Fatalf("nil embedder = (%d, %v), want (0, nil)", n, err)
	}

	// A fake embedder fills every pending fact.
	n, err := EmbedPendingMemoryFacts(ctx, d, embeddings.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected at least one fact embedded")
	}
	// Re-running finds nothing pending.
	if again, err := EmbedPendingMemoryFacts(ctx, d, embeddings.NewFake()); err != nil || again != 0 {
		t.Fatalf("second pass = (%d, %v), want (0, nil)", again, err)
	}

	facts, _ := db.OpenFacts(ctx, d)
	for _, f := range facts {
		if len(f.Embedding) == 0 {
			t.Fatalf("fact %q left without embedding", f.Text)
		}
	}
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestEmbedPendingMemoryFacts`
Expected: FAIL — `undefined: EmbedPendingMemoryFacts`, `undefined: db.MemoryFactsMissingEmbedding`.

- [ ] **Step 8: Implement the DB helpers**

Append to `internal/db/memory.go`:

```go
// MemoryFactsMissingEmbedding returns facts with no stored embedding (id+text
// only), for the best-effort backfill pass after reindex.
func MemoryFactsMissingEmbedding(ctx context.Context, q Queryer2) ([]MemoryFact, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, text FROM memory_facts WHERE embedding IS NULL ORDER BY id;`)
	if err != nil {
		return nil, fmt.Errorf("memory facts missing embedding: %w", err)
	}
	defer rows.Close()
	var out []MemoryFact
	for rows.Next() {
		var f MemoryFact
		if err := rows.Scan(&f.ID, &f.Text); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetMemoryFactEmbedding stores a fact's embedding BLOB.
func SetMemoryFactEmbedding(ctx context.Context, q Execer, id int64, vec []float32) error {
	if _, err := q.ExecContext(ctx,
		`UPDATE memory_facts SET embedding = ? WHERE id = ?;`, EncodeVector(vec), id); err != nil {
		return fmt.Errorf("set memory fact %d embedding: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 9: Implement `EmbedPendingMemoryFacts`**

Append to `internal/core/reembed.go`:

```go
// EmbedPendingMemoryFacts fills embeddings for memory_facts rows that have none,
// best-effort (ADR-028): a nil embedder or an unreachable Ollama leaves them
// NULL — the interval/injection paths do not need them, and the next reindex
// with Ollama up backfills. Embeddings are local and free, so this respects the
// token rule trivially. Returns how many facts were embedded.
func EmbedPendingMemoryFacts(ctx context.Context, sqlDB *sql.DB, embedder embeddings.Provider) (int, error) {
	if embedder == nil {
		return 0, nil
	}
	pending, err := db.MemoryFactsMissingEmbedding(ctx, sqlDB)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	texts := make([]string, len(pending))
	for i, f := range pending {
		texts[i] = f.Text
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		return 0, nil // best-effort: Ollama down; leave embeddings NULL
	}
	n := 0
	for i, f := range pending {
		if i >= len(vecs) {
			break
		}
		if err := db.SetMemoryFactEmbedding(ctx, sqlDB, f.ID, vecs[i]); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
```

- [ ] **Step 10: Wire into the reindex command**

In `cmd/axon/reindex_cmd.go`, inside the `if embeddings {` blocks (both the TTY branch ~line 64 and the plain branch ~line 89), after the `core.ReembedPending(...)` + `core.RefreshVectorIndex(...)` calls succeed, add a best-effort fact-embedding pass. In the plain branch (after line 98):

```go
			if _, err := core.EmbedPendingMemoryFacts(cmd.Context(), sqlDB, embedder); err != nil {
				return fmt.Errorf("embed memory facts: %w", err)
			}
```

And the equivalent inside the TTY `tui.Spin` closure before it returns its summary string. (These are additive lines; do not restructure the surrounding spinner logic.)

- [ ] **Step 11: Run the tests + build**

Run: `env -u FORCE_COLOR go test ./internal/core/ ./internal/db/ && go build ./cmd/axon`
Expected: PASS + clean build.

- [ ] **Step 12: Commit**

```bash
git add internal/core/reindex.go internal/core/reindex_memory_test.go internal/core/reembed.go internal/core/reembed_memory_test.go internal/db/memory.go internal/identity/render.go cmd/axon/reindex_cmd.go
git commit -m "feat(core): rebuild memory_facts in reindex, byte-safe, with best-effort embeddings (FR-135)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: memory-distill routine tier + interval promotion

**Files:**
- Modify: `internal/automations/memory.go:109-113` and `:190-194` (`ModelKey: "synthesis" → "routine"`), `:149-152` (promotion `Entry`), `:284-300` (`memoryEntryText` delegates to `ParseFact`)
- Test: `internal/automations/memory_interval_test.go`

**Interfaces:**
- Consumes: `identity.Remember`, `identity.Entry{…, ValidFrom}` (Task 2), `identity.ParseFact` (Task 1), `identity.Reconcile` (Task 3), `review.Accept` (unchanged), the `agent.Fake` recorded `Calls[i].Model`.
- Produces: no new exported symbol. Behavioural change: consolidation runs at `routine` tier; promoted facts carry `[fact]` + `ValidFrom` + a `[[source]]` wikilink.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/memory_interval_test.go`:

```go
package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/identity"
)

func TestMemoryDistillRunsAtRoutineTier(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- decided to keep the vector store brute-force\n",
	})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- Keep the vector store brute-force until 10^5 chunks.", Model: r.Model, Usage: agent.Usage{InputTokens: 50, OutputTokens: 12}}, nil
	}
	ctx := context.Background()
	if _, err := (MemoryDistill{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("expected one model call, got %d", fake.CallCount())
	}
	// newRC configures Routine: "sonnet", Synthesis: "opus". Routine tier => sonnet.
	if got := fake.Calls[0].Model; got != "sonnet" {
		t.Fatalf("distill model = %q, want routine tier (sonnet)", got)
	}
}

func TestMemoryDistillPromotesIntervalBearingFact(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- moved to Osaka\n",
	})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- Lives in Osaka.", Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 8}}, nil
	}
	ctx := context.Background()
	if _, err := (MemoryDistill{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	lines, _ := identity.BlockLines(ctx, rc.Vault)
	var found bool
	for _, line := range lines {
		f, ok := identity.ParseFact(line)
		if ok && strings.Contains(f.Text, "Lives in Osaka") {
			found = true
			if f.Kind != "fact" {
				t.Errorf("promoted fact kind = %q, want fact", f.Kind)
			}
			if f.ValidFrom != "2026-06-28" { // fixedNow date
				t.Errorf("promoted ValidFrom = %q, want 2026-06-28", f.ValidFrom)
			}
			if !strings.HasPrefix(f.Source, "[[") || !strings.HasSuffix(f.Source, "]]") {
				t.Errorf("promoted source = %q, want a [[wikilink]]", f.Source)
			}
		}
	}
	if !found {
		t.Fatalf("promoted fact not stored: %v", lines)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestMemoryDistillRunsAtRoutineTier|TestMemoryDistillPromotesIntervalBearingFact'`
Expected: FAIL — model is `opus` (synthesis) not `sonnet`; promoted fact has no `[fact]` kind, no wikilink source.

- [ ] **Step 3: Write minimal implementation**

In `internal/automations/memory.go`:

1. In `distil` (the `runModel` call ~line 110), change the tier:

```go
		Operation: "automation.memory-distill", ModelKey: "routine",
```

2. In `compact` (the `runModel` call ~line 191), change the tier:

```go
		Operation: "automation.memory-distill", ModelKey: "routine",
```

3. In `distil`, replace the promotion loop (~lines 149-153) so each promoted fact is interval-bearing. The source wikilink points at the newest recent daily note when there is one, else the `memory-distill` token:

```go
	src := "memory-distill"
	if len(recent) > 0 {
		// recent is newest-first; link the freshest daily note as the source.
		src = "[[" + strings.TrimSuffix(strings.TrimPrefix(recent[0], "Daily/"), ".md") + "]]"
	}
	for _, l := range newFacts {
		if _, err := identity.Remember(ctx, rc.Vault, identity.Entry{
			Text: l, Kind: "fact", Source: src, ValidFrom: today(rc), Date: today(rc),
		}); err != nil {
			return RunResult{}, err
		}
	}
```

4. Replace `memoryEntryText` (~lines 284-300) to delegate to `ParseFact`, which also strips the new `(until …; superseded by …)` annotation:

```go
// memoryEntryText extracts the bare fact from a formatted memory entry line,
// dropping the date prefix, [kind], (source: …) and any (until …) tombstone
// annotation so a distilled statement can be matched against it. It is the
// inverse view of identity.FormatEntry.
func memoryEntryText(line string) string {
	if f, ok := identity.ParseFact(line); ok {
		return f.Text
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/automations/`
Expected: PASS. This runs the whole package, confirming the tier flip did not break the existing `memory_test.go` cases (which do not assert the model string) nor the `standard_test.go` synthesis cases (those belong to other automations — heartbeat/digest — and are unaffected).

- [ ] **Step 5: Verify Accept still closes the interval end-to-end**

The reconcile Accept path (`review.Accept` → `identity.Reconcile`) is exercised by the identity tests (Task 3) and the live smoke (Task 9). Add a focused integration test to `internal/automations/memory_interval_test.go`:

```go
func TestMemoryDistillContradictionQueuesReconcile(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- moved to Osaka\n",
	})
	ctx := context.Background()
	// Seed a current fact the new activity will contradict.
	if _, err := identity.Remember(ctx, rc.Vault, identity.Entry{Text: "Lives in Tokyo", Kind: "fact", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "CONFLICT 1: Lives in Osaka", Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 8}}, nil
	}
	res, err := (MemoryDistill{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "proposed 1 reconciliation") {
		t.Fatalf("summary = %q, want a queued reconciliation", res.Summary)
	}
	// The contradiction is held in the review queue, NOT written to memory yet.
	lines, _ := identity.BlockLines(ctx, rc.Vault)
	for _, line := range lines {
		if strings.Contains(line, "Osaka") {
			t.Fatalf("contradiction must not auto-write to memory: %q", line)
		}
	}
}
```

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestMemoryDistillContradiction`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/automations/memory.go internal/automations/memory_interval_test.go
git commit -m "feat(automations): memory-distill at routine tier, promotes interval-bearing facts (FR-136)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: doctor `memoryFactsCheck`

**Files:**
- Modify: `internal/core/doctor.go` (add `memoryFactsCheck`; register it in `Doctor` next to `annIndexCheck`, ~line 126)
- Test: `internal/core/doctor_memory_test.go`

**Interfaces:**
- Consumes: `db.MemoryFactCounts` (Task 5), `identity.BlockLines` + `identity.ParseFact` (Tasks 1, 6), `config.ResolvedPaths{DBPath, VaultPath}`, the existing `Check`/`CheckStatus`/`StatusOK`/`StatusWarn` types.
- Produces: `func memoryFactsCheck(paths config.ResolvedPaths) Check` — advisory, never fatal.

- [ ] **Step 1: Write the failing test**

Create `internal/core/doctor_memory_test.go`:

```go
package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/vault"
)

func TestMemoryFactsCheckReportsCounts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(dir)
	if _, err := identity.Remember(ctx, v, identity.Entry{Text: "Lives in Tokyo", Kind: "fact", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Reconcile(ctx, v, "Lives in Tokyo", "Lives in Osaka", "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	paths := config.ResolvedPaths{DBPath: dbPath, VaultPath: dir}
	c := memoryFactsCheck(paths)
	if c.Status != StatusOK {
		t.Fatalf("status = %q, want ok", c.Status)
	}
	if !strings.Contains(c.Detail, "1 open") || !strings.Contains(c.Detail, "1 superseded") {
		t.Fatalf("detail = %q, want open/superseded counts", c.Detail)
	}
}

func TestMemoryFactsCheckFlagsUnparseableLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	// A hand-edited block with a line that fails ParseFact.
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	paths := config.ResolvedPaths{DBPath: dbPath, VaultPath: v.Root()}
	c := memoryFactsCheck(paths)
	if c.Status != StatusWarn {
		t.Fatalf("status = %q, want warn for an unparseable block line", c.Status)
	}
}

func TestMemoryFactsCheckNoDatabaseIsOK(t *testing.T) {
	paths := config.ResolvedPaths{DBPath: filepath.Join(t.TempDir(), "missing.sqlite"), VaultPath: t.TempDir()}
	if c := memoryFactsCheck(paths); c.Status != StatusOK {
		t.Fatalf("missing DB should be ok, got %q", c.Status)
	}
}
```

NOTE: `memoryFixture` is defined in `reindex_memory_test.go` (Task 6) in the same `core` package — its third block line (`- not a real entry … ]]]`) parses as an untyped open fact, which is NOT unparseable. To make `TestMemoryFactsCheckFlagsUnparseableLine` deterministic, the check must flag lines that fail `ParseFact` (`ok=false`). Since `memoryFixture`'s garbage line still parses, replace this test's fixture with one whose line truly fails — a line missing the `- ` bullet cannot appear inside `parseEntries`. Instead seed a fixture whose bullet is malformed at the strike markers:

```go
const badMemoryFixture = `---
type: memory
---
## Memory

<!-- axon:memory:start -->
- ~~unterminated strike with no closing markers
<!-- axon:memory:end -->
`
```

Use `badMemoryFixture` in `TestMemoryFactsCheckFlagsUnparseableLine` — `ParseFact` returns `ok=false` for a `- ~~…` line with no closing `~~` (see Task 1 `ParseFact`, which returns `ok=false` when the closing `~~` is absent).

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestMemoryFactsCheck`
Expected: FAIL — `undefined: memoryFactsCheck`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/core/doctor.go` (add `identity` and `vault` to the imports if absent):

```go
// memoryFactsCheck reports the derived memory_facts index size (open/superseded)
// and flags any axon:memory block line that fails ParseFact — a parse anomaly
// means someone hand-edited a fact into an unparseable shape. Advisory: a
// missing/unreadable DB or absent layer is reported ok and never fails doctor.
func memoryFactsCheck(paths config.ResolvedPaths) Check {
	const name = "memory-facts"
	ctx := context.Background()

	// Flag unparseable block lines directly from the vault (no DB needed).
	v := vault.NewFS(paths.VaultPath)
	if lines, err := identity.BlockLines(ctx, v); err == nil {
		for _, line := range lines {
			if _, ok := identity.ParseFact(line); !ok {
				return Check{name, StatusWarn,
					fmt.Sprintf("MEMORY.md has an unparseable memory line: %q — fix it in Obsidian", line)}
			}
		}
	}

	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{name, StatusOK, "no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{name, StatusOK, "database not readable; skipped"}
	}
	defer func() { _ = d.Close() }()

	total, open, superseded, err := db.MemoryFactCounts(ctx, d)
	if err != nil {
		return Check{name, StatusOK, "memory facts not counted; skipped"}
	}
	return Check{name, StatusOK,
		fmt.Sprintf("%d facts (%d open / %d superseded)", total, open, superseded)}
}
```

Register it in `Doctor`, in the per-profile block next to `annIndexCheck` (~line 126):

```go
			checks = append(checks, annIndexCheck(p, paths))
			checks = append(checks, memoryFactsCheck(paths))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run 'TestMemoryFactsCheck|TestDoctor'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/doctor_memory_test.go
git commit -m "feat(doctor): memory-facts check reports counts and flags unparseable lines (FR-135)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Final wiring, full suite, and live smoke

**Files:** none new. This task is a verification checklist (no code) plus the scratch-vault live smoke from the spec's Testing section.

- [ ] **Step 1: Full build + suite + lint**

```bash
go build ./cmd/axon
env -u FORCE_COLOR go test ./...
golangci-lint run
```
Expected: clean build, all packages PASS, lint green. Fix any `goimports`/`go vet` issues surfaced.

- [ ] **Step 2: `axon doctor` passes**

```bash
./axon doctor
```
Expected: exit 0; the new `memory-facts` line appears and is `ok` (or a benign `warn` if the local vault has a hand-edited line). No `fail`.

- [ ] **Step 3: Live smoke — seed a legacy entry + a superseding daily note**

Use a scratch `AXON_HOME` so nothing touches the real vault:

```bash
export AXON_HOME="$(mktemp -d)"
./axon init --yes        # or the project's non-interactive bootstrap
```
Seed a legacy (pre-R1) open fact into `02-Areas/Profile/MEMORY.md`'s `axon:memory` block:
```
- 2026-07-05 — Lives in Tokyo [fact] (source: session)
```
Add a daily note `Daily/2026-07-07.md` asserting the superseding fact ("Moved to Osaka this week").

- [ ] **Step 4: Dry-run then real consolidation**

```bash
./axon run memory-distill --dry-run    # reports "would … propose 1 reconciliation(s)"
./axon run memory-distill              # queues the reconcile (contradiction held, not auto-written)
```
Expected: the model call is logged at the **routine** tier in the token ledger / dashboard event (not synthesis). (Requires Claude/Ollama auth; where absent the fake-agent units in Task 7 stand in.)

- [ ] **Step 5: Accept the reconcile**

```bash
./axon review list                     # find the reconcile item id
./axon review accept <id>
```
Expected: `MEMORY.md`'s block now shows the Tokyo line tombstoned as
`- ~~2026-07-05 — Lives in Tokyo [fact] (source: session)~~ (until <today>; superseded by "…Osaka…")`
with a fresh open Osaka fact prepended.

- [ ] **Step 6: Reindex and verify the projection**

```bash
./axon reindex
```
Then inspect the DB:
```bash
sqlite3 "$AXON_HOME/<profile>/axon.db" \
  "SELECT text, valid_from, valid_until, struck FROM memory_facts ORDER BY line_no;"
```
Expected: exactly one open row (Osaka, `valid_until` NULL, `struck` 0) and one closed row (Tokyo, `valid_until` = today, `struck` 1). Confirm `MEMORY.md` bytes are unchanged by the reindex (diff before/after).

- [ ] **Step 7: Verify injection prefers the current fact**

Trigger a SessionStart render (e.g. `./axon hook session-start` or the project's equivalent) and confirm the injected "Recent memory" shows **only** the Osaka open fact — the Tokyo closed fact is excluded (FR-137).

- [ ] **Step 8: Confirm S8 (all-off still useful)**

Disable `memory-distill` in config; re-run `./axon reindex` and a SessionStart render. Facts still parse with intervals, the index still rebuilds, injection still filters — only auto-consolidation stops.

- [ ] **Step 9: Commit the completed slice marker (if the project tracks it)**

No code change is expected here; if docs/roadmap status needs a checkbox flip for R1, do it in a docs-only commit:

```bash
git commit --allow-empty -m "chore(R1): temporal memory layer slice complete (FR-134..137)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- FR-134 (interval grammar, open + closed forms, backward compatible) → Tasks 1, 2. ✅ (legacy `(superseded DATE)` and untyped lines covered by `ParseFact` table + `tombstone` legacy fallback.)
- FR-135 (derived `memory_facts`, read-only reindex rebuild, S9 determinism, best-effort embeddings, doctor counts) → Tasks 5, 6, 8. ✅ (vault-bytes-unchanged assertion in Task 6; delete-DB→reindex determinism in Task 6.)
- FR-136 (interval-aware supersedence via `Reconcile`, routine-tier `memory-distill`, `[fact]`+`valid_from`+`[[source]]` promotion) → Tasks 3, 7. ✅
- FR-137 (injection prefers currently-valid facts, no DB dependency, legacy/empty unchanged) → Task 4. ✅
- Cardinal rule 1 (no new model call; routine tier) → Task 7 tier flip; asserted via `fake.Calls[0].Model == "sonnet"`. ✅
- Cardinal rule 2 (wikilink-safe, tombstone-not-delete) → Tasks 2, 3 (Patch into block); Task 6 read-only reindex. ✅

**Placeholder scan:** No "TBD"/"add error handling"/"similar to Task N" — every code step has real Go. Task 5 uses the inline `Open(MemoryDSN)`+`Migrate` form the `internal/db` tests already use. ✅

**Type-name consistency:** `Fact`/`ParseFact` (identity), `Entry.ValidFrom`, `tombstone(line,date,supersededBy)`, `MemoryFact`/`ReplaceMemoryFacts`/`OpenFacts`/`MemoryFactCounts`/`MemoryFactsMissingEmbedding`/`SetMemoryFactEmbedding` (db), `BlockLines` (identity), `rebuildMemoryFacts`/`EmbedPendingMemoryFacts`/`memoryFactsCheck` (core) — used identically across all tasks. `Reconcile` signature unchanged throughout. ✅

**Cross-task interface note:** Task 4 narrows `RecentEntries` to open-only, so Task 6 introduces `identity.BlockLines` for the unfiltered projection reindex needs — this dependency is stated in both tasks. ✅
