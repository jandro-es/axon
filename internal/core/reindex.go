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

// Reindex rebuilds the derived notes mirror and link graph entirely from the
// Markdown vault (ADR-006): the database is disposable and reconstructable, so
// this clears the derived tables and repopulates them. It runs in a single
// transaction so a failure leaves the previous index intact.
//
// Embedding existing notes (vectors/FTS) is intentionally out of scope here —
// that arrives with the Ollama provider in Phase 2. This builds the operational
// state that Phase 1 owns.
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
	for _, p := range paths {
		n, err := v.Read(ctx, p)
		if err != nil {
			return res, err
		}
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

	if err := db.ResetDerived(ctx, tx); err != nil {
		return res, err
	}

	// First pass: insert notes, building resolution maps as we go.
	byRel := make(map[string]int64, len(notes)) // RelNoExt -> id
	byBase := make(map[string]int64, len(notes))
	ids := make([]int64, len(notes))
	for i, n := range notes {
		id, err := db.InsertNote(ctx, tx, n.row)
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
