# T1 — Action index: grammar, derived table, CLI — design

**Slice:** T1 (roadmap `docs/16-roadmap-1.2.5.md`) · **Date:** 2026-07-10
**FR:** FR-157, FR-158, FR-159 · **ADR:** ADR-033
**Status:** design approved; ready for implementation plan.

> Current maxima before this slice: FR-156, ADR-032. After: FR-159, ADR-033.
> Migration: `0007_actions.sql` (next free; `0006_eval_runs.sql` is the last).

## 1. Summary

The foundation the whole 1.2.5 "act on it" theme reads. Three pieces:

- **A tolerant parser** (FR-157) for checkbox lines in the **Obsidian Tasks emoji
  grammar** — the single structured task parser AXON has ever had (today it's
  `strings.Count("- [ ]")` in three places plus one regex). A pure leaf package
  `internal/actions`.
- **A derived, disposable `actions` SQLite table** (FR-158) rebuilt inside the
  reindex transaction from Markdown alone — the exact `memory_facts`/ADR-028
  pattern (delete-all + insert, S9-safe). Never authoritative; `reindex` rebuilds
  it byte-equivalently.
- **`axon actions`** (FR-159) — a read-only CLI to list/filter/count actions,
  `--json`-capable, structured like `axon related`.

**Zero model calls, zero vault mutation.** T1 only reads Markdown and populates a
derived index. The consolidated note (T2), dashboard (T3), and the one write —
completion (T3/ADR-034) — build on this; T1 defines the identity hash they use
but performs no writes itself.

## 2. Decisions (approved)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Index scope | **All but system dirs.** Parse checkbox lines in every `.md` note except `Templates/`, `.axon/`, `.trash/`, fenced code blocks, and the `axon:actions` projection block. **Index `04-Archive/` but flag rows `archived = 1`** so open/active views exclude them while they stay queryable. |
| 2 | Checkbox states | **Tolerant, 3 states.** `[ ]`→`open`; `[x]`/`[X]`→`done`; `[-]`→`cancelled` (excluded from open views, **not** counted as done); any other single char (`[/]`, `[>]`, …)→`open`. The parser never errors on an unknown marker. |
| 3 | Status model | **One primary bucket by precedence + raw fields.** Each action resolves to ONE bucket (`done > cancelled > someday > waiting > overdue > today > scheduled > next`) computed **at read time**; the table stores raw `due`/`scheduled`/`start`/`tags`/`contexts` so any consumer can slice independently (e.g. "overdue among waiting"). |

Decisions folded in without a question (stated here for the record):

