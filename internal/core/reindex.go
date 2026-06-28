package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/vault"
)

// ReindexResult summarises a reindex for the caller and the init summary.
type ReindexResult struct {
	Notes          int
	Links          int
	BrokenWikilink int // wikilink/embed edges that don't resolve to a known note
}

// Reindex reconstructs the derived notes mirror and link graph from the Markdown
// vault (ADR-006). It *converges* the notes table by path rather than wiping it:
// notes that persist keep their row id, so the chunks and vectors anchored to
// them (written by ingestion) survive a routine reindex. Notes whose files are
// gone are deleted (cascading their chunks); links are fully rebuilt. Runs in a
// single transaction so a failure leaves the previous index intact.
//
// Re-embedding is separate (see ReembedPending): a plain reindex never requires
// Ollama and never discards existing vectors for unchanged notes.
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
		})
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin reindex tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Drop notes whose files no longer exist (cascades their chunks/vectors).
	existingIDs, err := db.NotePathIDs(ctx, tx)
	if err != nil {
		return res, err
	}
	for path, id := range existingIDs {
		if !present[path] {
			if err := db.DeleteNote(ctx, tx, id); err != nil {
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
		byRel[vault.RelNoExt(n.row.Path)] = id
		// For ambiguous basenames, first occurrence wins (deterministic by
		// sorted path order); bare links to a duplicated basename are rare.
		if base := vault.BaseNoExt(n.row.Path); base != "" {
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

// resolveTarget maps a wikilink target to a note id using the path/basename
// resolution maps, mirroring how Obsidian resolves links.
func resolveTarget(target string, byRel, byBase map[string]int64) (int64, bool) {
	key, isPath := vault.TargetKey(target)
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
