package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/vault"
)

// ReindexResult summarises a reindex for the caller and the init summary.
type ReindexResult struct {
	Notes          int
	Links          int
	BrokenWikilink int // wikilink/embed edges that don't resolve to a known note
	Rechunked      int // notes whose chunks/FTS rows were rebuilt from the body
}

// Reindex reconstructs the derived notes mirror, link graph AND the search
// index from the Markdown vault (ADR-006: SQLite is derived and disposable, so
// this must fully rebuild it). It *converges* the notes table by path rather
// than wiping it: notes that persist keep their row id, and notes whose body
// hash is unchanged keep their existing chunks and vectors (including
// source-anchored chunks written by ingestion). Notes whose body changed — or
// that have no chunks at all, as after deleting the database — get their
// chunks and FTS rows rebuilt from the body. Notes whose files are gone are
// deleted (chunks, vectors and FTS rows included); links are fully rebuilt.
// Runs in a single transaction so a failure leaves the previous index intact.
//
// Re-embedding is separate (see ReembedPending): a plain reindex never requires
// Ollama and never discards existing vectors for unchanged notes; rebuilt
// chunks are left vector-pending for the next embedding pass.
func Reindex(ctx context.Context, v *vault.FS, sqlDB *sql.DB) (ReindexResult, error) {
	var res ReindexResult

	paths, err := v.List(ctx)
	if err != nil {
		return res, err
	}

	// Read and parse every note up front (Phase 1 vaults are small), so we can
	// resolve link targets against the full set of note ids in a second pass.
	type parsed struct {
		row   db.NoteRow
		links []vault.Link
		body  string
	}
	notes := make([]*parsed, 0, len(paths))
	present := make(map[string]bool, len(paths))
	for _, p := range paths {
		n, err := v.Read(ctx, p)
		if err != nil {
			return res, err
		}
		present[p] = true
		notes = append(notes, &parsed{
			row:   noteRow(p, n),
			links: vault.ParseLinks(n.Body),
			body:  n.Body,
		})
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin reindex tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Snapshot each note's prior hash and chunk count BEFORE upserting, so the
	// rechunk decision below compares against the previous index state.
	states, err := db.NoteIndexStates(ctx, tx)
	if err != nil {
		return res, err
	}

	// Drop notes whose files no longer exist (chunks/vectors/FTS included).
	for path, st := range states {
		if !present[path] {
			if err := db.DeleteNote(ctx, tx, st.ID); err != nil {
				return res, err
			}
		}
	}

	// Links are rebuilt from scratch each time; they anchor nothing.
	if err := db.ClearLinks(ctx, tx); err != nil {
		return res, err
	}

	// First pass: upsert notes by path (stable ids), building resolution maps.
	byRel := make(map[string]int64, len(notes)) // RelNoExt -> id
	byBase := make(map[string]int64, len(notes))
	ids := make([]int64, len(notes))
	for i, n := range notes {
		id, err := db.UpsertNote(ctx, tx, n.row)
		if err != nil {
			return res, err
		}
		ids[i] = id

		// Rebuild the note's chunks + FTS rows when the body changed or when
		// no chunks exist (fresh database). Unchanged notes keep their chunks
		// and vectors untouched — including ingestion's source-anchored ones.
		prev, had := states[n.row.Path]
		if !had || prev.ContentHash != n.row.ContentHash || prev.Chunks == 0 {
			rechunked, err := rechunkNote(ctx, tx, id, n.body)
			if err != nil {
				return res, err
			}
			if rechunked {
				res.Rechunked++
			}
		}

		// Keys are lowercased: Obsidian resolves links case-insensitively, so
		// [[beta]] must find Beta.md (resolveTarget lowercases lookups to match).
		byRel[strings.ToLower(vault.RelNoExt(n.row.Path))] = id
		// For ambiguous basenames, first occurrence wins (deterministic by
		// sorted path order); bare links to a duplicated basename are rare.
		if base := strings.ToLower(vault.BaseNoExt(n.row.Path)); base != "" {
			if _, exists := byBase[base]; !exists {
				byBase[base] = id
			}
		}
	}

	// Second pass: insert resolved link edges.
	for i, n := range notes {
		for _, l := range n.links {
			row := db.LinkRow{SrcNoteID: ids[i], DstPath: l.Target, Kind: string(l.Kind)}
			if l.Kind != vault.KindTag {
				if dst, ok := resolveTarget(l.Target, byRel, byBase); ok {
					row.DstNoteID = &dst
				}
			}
			if err := db.InsertLink(ctx, tx, row); err != nil {
				return res, err
			}
		}
	}

	// Rebuild the derived memory fact index from the axon:memory block (ADR-028).
	// Read-only Markdown→DB: this NEVER writes to the vault (S9).
	if err := rebuildMemoryFacts(ctx, v, tx); err != nil {
		return res, err
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit reindex: %w", err)
	}

	if res.Notes, err = db.CountNotes(ctx, sqlDB); err != nil {
		return res, err
	}
	if res.Links, err = db.CountLinks(ctx, sqlDB); err != nil {
		return res, err
	}
	if res.BrokenWikilink, err = db.CountBrokenWikilinks(ctx, sqlDB); err != nil {
		return res, err
	}
	return res, nil
}

// rechunkNote replaces a note's chunks and FTS rows with fresh chunks of its
// body (vectors are left pending for ReembedPending). Reports whether any
// chunk rows were written — an empty body yields none and is not "rechunked".
func rechunkNote(ctx context.Context, tx db.DBTX, noteID int64, body string) (bool, error) {
	if err := db.DeleteChunksForNote(ctx, tx, noteID); err != nil {
		return false, err
	}
	chunks := ingestion.DefaultChunks(body)
	for _, c := range chunks {
		cid, err := db.InsertChunk(ctx, tx, db.ChunkRow{
			NoteID: &noteID, Ordinal: c.Ordinal, Text: c.Text,
			TokenCount: c.TokenCount, ContentHash: c.ContentHash,
		})
		if err != nil {
			return false, err
		}
		if err := db.InsertChunkFTS(ctx, tx, cid, c.Text); err != nil {
			return false, err
		}
	}
	return len(chunks) > 0, nil
}

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

// resolveTarget maps a wikilink target to a note id using the path/basename
// resolution maps, mirroring how Obsidian resolves links (case-insensitively —
// the maps are keyed lowercase).
func resolveTarget(target string, byRel, byBase map[string]int64) (int64, bool) {
	key, isPath := vault.TargetKey(target)
	key = strings.ToLower(key)
	if isPath {
		id, ok := byRel[key]
		return id, ok
	}
	id, ok := byBase[key]
	return id, ok
}

// noteRow builds a db.NoteRow from a parsed note, deriving title/hash/word-count.
func noteRow(path string, n *vault.Note) db.NoteRow {
	title := n.FrontmatterString("title")
	if title == "" {
		title = vault.BaseNoExt(path)
	}
	updated := n.FrontmatterString("updated")
	if updated == "" && !n.Updated.IsZero() {
		updated = n.Updated.UTC().Format("2006-01-02")
	}
	return db.NoteRow{
		Path:        path,
		Title:       title,
		Type:        n.FrontmatterString("type"),
		Status:      n.FrontmatterString("status"),
		Tags:        n.Tags(),
		ContentHash: config.ContentHash(n.Body),
		WordCount:   len(strings.Fields(n.Body)),
		Created:     n.FrontmatterString("created"),
		Updated:     updated,
		LastIndexed: nowStamp(),
	}
}
