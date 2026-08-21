# Recipe vocabulary v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a recipe read the review queue and reach the `sources` table and note staleness, by splitting the recipe path validator into read and write rules and adding two readers.

**Architecture:** Three packages, in dependency order. `internal/db` gains a `sources` read projection and a limit on an existing helper (leaf, no new imports). `internal/config` splits `validRecipePath` into `validRecipeReadPath`/`validRecipeWritePath` and adds two reader structs with their validation. `internal/automations` renders the two new readers inside the existing `resolveInputs` switch. There is no engine change, no new sink, no new ADR, and no migration.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite` (pure-Go, FTS5), table-driven tests, `goccy/go-yaml` + `go-playground/validator` for config.

**Spec:** `docs/superpowers/specs/2026-08-21-recipe-vocabulary-v2-design.md`

## Global Constraints

- **FR IDs:** FR-202 (path split + `.axon/` read allow-list + self-feeding refusal), FR-203 (the two readers + db seam). Trace every change to one of them.
- **ADR-039 is amended, not superseded.** No ADR-040. No new sink, no fan-out, no template logic.
- **No migration.** Schema stays `0007`. `sources` and `notes` DDL are untouched.
- **Cardinal rule 1:** no new path reaches Claude. Both new readers are zero-model (pure SQL).
- **Cardinal rule 2:** no new writer. `vault.Patch` into an `axon:` block and the review-queue append remain the only two sinks.
- **`.axon/` read allow-list is exactly two files:** `.axon/review-queue.md`, `.axon/review-queue-archive.md`. Nothing else under `.axon/` becomes readable, and nothing under `.axon/` becomes writable.
- **Reader ranges (copied verbatim from the spec):** `stale_notes.older_than_days` 0–3650 (0 → default 90), `stale_notes.limit` 0–100 (0 → default 20), `sources.older_than_days` 0–3650 (0 → **no age filter**), `sources.limit` 0–100 (0 → default 20). `recent_notes` keeps its existing 0–90 / 0–100 caps unchanged.
- **Rendered line shapes:** `stale_notes` → `[[path]] (updated DATE)`; `sources` → `[[path]] — url (fetched DATE, kind, status)`, and with no note row → `url (fetched DATE, kind, status)`. The dash is an em dash (`—`), matching the spec.
- **`sources.fetched_at` is RFC3339 UTC**; lexicographic `<` is the chronological compare (`CountSourcesSince` is the precedent).
- **Go hygiene:** `gofmt`/`goimports` clean, `go vet` and `golangci-lint` green, errors wrapped with `%w`, `context.Context` propagated. Run `gofmt -l .` before every commit.
- **Never bind port 7777** in any smoke config — that is the live daemon's port. Smoke work uses 7799.

---

### Task 1: `db.SourcesOlderThan` — the sources read projection

**Files:**
- Modify: `internal/db/chunks.go` (add `SourceInfo` + `SourcesOlderThan` after `GetSourceByURL`)
- Test: `internal/db/chunks_test.go` (create if absent; if present, append)

**Interfaces:**
- Consumes: `db.Queryer2` (`internal/db/notes.go:89`), `db.SourceRow` + `db.UpsertSource` (`internal/db/chunks.go`), `db.InsertNote` + `db.NoteRow`, `newMigratedDB(t)` (`internal/db/notes_test.go:9`).
- Produces: `db.SourceInfo{Path, URL, Kind, FetchedAt, Status string}` and `db.SourcesOlderThan(ctx context.Context, q Queryer2, beforeTS string, limit int) ([]SourceInfo, error)`. Task 5 calls it.

- [ ] **Step 1: Write the failing test**

Append to `internal/db/chunks_test.go` (create the file with `package db` and imports `context`, `testing` if it does not exist):

```go
// seedSource inserts one note + one source row, returning the source id.
func seedSource(t *testing.T, d *sql.DB, path, url, kind, fetchedAt, status string) {
	t.Helper()
	ctx := context.Background()
	var notePtr *int64
	if path != "" {
		id, err := InsertNote(ctx, d, NoteRow{Path: path, Title: path, Updated: "2026-01-01"})
		if err != nil {
			t.Fatal(err)
		}
		notePtr = &id
	}
	if _, err := UpsertSource(ctx, d, SourceRow{
		NoteID: notePtr, URL: url, Kind: kind,
		FetchedAt: fetchedAt, ContentHash: "h", Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSourcesOlderThan(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	seedSource(t, d, "03-Resources/Knowledge/Old.md", "https://old.example/a", "url", "2025-01-01T00:00:00Z", "ok")
	seedSource(t, d, "03-Resources/Knowledge/New.md", "https://new.example/b", "url", "2026-08-01T00:00:00Z", "ok")
	seedSource(t, d, "", "https://orphan.example/c", "pdf", "2024-06-01T00:00:00Z", "failed")

	// Age filter: only the two predating the cutoff.
	got, err := SourcesOlderThan(ctx, d, "2026-01-01T00:00:00Z", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("age filter: want 2 rows, got %d: %+v", len(got), got)
	}
	// Newest first.
	if got[0].URL != "https://old.example/a" || got[1].URL != "https://orphan.example/c" {
		t.Fatalf("ordering wrong: %+v", got)
	}
	// A source with no note row carries an empty Path.
	if got[1].Path != "" || got[1].Kind != "pdf" || got[1].Status != "failed" {
		t.Fatalf("orphan projection wrong: %+v", got[1])
	}
	if got[0].Path != "03-Resources/Knowledge/Old.md" {
		t.Fatalf("joined path wrong: %+v", got[0])
	}

	// Empty cutoff means no age filter.
	all, err := SourcesOlderThan(ctx, d, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("no-filter: want 3 rows, got %d", len(all))
	}

	// Limit caps the result.
	one, err := SourcesOlderThan(ctx, d, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].URL != "https://new.example/b" {
		t.Fatalf("limit: want the newest single row, got %+v", one)
	}
}
```

Ensure the file's import block is `import ("context"; "database/sql"; "testing")`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestSourcesOlderThan -v`
Expected: FAIL — `undefined: SourcesOlderThan` (compile error).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/db/chunks.go`, immediately after `GetSourceByURL`:

```go
// SourceInfo is the read-layer projection of one ingested source joined to
// its note (FR-203). Distinct from the write-side SourceRow: Path replaces
// NoteID, and a source whose note is gone (ON DELETE SET NULL) carries an
// empty Path.
type SourceInfo struct {
	Path      string
	URL       string
	Kind      string
	FetchedAt string
	Status    string
}

// SourcesOlderThan lists sources fetched strictly before beforeTS (RFC3339
// UTC, so a lexicographic compare is chronological — the CountSourcesSince
// precedent), newest first, capped at limit. An empty beforeTS applies no
// age filter; limit <= 0 means unlimited. Status is returned, never filtered
// on: a failed or redacted source is exactly what a freshness recipe wants
// to surface.
func SourcesOlderThan(ctx context.Context, q Queryer2, beforeTS string, limit int) ([]SourceInfo, error) {
	query := `SELECT COALESCE(n.path,''), COALESCE(s.url,''), COALESCE(s.kind,''),
	                 COALESCE(s.fetched_at,''), COALESCE(s.status,'')
	            FROM sources s LEFT JOIN notes n ON n.id = s.note_id`
	var args []any
	if beforeTS != "" {
		query += ` WHERE s.fetched_at != '' AND s.fetched_at IS NOT NULL AND s.fetched_at < ?`
		args = append(args, beforeTS)
	}
	query += ` ORDER BY s.fetched_at DESC, s.url`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := q.QueryContext(ctx, query+";", args...)
	if err != nil {
		return nil, fmt.Errorf("sources older than %q: %w", beforeTS, err)
	}
	defer rows.Close()
	var out []SourceInfo
	for rows.Next() {
		var s SourceInfo
		if err := rows.Scan(&s.Path, &s.URL, &s.Kind, &s.FetchedAt, &s.Status); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ -run TestSourcesOlderThan -v && gofmt -l internal/db/`
Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/db/chunks.go internal/db/chunks_test.go
git commit -m "feat(db): SourcesOlderThan read projection for recipe readers (FR-203)"
```

---

### Task 2: `db.NotesUpdatedBefore` gains a limit

**Files:**
- Modify: `internal/db/notes.go:311-321` (`NotesUpdatedBefore`)
- Modify: `internal/automations/actionsreview.go:32` (the one caller)
- Test: `internal/db/notes_test.go` (append)

**Interfaces:**
- Consumes: `db.Queryer2`, `db.NoteStamp`, `scanNoteStamps` (all in `internal/db/notes.go`).
- Produces: `db.NotesUpdatedBefore(ctx context.Context, q Queryer2, beforeDate string, limit int) ([]NoteStamp, error)` — signature **changed**, `limit <= 0` means unlimited. Task 5 calls it with a real limit.

- [ ] **Step 1: Write the failing test**

Append to `internal/db/notes_test.go`:

```go
func TestNotesUpdatedBeforeLimit(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	for _, u := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
		if _, err := InsertNote(ctx, d, NoteRow{Path: "n-" + u + ".md", Title: u, Updated: u}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := NotesUpdatedBefore(ctx, d, "2025-01-01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("limit 0 must be unlimited, got %d", len(all))
	}
	// Oldest first (existing ORDER BY updated, path).
	if all[0].Updated != "2024-01-01" {
		t.Fatalf("ordering changed: %+v", all[0])
	}
	two, err := NotesUpdatedBefore(ctx, d, "2025-01-01", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 2 || two[0].Updated != "2024-01-01" {
		t.Fatalf("limit 2 wrong: %+v", two)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestNotesUpdatedBeforeLimit -v`
Expected: FAIL — `too many arguments in call to NotesUpdatedBefore` (compile error).

- [ ] **Step 3: Write minimal implementation**

Replace `NotesUpdatedBefore` in `internal/db/notes.go` with:

```go
// NotesUpdatedBefore lists notes last updated strictly before beforeDate
// (YYYY-MM-DD), excluding notes with no updated stamp, oldest first.
// limit <= 0 means unlimited.
func NotesUpdatedBefore(ctx context.Context, q Queryer2, beforeDate string, limit int) ([]NoteStamp, error) {
	query := `SELECT id, path, COALESCE(title,''), COALESCE(updated,'')
	            FROM notes WHERE updated != '' AND updated IS NOT NULL AND updated < ?
	           ORDER BY updated, path`
	args := []any{beforeDate}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := q.QueryContext(ctx, query+";", args...)
	if err != nil {
		return nil, fmt.Errorf("notes updated before: %w", err)
	}
	defer rows.Close()
	return scanNoteStamps(rows)
}
```

Then update the sole caller, `internal/automations/actionsreview.go:32`:

```go
	stale, err := db.NotesUpdatedBefore(ctx, rc.DB, cutoff, 0)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ ./internal/automations/ -run 'TestNotesUpdatedBeforeLimit|ActionsReview' -v && gofmt -l internal/db internal/automations`
Expected: PASS for both packages (the actions-review tests must still pass unchanged — passing 0 preserves the old behaviour), `gofmt -l` silent.

- [ ] **Step 5: Commit**

```bash
git add internal/db/notes.go internal/db/notes_test.go internal/automations/actionsreview.go
git commit -m "feat(db): NotesUpdatedBefore takes a limit; actions-review passes 0 (FR-203)"
```

---

### Task 3: Split the recipe path validator (FR-202)

**Files:**
- Modify: `internal/config/recipes.go:88-99` (`validRecipePath`) and its two call sites (input note ~line 137, block sink ~line 180)
- Modify: `internal/config/recipes_test.go` (the existing `"note path .axon"` case at line ~54 **flips meaning** — it must become a legal case)
- Test: `internal/config/recipes_test.go` (new table)

**Interfaces:**
- Consumes: nothing new.
- Produces: `validRecipeReadPath(p string) error` and `validRecipeWritePath(p string) error` in `internal/config`, plus the package-level `readableAxonFiles` map. Task 4 does not use them directly; Task 6's docs describe them.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/recipes_test.go`:

```go
func TestRecipePathReadWriteSplit(t *testing.T) {
	cases := []struct {
		path            string
		readOK, writeOK bool
	}{
		{"03-Resources/Notes.md", true, true},
		{".axon/review-queue.md", true, false},         // FR-202: reading is not writing
		{".axon/review-queue-archive.md", true, false}, // the second allow-listed file
		{".axon/logs/run.md", false, false},            // everything else under .axon/ stays closed
		{".axon/exports/2026/bundle.md", false, false},
		{".axon/snapshots/s.md", false, false},
		{".trash/gone.md", false, false},
		{"../etc/passwd.md", false, false},
		{"/abs/path.md", false, false},
		{"notes.txt", false, false}, // non-.md
		{"   ", false, false},       // empty
	}
	for _, c := range cases {
		if got := validRecipeReadPath(c.path) == nil; got != c.readOK {
			t.Errorf("read %q: allowed=%v want %v", c.path, got, c.readOK)
		}
		if got := validRecipeWritePath(c.path) == nil; got != c.writeOK {
			t.Errorf("write %q: allowed=%v want %v", c.path, got, c.writeOK)
		}
	}
}

func TestValidateRecipesAllowsReviewQueueRead(t *testing.T) {
	r := validRecipe()
	r.Inputs[1].Note.Path = ".axon/review-queue.md"
	if err := validateRecipes(Profile{Recipes: []Recipe{r}}); err != nil {
		t.Fatalf("block-sink recipe reading the review queue must be legal: %v", err)
	}
}
```

Then **change the existing rejection case** in `TestValidateRecipesRejects` (currently `{"note path .axon", func(r *Recipe) { r.Inputs[1].Note.Path = ".axon/review-queue.md" }, "path"}`) to point at a still-refused file:

```go
		{"note path .axon non-allowlisted", func(r *Recipe) { r.Inputs[1].Note.Path = ".axon/logs/run.md" }, "path"},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestRecipePath|TestValidateRecipes' -v`
Expected: FAIL — `undefined: validRecipeReadPath` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `internal/config/recipes.go`, replace `validRecipePath` with:

```go
// readableAxonFiles are the only .axon/ files a recipe input may read
// (FR-202). Reading is not writing, but the exception stays narrow: logs/,
// exports/, snapshots/ and dashboards/ hold material never written to be
// read back into a model call.
var readableAxonFiles = map[string]bool{
	".axon/review-queue.md":         true,
	".axon/review-queue-archive.md": true,
}

// validRecipeBasePath enforces the vault-relative note-path rules shared by
// recipe inputs and block sinks.
func validRecipeBasePath(p string) error {
	switch {
	case strings.TrimSpace(p) == "":
		return fmt.Errorf("path is required")
	case !strings.HasSuffix(p, ".md"):
		return fmt.Errorf("path %q must end in .md", p)
	case strings.HasPrefix(p, "/") || strings.Contains(p, ".."):
		return fmt.Errorf("path %q must be vault-relative without '..'", p)
	case strings.HasPrefix(p, ".trash/"):
		return fmt.Errorf("path %q may not target .trash/", p)
	}
	return nil
}

// validRecipeWritePath governs the block sink: no system files, ever
// (ADR-039 — the sink boundary does not move).
func validRecipeWritePath(p string) error {
	if err := validRecipeBasePath(p); err != nil {
		return err
	}
	if strings.HasPrefix(p, ".axon/") {
		return fmt.Errorf("path %q may not target .axon/ or .trash/", p)
	}
	return nil
}

// validRecipeReadPath governs note inputs: .axon/ stays closed except the
// allow-listed files (FR-202).
func validRecipeReadPath(p string) error {
	if err := validRecipeBasePath(p); err != nil {
		return err
	}
	if strings.HasPrefix(p, ".axon/") && !readableAxonFiles[p] {
		return fmt.Errorf("path %q is not a readable .axon/ file (only %s)", p, readableAxonList())
	}
	return nil
}

// readableAxonList renders the allow-list deterministically for error text.
func readableAxonList() string {
	names := make([]string, 0, len(readableAxonFiles))
	for n := range readableAxonFiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
```

Add `"sort"` to the file's imports. Then change the two call sites:

- input note (`in.Note.Path`): `validRecipePath` → `validRecipeReadPath`
- block sink (`b.Note`): `validRecipePath` → `validRecipeWritePath`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v && gofmt -l internal/config/`
Expected: PASS for the whole package (the flipped case now points at `.axon/logs/run.md`), `gofmt -l` silent.

- [ ] **Step 5: Commit**

```bash
git add internal/config/recipes.go internal/config/recipes_test.go
git commit -m "feat(config): split recipe path rules; recipes may read the review queue (FR-202)"
```

---

### Task 4: The two reader types + their validation, and the self-feeding refusal

**Files:**
- Modify: `internal/config/recipes.go` (`RecipeInput` struct, two new input structs, the reader-count block, a new post-loop check)
- Test: `internal/config/recipes_test.go` (append)

**Interfaces:**
- Consumes: `validRecipeReadPath` (Task 3), `readableAxonFiles` (Task 3).
- Produces: `config.RecipeStaleInput{OlderThanDays, Limit int}` and `config.RecipeSourcesInput{OlderThanDays, Limit int}`, reachable as `RecipeInput.StaleNotes *RecipeStaleInput` (`yaml:"stale_notes,omitempty"`) and `RecipeInput.Sources *RecipeSourcesInput` (`yaml:"sources,omitempty"`). Task 5 renders both.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/recipes_test.go`:

```go
func TestValidateRecipesNewReaders(t *testing.T) {
	base := func() Recipe {
		r := validRecipe()
		r.Inputs = []RecipeInput{
			{Name: "dormant", StaleNotes: &RecipeStaleInput{OlderThanDays: 365, Limit: 20}},
			{Name: "old", Sources: &RecipeSourcesInput{OlderThanDays: 180, Limit: 30}},
		}
		r.Prompt = "Stale: {{dormant}} Sources: {{old}}"
		return r
	}
	if err := validateRecipes(Profile{Recipes: []Recipe{base()}}); err != nil {
		t.Fatalf("valid new readers rejected: %v", err)
	}
	// Zero means "apply the default" for both, so an all-zero reader is legal.
	z := base()
	z.Inputs[0].StaleNotes = &RecipeStaleInput{}
	z.Inputs[1].Sources = &RecipeSourcesInput{}
	if err := validateRecipes(Profile{Recipes: []Recipe{z}}); err != nil {
		t.Fatalf("zero-valued new readers rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Recipe)
		want string
	}{
		{"stale days too big", func(r *Recipe) { r.Inputs[0].StaleNotes.OlderThanDays = 3651 }, "older_than_days"},
		{"stale days negative", func(r *Recipe) { r.Inputs[0].StaleNotes.OlderThanDays = -1 }, "older_than_days"},
		{"stale limit too big", func(r *Recipe) { r.Inputs[0].StaleNotes.Limit = 101 }, "limit"},
		{"sources days too big", func(r *Recipe) { r.Inputs[1].Sources.OlderThanDays = 3651 }, "older_than_days"},
		{"sources limit negative", func(r *Recipe) { r.Inputs[1].Sources.Limit = -1 }, "limit"},
		{"stale plus note is two readers", func(r *Recipe) {
			r.Inputs[0].Note = &RecipeNoteInput{Path: "a.md"}
		}, "exactly one"},
		{"sources plus search is two readers", func(r *Recipe) {
			r.Inputs[1].Search = &RecipeSearchInput{Query: "x"}
		}, "exactly one"},
	}
	for _, c := range cases {
		r := base()
		c.mut(&r)
		err := validateRecipes(Profile{Recipes: []Recipe{r}})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestValidateRecipesRefusesSelfFeedingReviewRecipe(t *testing.T) {
	// review sink + reads its own queue => the change-gate can never skip.
	bad := validRecipe()
	bad.Inputs[1].Note.Path = ".axon/review-queue.md"
	bad.Output = RecipeOutput{Review: &RecipeReviewSink{}}
	err := validateRecipes(Profile{Recipes: []Recipe{bad}})
	if err == nil || !strings.Contains(err.Error(), "own output") {
		t.Fatalf("self-feeding review recipe must be refused, got %v", err)
	}

	// Legal neighbour 1: review sink reading the ARCHIVE (a human must act
	// before anything lands there).
	okArchive := validRecipe()
	okArchive.Inputs[1].Note.Path = ".axon/review-queue-archive.md"
	okArchive.Output = RecipeOutput{Review: &RecipeReviewSink{}}
	if err := validateRecipes(Profile{Recipes: []Recipe{okArchive}}); err != nil {
		t.Fatalf("review sink reading the archive must be legal: %v", err)
	}

	// Legal neighbour 2: BLOCK sink reading the queue — D3's actual case.
	okBlock := validRecipe()
	okBlock.Inputs[1].Note.Path = ".axon/review-queue.md"
	if err := validateRecipes(Profile{Recipes: []Recipe{okBlock}}); err != nil {
		t.Fatalf("block sink reading the queue must be legal: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'NewReaders|SelfFeeding' -v`
Expected: FAIL — `undefined: RecipeStaleInput` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `internal/config/recipes.go`, extend `RecipeInput` and add the two structs:

```go
// RecipeInput names one reader; exactly one of the reader fields is set.
type RecipeInput struct {
	Name        string               `yaml:"name"`
	Note        *RecipeNoteInput     `yaml:"note,omitempty"`
	Search      *RecipeSearchInput   `yaml:"search,omitempty"`
	RecentNotes *RecipeRecentInput   `yaml:"recent_notes,omitempty"`
	StaleNotes  *RecipeStaleInput    `yaml:"stale_notes,omitempty"`
	Sources     *RecipeSourcesInput  `yaml:"sources,omitempty"`
}

// RecipeStaleInput renders notes untouched since a cutoff as
// "[[path]] (updated D)" — the inverse of RecipeRecentInput, with its own
// wider range because staleness is by definition about older material.
type RecipeStaleInput struct {
	OlderThanDays int `yaml:"older_than_days,omitempty"` // 0 → default 90
	Limit         int `yaml:"limit,omitempty"`           // 0 → default 20
}

// RecipeSourcesInput renders ingested sources as
// "[[note]] — url (fetched D, kind, status)".
type RecipeSourcesInput struct {
	OlderThanDays int `yaml:"older_than_days,omitempty"` // 0 → no age filter
	Limit         int `yaml:"limit,omitempty"`           // 0 → default 20
}
```

Add the bounds inside the per-input reader block, beside the `RecentNotes` case:

```go
			if in.StaleNotes != nil {
				readers++
				if in.StaleNotes.OlderThanDays < 0 || in.StaleNotes.OlderThanDays > maxRecipeAgeDays {
					return fmt.Errorf("%s input %q: older_than_days must be 0–%d", where, in.Name, maxRecipeAgeDays)
				}
				if in.StaleNotes.Limit < 0 || in.StaleNotes.Limit > 100 {
					return fmt.Errorf("%s input %q: limit must be 0–100", where, in.Name)
				}
			}
			if in.Sources != nil {
				readers++
				if in.Sources.OlderThanDays < 0 || in.Sources.OlderThanDays > maxRecipeAgeDays {
					return fmt.Errorf("%s input %q: older_than_days must be 0–%d", where, in.Name, maxRecipeAgeDays)
				}
				if in.Sources.Limit < 0 || in.Sources.Limit > 100 {
					return fmt.Errorf("%s input %q: limit must be 0–100", where, in.Name)
				}
			}
```

Update the reader-exclusivity error text to name all five:

```go
			if readers != 1 {
				return fmt.Errorf("%s input %q: exactly one of note, search, recent_notes, stale_notes, sources required", where, in.Name)
			}
```

Add the cap constant beside `maxRecipeInputs`:

```go
const (
	maxRecipeInputs  = 8
	maxRecipeAgeDays = 3650 // stale_notes / sources lookback ceiling (~10 years)
)
```

Finally, add the self-feeding check **after** the sink block resolves, still inside the per-recipe loop:

```go
		// A review-sink recipe reading its own queue would feed on its own
		// output: the input hash changes every run, so the change-gate can
		// never skip and a prompt recipe burns a model call per tick forever
		// (FR-202). The archive is safe — a human must accept or dismiss
		// before anything lands there.
		if r.Output.Review != nil {
			for _, in := range r.Inputs {
				if in.Note != nil && in.Note.Path == reviewQueueRecipePath {
					return fmt.Errorf("%s: a review-sink recipe may not read %s (its own output would be its next input); read %s instead",
						where, reviewQueueRecipePath, reviewArchiveRecipePath)
				}
			}
		}
```

with the two path constants declared beside `readableAxonFiles`, and the map rewritten in terms of them so the two cannot drift:

```go
const (
	reviewQueueRecipePath   = ".axon/review-queue.md"
	reviewArchiveRecipePath = ".axon/review-queue-archive.md"
)

var readableAxonFiles = map[string]bool{
	reviewQueueRecipePath:   true,
	reviewArchiveRecipePath: true,
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v && gofmt -l internal/config/ && go vet ./internal/config/`
Expected: PASS, `gofmt -l` silent, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/config/recipes.go internal/config/recipes_test.go
git commit -m "feat(config): stale_notes and sources readers; refuse self-feeding review recipes (FR-202/203)"
```

---

### Task 5: Render the two readers in the automation engine

**Files:**
- Modify: `internal/automations/recipe.go` (const block ~line 18, `resolveInputs` switch ~line 47)
- Test: `internal/automations/recipe_test.go` (append)

**Interfaces:**
- Consumes: `db.SourcesOlderThan` + `db.SourceInfo` (Task 1), `db.NotesUpdatedBefore(…, limit)` (Task 2), `config.RecipeStaleInput` / `config.RecipeSourcesInput` (Task 4), plus the existing `clipInput`, `stripExt`, `rc.now()`, `newRC(t, files)` and `seedNote(t, rc, path, title, updated)` helpers.
- Produces: nothing new exported. This is the last behaviour task.

- [ ] **Step 1: Write the failing test**

Append to `internal/automations/recipe_test.go`:

```go
// seedSourceRow inserts a note + source pair for the sources reader.
func seedSourceRow(t *testing.T, rc RunCtx, path, url, kind, fetchedAt, status string) {
	t.Helper()
	ctx := context.Background()
	var notePtr *int64
	if path != "" {
		id, err := db.InsertNote(ctx, rc.DB, db.NoteRow{Path: path, Title: path, Updated: "2026-01-01"})
		if err != nil {
			t.Fatal(err)
		}
		notePtr = &id
	}
	if _, err := db.UpsertSource(ctx, rc.DB, db.SourceRow{
		NoteID: notePtr, URL: url, Kind: kind, FetchedAt: fetchedAt, ContentHash: "h", Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeStaleNotesReader(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Dormant.md", "Dormant", "2020-01-01")
	seedNote(t, rc, "03-Resources/Fresh.md", "Fresh", "2026-06-27")

	def := testRecipe()
	def.Inputs = []config.RecipeInput{
		{Name: "dormant", StaleNotes: &config.RecipeStaleInput{OlderThanDays: 365, Limit: 10}},
	}
	def.Render = "{{dormant}}"
	vals, reason, err := RecipeRun{def: def}.resolveInputs(context.Background(), rc)
	if err != nil || reason != "" {
		t.Fatalf("resolve: reason=%q err=%v", reason, err)
	}
	got := vals["dormant"]
	if !strings.Contains(got, "[[03-Resources/Dormant]] (updated 2020-01-01)") {
		t.Fatalf("stale line shape wrong: %q", got)
	}
	if strings.Contains(got, "Fresh") {
		t.Fatalf("a note inside the window leaked into stale_notes: %q", got)
	}
}

func TestRecipeSourcesReader(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedSourceRow(t, rc, "03-Resources/Knowledge/Old.md", "https://old.example/a", "url", "2020-01-01T00:00:00Z", "ok")
	seedSourceRow(t, rc, "", "https://orphan.example/c", "pdf", "2020-02-01T00:00:00Z", "failed")

	def := testRecipe()
	def.Inputs = []config.RecipeInput{
		{Name: "old", Sources: &config.RecipeSourcesInput{OlderThanDays: 30, Limit: 10}},
	}
	def.Render = "{{old}}"
	vals, reason, err := RecipeRun{def: def}.resolveInputs(context.Background(), rc)
	if err != nil || reason != "" {
		t.Fatalf("resolve: reason=%q err=%v", reason, err)
	}
	got := vals["old"]
	if !strings.Contains(got, "[[03-Resources/Knowledge/Old]] — https://old.example/a (fetched 2020-01-01, url, ok)") {
		t.Fatalf("source line shape wrong: %q", got)
	}
	// A source with no note row renders without a wikilink.
	if !strings.Contains(got, "https://orphan.example/c (fetched 2020-02-01, pdf, failed)") ||
		strings.Contains(got, "[[]]") {
		t.Fatalf("orphan source line wrong: %q", got)
	}
}

// The D3-shaped regression docs/20 C2 asks for by name: composing over
// another automation's output plus the review queue must produce ONE intact
// managed block, with the nested markers it swallowed made inert.
func TestRecipeComposesOverAutomationOutputAndReviewQueue(t *testing.T) {
	actions := "# Actions\n\n<!-- axon:actions:start -->\n- [ ] overdue thing\n<!-- axon:actions:end -->\n"
	queue := "# Review queue\n\n- [ ] abc123 link \"A\" -> \"B\"\n"
	rc, _ := newRC(t, map[string]string{
		"01-Projects/Actions.md":  actions,
		".axon/review-queue.md":   queue,
		"01-Projects/Weekly.md":   "# Weekly\n\nMy own prose.\n",
	})
	def := config.Recipe{
		Name: "weekly-review", Purpose: "Weekly review.",
		Inputs: []config.RecipeInput{
			{Name: "board", Note: &config.RecipeNoteInput{Path: "01-Projects/Actions.md"}},
			{Name: "pending", Note: &config.RecipeNoteInput{Path: ".axon/review-queue.md"}},
		},
		Render: "Board:\n{{board}}\n\nPending:\n{{pending}}",
		Output: config.RecipeOutput{Block: &config.RecipeBlockSink{Note: "01-Projects/Weekly.md", Block: "weekly"}},
	}
	if _, err := (RecipeRun{def: def}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	n, err := rc.Vault.Read(context.Background(), "01-Projects/Weekly.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(n.Body, "<!-- axon:weekly:end -->"); got != 1 {
		t.Fatalf("want exactly 1 end marker for the recipe's own block, got %d:\n%s", got, n.Body)
	}
	if strings.Contains(n.Body, "<!-- axon:actions:end -->") {
		t.Fatalf("nested marker survived un-neutralized:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "overdue thing") || !strings.Contains(n.Body, "abc123") {
		t.Fatalf("composed content missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "My own prose.") {
		t.Fatalf("human prose clobbered:\n%s", n.Body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/automations/ -run 'StaleNotesReader|SourcesReader|ComposesOver' -v`
Expected: FAIL — `StaleNotes`/`Sources` unknown fields (compile error) once Task 4 has landed the types this is a runtime failure instead: the two reader tests fail with an empty rendered value.

- [ ] **Step 3: Write minimal implementation**

Add defaults to the const block in `internal/automations/recipe.go`:

```go
	recipeStaleDaysDef   = 90
	recipeStaleLimitDef  = 20
	recipeSourcesLimit   = 20
```

Add two cases to the `resolveInputs` switch, after the `in.RecentNotes` case:

```go
		case in.StaleNotes != nil:
			days := in.StaleNotes.OlderThanDays
			if days <= 0 {
				days = recipeStaleDaysDef
			}
			limit := in.StaleNotes.Limit
			if limit <= 0 {
				limit = recipeStaleLimitDef
			}
			before := rc.now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
			stamps, err := db.NotesUpdatedBefore(ctx, rc.DB, before, limit)
			if err != nil {
				return nil, "", err
			}
			var b strings.Builder
			for _, s := range stamps {
				fmt.Fprintf(&b, "[[%s]] (updated %s)\n", stripExt(s.Path), s.Updated)
			}
			vals[in.Name] = clipInput(strings.TrimSpace(b.String()))
		case in.Sources != nil:
			limit := in.Sources.Limit
			if limit <= 0 {
				limit = recipeSourcesLimit
			}
			// 0 means "no age filter" for sources, so an empty cutoff is a
			// legitimate value here — unlike stale_notes.
			cutoff := ""
			if d := in.Sources.OlderThanDays; d > 0 {
				cutoff = rc.now().UTC().AddDate(0, 0, -d).Format(time.RFC3339)
			}
			rows, err := db.SourcesOlderThan(ctx, rc.DB, cutoff, limit)
			if err != nil {
				return nil, "", err
			}
			var b strings.Builder
			for _, s := range rows {
				line := fmt.Sprintf("%s (fetched %s, %s, %s)", s.URL, dateOnly(s.FetchedAt), s.Kind, s.Status)
				if s.Path != "" {
					line = "[[" + stripExt(s.Path) + "]] — " + line
				}
				b.WriteString(line + "\n")
			}
			vals[in.Name] = clipInput(strings.TrimSpace(b.String()))
```

Add the helper at the bottom of the file and `"time"` to the imports:

```go
// dateOnly trims an RFC3339 stamp to its date for rendered source lines.
func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/automations/ -v && gofmt -l internal/automations/ && go vet ./internal/automations/`
Expected: PASS for the whole package, `gofmt -l` silent, vet clean.

- [ ] **Step 5: Full build + lint gate, then commit**

```bash
go build ./... && go test ./... && golangci-lint run
git add internal/automations/recipe.go internal/automations/recipe_test.go
git commit -m "feat(automations): render stale_notes and sources recipe readers (FR-203)"
```

Expected: whole test suite green. If `golangci-lint` flags `ST1018` on a literal, do **not** declare a second zero-width-space literal — reuse `vault.NeutralizeMarkers` (this slice adds no new marker handling).

---

### Task 6: Documentation, requirements, and the ADR amendment

**Files:**
- Modify: `docs/03-requirements.md` (FR-199's reader list; two new rows FR-202/FR-203; section stamp)
- Modify: `docs/02-architecture.md` (ADR-039 Status gains a dated amendment)
- Modify: `docs/04-data-model-and-config.md` (recipe config reference)
- Modify: `docs/06-component-automation-engine.md`, `docs/AUTOMATIONS.md`
- Modify: `axon.config.example.yaml` (commented recipe sample)
- Modify: `docs/20-roadmap-ai-os.md` (C2: P1+P2 shipped, P3 remains), `docs/19-roadmap-second-brain.md` (E2 qualified)
- Modify: `CHANGELOG.md` (`[Unreleased]`)
- Modify: `CLAUDE.md` (FR range → FR-203)

**Interfaces:**
- Consumes: the final shapes from Tasks 1–5. No code.
- Produces: nothing consumed by a later task.

- [ ] **Step 1: Add the two FR rows to `docs/03-requirements.md`**

Below FR-201, in the same table, with the same voice as its neighbours:

```markdown
| FR-202 | S | **Recipe read/write path split (ADR-039 amended).** The single `validRecipePath` becomes a shared base (`.md`, vault-relative, no `..`, no leading `/`, never `.trash/`) plus two rules: `validRecipeWritePath` (block sink — `.axon/` refused, today's behaviour verbatim) and `validRecipeReadPath` (note inputs — `.axon/` refused **except** an allow-list of exactly `.axon/review-queue.md` and `.axon/review-queue-archive.md`). Reading is not writing: the review queue is plain Markdown the dashboard already serves, and a recipe composing a weekly review needs it. The sink boundary does not move — no recipe writes a system file, and there is still no third sink. Additionally, a `review {}`-sink recipe may **not** read `.axon/review-queue.md`: its own output would be its next input, so the change-gate could never skip and a `prompt` recipe would burn one model call per tick forever; the archive stays legal (a human must accept or dismiss before anything lands there), as does a `block`-sink recipe reading the queue. |
| FR-203 | S | **`stale_notes` and `sources` readers (ADR-039 amended).** The recipe reader vocabulary goes three → five, all still zero-Claude: `stale_notes {older_than_days 0–3650 (0 → 90), limit 0–100 (0 → 20)}` → `[[path]] (updated DATE)` for notes untouched before the cutoff — a sibling of `recent_notes`, not a mode of it, so that reader keeps its honest 0–90 cap; and `sources {older_than_days 0–3650 (0 → no age filter), limit 0–100 (0 → 20)}` → `[[note]] — url (fetched DATE, kind, status)` from the `sources` table, rendering `status` and never filtering on it (a failed or redacted source is what a freshness recipe most wants to surface); a source whose note row is gone renders without the wikilink. Seams: `db.SourcesOlderThan` (a `SourceInfo` projection over `sources LEFT JOIN notes`, RFC3339 lexicographic cutoff — the `CountSourcesSince` precedent) and a `limit` parameter on the existing `db.NotesUpdatedBefore`. Both readers clip through the existing input cap; no new sink, no migration. |
```

Then update FR-199's reader list in place — `recent_notes {lookback_days 1–90, limit ≤ 100}` becomes `recent_notes {lookback_days 1–90, limit ≤ 100}, stale_notes {older_than_days ≤ 3650, limit ≤ 100}, sources {older_than_days ≤ 3650, limit ≤ 100}` — and change FR-199's "`.axon/`/`.trash/`" sentence to say the **block sink** rejects them (FR-202 splits the read rule out).

- [ ] **Step 2: Amend ADR-039 in `docs/02-architecture.md`**

Append to ADR-039's **Status** line (do not renumber, do not add ADR-040):

```markdown
**Amended 2026-08-21** (FR-202, FR-203; spec
`docs/superpowers/specs/2026-08-21-recipe-vocabulary-v2-design.md`;
graduates docs/20 C2 P1+P2): the recipe path rule is **two** rules — reading
is not writing, so note inputs may read an allow-listed pair of `.axon/`
files (`review-queue.md`, `review-queue-archive.md`) while the block sink
still refuses `.axon/` entirely; the reader vocabulary is **five**, not
three (`stale_notes`, `sources`); and a `review {}`-sink recipe may not read
the review queue, whose output would otherwise be its own next input. No
architectural boundary moves — sinks, the chokepoint, config-not-vault, the
registry story and "anything needing a new sink is a Go automation" all
stand.
```

- [ ] **Step 3: Update the config reference and the example**

In `docs/04-data-model-and-config.md`, extend the `recipes:` reference with the two readers and their ranges, and correct the line that currently reads `.axon//.trash/ targets refused` to say **block-sink** targets are refused while inputs may read the two allow-listed queue files.

In `axon.config.example.yaml`, extend the commented recipe sample with a second, fully commented recipe:

```yaml
  #   - name: source-freshness
  #     purpose: "Weekly advisory on research sources that have gone stale."
  #     inputs:
  #       - name: old
  #         sources: {older_than_days: 180, limit: 30}   # "[[note]] — url (fetched D, kind, status)"
  #       - name: dormant
  #         stale_notes: {older_than_days: 365, limit: 20} # "[[path]] (updated D)"
  #     render: |
  #       Sources not re-fetched in 180 days ({{today}}):
  #       {{old}}
  #
  #       Notes untouched in a year:
  #       {{dormant}}
  #     output:
  #       block: {note: "03-Resources/Source Freshness.md", block: "freshness"}
```

Verify the example still loads: `go run ./cmd/axon config validate --config axon.config.example.yaml` (expected: valid; the sample is commented out, so this proves the file still parses).

- [ ] **Step 4: Update the component docs and both roadmaps**

- `docs/06-component-automation-engine.md` and `docs/AUTOMATIONS.md`: extend the recipe reader list to five and note the `.axon/` read allow-list.
- `docs/20-roadmap-ai-os.md` C2: mark **Priority 1 and Priority 2 SHIPPED 2026-08-21 (FR-202/FR-203)**; leave Priority 3 as the open remainder and keep the "prefer `docs/19` E1 first" ordering. Resolve the section's open decisions with what was chosen (allow-list not wholesale; sibling `stale_notes`; render status, no filter; the 90-day cap lifts only for the age-selecting path).
- `docs/19-roadmap-second-brain.md` E2: **qualify, do not reverse**, the "confirmed Go" note — E2's aggregate-digest half is now expressible as a recipe; its per-note advisory half still needs a fan-out sink and stays Go.
- Also correct the `docs/20` sequencing sketch, which names `docs/19` F2 as a remaining recipe candidate: F2 needs a new review-queue kind plus a note `Create`, so it is a Go slice by ADR-039's own boundary.

- [ ] **Step 5: CHANGELOG, CLAUDE.md, and the final gate**

Add to `CHANGELOG.md` under `[Unreleased]` → `### Added`:

```markdown
- Recipes can read the review queue, and reach note staleness and ingested sources: a read/write split on the recipe path rule with a two-file `.axon/` read allow-list, plus `stale_notes` and `sources` readers (FR-202, FR-203; ADR-039 amended).
```

In `CLAUDE.md`, update the FR range from FR-201 to FR-203 in the header line that states current maxima.

Then:

```bash
gofmt -l . && go build ./... && go test ./... && golangci-lint run
git add -A
git commit -m "docs: FR-202/FR-203, ADR-039 amendment, config reference and roadmaps"
```

Expected: everything green, nothing unformatted.

---

### Task 7: Live smoke in an isolated environment

**Files:**
- Create: `<scratchpad>/vocab2-smoke/` (throwaway — never committed)

**Interfaces:**
- Consumes: the built `axon` binary and every change from Tasks 1–6.
- Produces: a verification record for the commit message / completion report.

- [ ] **Step 1: Build the binary with the SPA embedded**

```bash
cd web && npm run build && cd ..
go build -o /tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/vocab2-smoke/axon ./cmd/axon
```

The `cd web` is a cwd-persistence trap — `cd ..` before `go build` or the build runs in the wrong directory.

- [ ] **Step 2: Build the smoke config from the shipped example, then re-port it**

Never hand-roll a smoke config — validation requires the full field set. Copy and immediately re-port:

```bash
S=/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/vocab2-smoke
mkdir -p "$S/home" "$S/vault/.axon" "$S/vault/01-Projects" "$S/vault/03-Resources"
cp axon.config.example.yaml "$S/home/config.yaml"
sed -i '' 's/port: 7777/port: 7799/g' "$S/home/config.yaml"
grep -rn 7777 "$S" && echo "STOP: 7777 still present" || echo "port clean"
```

`port: 7777` appears in **both** the personal and work profiles — `7777` is the live daemon's port and binding it kills the real daemon's start. Re-check with the `grep` above before any `axon start`.

- [ ] **Step 3: Point the config at the smoke vault and add two real recipes**

Edit `$S/home/config.yaml`: set the active profile's `vault` to `$S/vault`, its `data_dir` to `$S/home/profiles/personal`, then add an uncommented `recipes:` list with (a) a `render` recipe reading `01-Projects/Actions.md` + `.axon/review-queue.md` into `block: {note: "01-Projects/Weekly.md", block: "weekly"}`, and (b) a `render` recipe using `sources: {older_than_days: 1, limit: 10}` + `stale_notes: {older_than_days: 1, limit: 10}` into `block: {note: "03-Resources/Freshness.md", block: "freshness"}`. Give each an `automations.<name>` entry with `enabled: true`.

Seed the vault:

```bash
printf '# Actions\n\n<!-- axon:actions:start -->\n- [ ] overdue thing\n<!-- axon:actions:end -->\n' > "$S/vault/01-Projects/Actions.md"
printf '# Review queue\n\n- [ ] abc123 link "A" -> "B"\n' > "$S/vault/.axon/review-queue.md"
printf '# Weekly\n\nMy own prose survives.\n' > "$S/vault/01-Projects/Weekly.md"
```

- [ ] **Step 4: Run both recipes and check the six properties**

```bash
export AXON_HOME="$S/home"
"$S/axon" run weekly-review --config "$S/home/config.yaml"
"$S/axon" run source-freshness --config "$S/home/config.yaml"
cat "$S/vault/01-Projects/Weekly.md"
```

Verify: (1) the block was written; (2) `grep -c 'axon:weekly:end'` is exactly **1**; (3) the nested `axon:actions:end` marker is inert (zero-width-space separated, not a literal end marker); (4) `My own prose survives.` is still there; (5) a second `axon run weekly-review` reports a change-gate skip; (6) editing `.axon/review-queue.md` re-arms it.

- [ ] **Step 5: Check the refusals and the daemon path**

```bash
# Validation must refuse a non-allow-listed .axon/ read and a self-feeding recipe.
# Temporarily point an input at .axon/logs/run.md, then run:
"$S/axon" config validate --config "$S/home/config.yaml"   # expect: refusal naming the path
# Then restore, set that recipe's sink to review: {} while it reads the queue:
"$S/axon" config validate --config "$S/home/config.yaml"   # expect: "own output would be its next input"
# Restore the working config, then confirm the daemon schedules both recipes:
"$S/axon" start --config "$S/home/config.yaml"
```

For `axon start`, confirm the log contains **`daemon running`** — not merely `scheduled …`. A bind failure prints the banner and schedules everything before dying, which reads as success if you only check for `scheduled`. Then `axon doctor` should report both recipes valid, and `axon automations` should list them with their own purposes. SIGTERM and confirm a clean exit with the pidfile removed.

- [ ] **Step 6: Record the result and clean up**

Delete `$S`. Note the verified properties in the completion report; nothing from the smoke directory is committed.

---

## Self-Review

**Spec coverage:** FR-202's path split → Task 3; its `.axon/` allow-list → Task 3; its self-feeding refusal → Task 4. FR-203's `stale_notes` → Tasks 2 (db) + 4 (config) + 5 (render); its `sources` → Tasks 1 (db) + 4 (config) + 5 (render); the `SourceInfo` projection and the RFC3339 compare → Task 1; the `NotesUpdatedBefore` limit → Task 2. The D3-shaped marker regression the spec names explicitly → Task 5. ADR-039 amendment, docs, roadmaps, CHANGELOG → Task 6. Live smoke including the 7777 guard → Task 7. Out-of-scope items (fan-out sink, orphan readers, wholesale `.axon/`, status filter) appear in no task, correctly.

**Type consistency:** `SourceInfo{Path, URL, Kind, FetchedAt, Status}` defined in Task 1 and consumed with those exact fields in Task 5. `SourcesOlderThan(ctx, q, beforeTS, limit)` defined in Task 1, called with `(ctx, rc.DB, cutoff, limit)` in Task 5. `NotesUpdatedBefore(ctx, q, beforeDate, limit)` changed in Task 2, called with a real limit in Task 5 and with `0` from `actions-review`. `RecipeStaleInput`/`RecipeSourcesInput` with `OlderThanDays`/`Limit` defined in Task 4 and used with those names in Task 5's tests and switch. `validRecipeReadPath`/`validRecipeWritePath`/`readableAxonFiles`/`maxRecipeAgeDays` defined in Tasks 3–4 and referenced nowhere later by a different name.

**Ordering note:** Task 5's Step 2 depends on Task 4 having landed the config types; if executed strictly in order the failure there is a runtime one (empty rendered values), not a compile error. That is called out in the step itself.