- **Identity hash** = `sha256hex(source_path + "\n" + collapseWS(checkbox-stripped line body))` — **state-independent** (so `[ ]`→`[x]` doesn't change identity) but **content-sensitive** (rescheduling *is* a new identity, so a stale dashboard row 409s on completion — the intended T3/ADR-034 behavior). Duplicate identical lines in one note → the completion mutation targets the first still-open match.
- **Date grammar = emoji only for v1.** `📅` due, `⏳` scheduled, `🛫` start, `✅` done-date, priority `🔺⏫🔼🔽⏬`. Dataview inline-field syntax (`[due:: …]`) is **out of scope** (additive follow-up). `🔁` recurrence is parse-tolerated (left verbatim in text), never expanded (roadmap non-goal).
- **Subtasks are flattened.** Indented checkbox lines are indexed as independent actions; T1 models no parent/child hierarchy (non-goal).
- **Bucket is not stored.** It's date-relative (`overdue`/`today` depend on "now"), so storing it would go stale at midnight. Only date-independent fields persist; the bucket is a pure Go function over a row + `today`.

## 3. The parser — `internal/actions` (FR-157)

A new **leaf package** (imports only stdlib), so the grammar + GTD logic are unit-testable in isolation and reused by reindex, the CLI, and later T2/T3.

```go
package actions

type State string // "open" | "done" | "cancelled"

type Action struct {
    SourcePath string   // vault-relative note the checkbox lives in
    LineNo     int      // 0-based index in the note body (ordering; not the match key)
    Section    string   // nearest enclosing heading text ("" if none)
    Text       string   // task text, date/priority emoji stripped; tags/contexts/links kept
    Raw        string   // the full original line (byte-precise completion match, T3)
    State      State
    Checkbox   string   // the literal marker char: " ", "x", "-", "/", …
    Priority   string   // "highest"|"high"|"medium"|"low"|"lowest"|""
    Due        string   // "YYYY-MM-DD" | ""
    Scheduled  string
    Start      string
    DoneDate   string   // ✅ date | ""
    Project    string   // first [[wikilink]] target on the line | "" (fallback: SourcePath)
    Contexts   []string // @context tokens
    Tags       []string // #tags (incl. #someday / #waiting)
    Archived   bool     // source note under 04-Archive/
}

// Parse turns ONE line into an Action. ok=false for a non-checkbox line.
func Parse(line string) (Action, bool)

// Extract walks a whole note body: tracks the current heading, skips fenced code
// and the axon:actions projection block, and returns every checkbox line parsed.
// archived is threaded from the caller (folder-derived). SourcePath/LineNo/Section
// /Archived are filled here; Parse fills the line-local fields.
func Extract(sourcePath, body string, archived bool) []Action

// Hash is the stable, state-independent identity (see §2).
func (a Action) Hash() string

// Bucket resolves the single GTD bucket by the approved precedence.
func Bucket(a Action, today time.Time) string // overdue|today|scheduled|next|waiting|someday|done|cancelled
```

**`Parse` shape** (mirrors `identity.parseFactBody` — string-slicing, not one mega-regex):

1. `checkboxRe = ^(\s*)[-*+] \[(.)\] (.*)$`. No match → `false`.
2. `Checkbox` = the captured char; `State` from it (`" "`→open, `x`/`X`→done, `-`→cancelled, else open).
3. Peel emoji fields off the body with small per-field regexes (`📅\s*(\d{4}-\d{2}-\d{2})`, etc.), removing matched spans as they're captured; map the priority emoji to its word.
4. Collect `@ctx` and `#tag` tokens (regex over remaining text); first `[[target]]` (alias/`#heading` stripped) → `Project`.
5. `Text` = the residue after date/priority stripping, whitespace-collapsed. Tags, contexts and wikilinks stay in `Text` (they read naturally in a list).

**`Extract` shape:** split `body` on `\n`; maintain `inFence` (toggled by ```` ``` ```` / `~~~`) and `inActionsBlock` (between `<!-- axon:actions:start -->` / `:end`); track `section` from the latest `^#{1,6} ` heading; skip lines while `inFence || inActionsBlock`; else `Parse` and, on `ok`, stamp `SourcePath`/`LineNo`/`Section`/`Archived`.

**`Bucket` precedence** (read-time): `done`→done; `cancelled`→cancelled; else open → `#someday` tag→someday; `#waiting` tag→waiting; `Due != "" && Due < today`→overdue; `Due == today`→today; `Start > today || Scheduled > today`→scheduled; else next. (Date comparisons are lexical on `YYYY-MM-DD`.)

## 4. The derived table — `0007_actions.sql` + `internal/db/actions.go` (FR-158)

**Migration** (auto-discovered via the existing `//go:embed migrations/*.sql`; no wiring):

```sql
-- 0007_actions — 1.2.5 T1 (ADR-033). A DERIVED, disposable projection of the
-- checkbox lines across the vault: reindex delete-all+inserts these rows from
-- Markdown (the vault is the source of truth, ADR-011). Never authoritative.
CREATE TABLE actions (
  id           INTEGER PRIMARY KEY,
  hash         TEXT NOT NULL,          -- state-independent identity (path + normalized body)
  source_path  TEXT NOT NULL,          -- vault-relative note the checkbox lives in
  line_no      INTEGER NOT NULL,       -- 0-based body line index (ordering/display)
  section      TEXT,                   -- nearest enclosing heading ("" if none)
  text         TEXT NOT NULL,          -- task text, date/priority markers stripped
  raw          TEXT NOT NULL,          -- full original line (byte-precise completion match)
  state        TEXT NOT NULL,          -- open | done | cancelled  (checkbox-derived, date-independent)
  checkbox     TEXT NOT NULL,          -- literal marker char
  priority     TEXT,                   -- highest|high|medium|low|lowest | NULL
  due          TEXT,                   -- YYYY-MM-DD | NULL
  scheduled    TEXT,
  start        TEXT,
  done_date    TEXT,                   -- ✅ date | NULL
  project      TEXT,                   -- explicit [[link]] target | NULL (fallback = source_path)
  contexts     TEXT,                   -- json array of @context tokens
  tags         TEXT,                   -- json array of #tags
  archived     INTEGER NOT NULL DEFAULT 0,
  updated      TEXT NOT NULL
);
CREATE INDEX idx_actions_open   ON actions(state) WHERE state = 'open';
CREATE INDEX idx_actions_source ON actions(source_path);
```

**Repository** (`internal/db/actions.go`, mirroring `internal/db/memory.go` — reuse `nullify`/`boolInt`, the `Execer`/`Queryer`/`Queryer2` interfaces, and JSON-array (un)marshal helpers):

```go
type Action struct { /* the columns above, []string Contexts/Tags, bool Archived */ }

func ReplaceActions(ctx context.Context, q Execer, as []Action) error   // DELETE FROM actions; then INSERT each (caller-ordered)
func ListActions(ctx context.Context, q Queryer2, opts ListOpts) ([]Action, error) // filter by source_path/state; empty opts = all
func ActionStateCounts(ctx context.Context, q Queryer) (total, open, done, cancelled, archived int, err error) // SQL COUNT/SUM — the date-independent counts (doctor)
```

Date-relative counts (overdue / due-today / someday / waiting) are computed in Go over the loaded rows via the `actions` package — one source of truth for bucket logic, no `today` baked into SQL. `ReplaceActions` inserts in caller order (by `source_path`, then `LineNo`) for a deterministic, S9-comparable table.

## 5. Reindex wiring — `internal/core/reindex.go` (FR-158)

Actions differ from `memory_facts` in one way: they're scattered across **every**
note, not in one managed block. So they ride the existing per-note reindex loop
(which already reads every body via `v.Read`) rather than a single-block read.

- In the note loop (around `reindex.go:56`), for each note also compute
  `archived := strings.HasPrefix(path, "04-Archive/")` and
  `acts = append(acts, actions.Extract(path, n.Body, archived)...)`.
- After the loop, before `tx.Commit()` (next to the `rebuildMemoryFacts` call at
  ~`reindex.go:149`), map `[]actions.Action → []db.Action` and
  `db.ReplaceActions(ctx, tx, rows)` **inside the same transaction**. A failure
  rolls back with the rest, leaving the prior index intact (S9).
- No embedding column, so **no `EmbedPending…` counterpart** and no
  `reindex_cmd.go` change — actions are pure text/metadata (embeddings are a
  possible future for a "similar tasks" feature; explicitly not T1).

`n.Body` is already frontmatter-stripped, so `LineNo` is body-relative — fine, since the T3 completion writer matches by `Hash()`, not line number.

## 6. The CLI — `axon actions` (FR-159)

`cmd/axon/actions_cmd.go`, structured like `related_cmd.go`:

- `Use: "actions"`, no positional args.
- Wiring: `loadProfileDeps(gf, true)` → `db.ListActions(...)` → compute buckets via
  `actions.Bucket` → filter/sort/render.
- **Flags:** `--status string`, `--project string`, `--context string`, `--all` (include done + cancelled + archived), `--json`. `--status` accepts a `Bucket()` name (`overdue|today|scheduled|next|waiting|someday|done|cancelled`) **or** one of two convenience aggregates: `open` (every non-done/cancelled/archived action — the default when `--status` is empty) and `week` (open actions with `due` within the next 7 days). Unknown value → a clear error listing the accepted set.
- **Default output:** a one-line counts header (`Open N · Overdue N · Today N · Waiting N · Someday N · Done(7d) N`) then the list sorted by bucket precedence then `due`, columns *status · due · text · source*. On a TTY, a `tui.Table`; non-TTY a plain `ui.For`-styled list; empty → a friendly line.
- `--json` emits `[]{Action + computed bucket}` via `json.NewEncoder` (indented), so downstream tooling gets the bucket without re-deriving it.
- Registered on the data-command `root.AddCommand(...)` line in `cmd/axon/root.go`.

"This week" = **rolling 7 days** from today (matches the T2 decision, avoids ISO-week edge confusion).

## 7. Doctor

Add an advisory `actionsCheck` in `internal/core` doctor (the `mergeCheck`/`relatedCheck` template, `StatusOK` always): opens the DB read-only, reports the state counts from `db.ActionStateCounts` (e.g. "142 actions indexed: 37 open, 100 done, 5 cancelled") and warns if it can't open/read. It does **not** re-run the parser or flag unparseable lines — a non-checkbox line simply isn't an action, so there's nothing to warn about (contrast `memoryFactsCheck`, which warns on malformed block lines). Never fails the build.

## 8. Guardrails & invariants

- **Cardinal rule 1 (no Claude bypass):** N/A — T1 makes no model call. No token-ledger entry (nothing to ledger).
- **Cardinal rule 2 (wikilink-safe):** T1 is **read-only**. No `vault.write`/`patch`/`move`, no `fs` writes. The single new mutation (completion) is T3/ADR-034, deliberately not in this slice.
- **S8 (all-off still useful):** T1 adds a derived index + a read command; nothing is scheduled, nothing spends. With every automation off, `axon actions` still works and nothing else changes.
- **S9 (vault rebuilds DB, never reverse):** the `actions` table is derived and disposable; `reindex` rebuilds it from Markdown byte-equivalently. No task truth exists only in SQLite. Tested explicitly (reindex twice → identical rows).
- **NFR-05 (content is data):** task text is parsed as data; T1 never interprets it as instructions (it makes no model call at all).
- **Determinism:** `List` sort + caller-ordered inserts make the table order-stable; parsing is pure.

## 9. Testing strategy

- **Parser (`internal/actions`), table-driven:** the grammar matrix — each state incl. unknown markers; each emoji field present/absent/combined; priority emoji; `@ctx`/`#tag`/`[[link]]` extraction; fenced-code + `axon:actions`-block skipping in `Extract`; heading tracking; `Hash()` state-independence (`[ ]` vs `[x]` of the same body hash-equal) and reschedule-sensitivity; `Bucket` precedence (incl. waiting-outranks-overdue with the raw `due` still set). Pathological input (huge line, emoji-dense, nested lists, no trailing newline) never panics.
- **DB (`internal/db`):** real in-memory SQLite + `Migrate`; `ReplaceActions` then `ListActions` round-trips; `ActionStateCounts` matches; delete-all replaces cleanly on re-run.
- **Reindex (`internal/core`):** real vault fixture with checkbox lines across ≥4 notes incl. one under `04-Archive/`; assert rows/states/dates/`archived`; **reindex-twice byte-equivalence** (S9); a note edited between passes flips state.
- **CLI:** `run(t, "actions", "--json", "--config", cfg)` (the `cli_test.go` harness), asserting filtered/sorted JSON and the counts header.
- **Doctor:** `actions_doctor_test.go` — `StatusOK` + the count-substring, empty-vault and populated cases.
- **Live smoke:** seed a scratch vault with a spread of real-looking tasks, `axon reindex`, `axon actions` / `--status overdue` / `--json`; confirm counts and buckets by eye; reindex twice → identical. No Claude/Ollama needed (zero model calls). Run suites with `env -u FORCE_COLOR`. Never touch the user's `:7777` daemon.

## 10. Build order (for the implementation plan)

1. `internal/actions`: `Action`, `Parse`, `Extract`, `Hash`, `Bucket` + the full table-driven parser tests. *(Pure leaf; nothing else compiles against it yet.)*
2. `internal/db`: `0007_actions.sql` + `actions.go` (`Action` row, `ReplaceActions`, `ListActions`, `ActionStateCounts`) + db tests.
3. `internal/core/reindex.go`: per-note `Extract` + `rebuildActions` in-tx + reindex tests (incl. S9 byte-equivalence).
4. `cmd/axon/actions_cmd.go` + `root.go` registration + CLI test.
5. `internal/core` doctor `actionsCheck` + `actions_doctor_test.go`.
6. Docs at build: `docs/03` FR-157/158/159 rows; `docs/02` ADR-033; `docs/04` `actions` DDL block; `docs/16` T1 line marked built; CLAUDE.md FR/ADR ranges + migration note; `axon automations`/GUIDE unaffected (no automation/tool yet).

## 11. Out of scope (this slice)

- Any **write** — completion, tagging, consolidation (T2/T3/T5).
- The consolidated `axon:actions` note projection (T2) and dashboard tab (T3).
- MCP tools and the SessionStart pointer (T4).
- Dataview inline-field date syntax; recurrence expansion; subtask hierarchy.
- Task **embeddings** / "similar tasks" (the table intentionally has no vector column).
- Converging `daily-log`/`heartbeat`'s ad-hoc parsing onto the index — that's FR-161 (T2), not T1.
