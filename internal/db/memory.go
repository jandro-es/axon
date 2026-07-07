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
