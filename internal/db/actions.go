package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Action is one row of the derived actions index (ADR-033). A re-derivable
// projection of a checkbox line; reindex delete-all+inserts these from Markdown,
// so they are disposable (S9).
type Action struct {
	ID         int64    `json:"id"`
	Hash       string   `json:"hash"`
	SourcePath string   `json:"source_path"`
	LineNo     int      `json:"line_no"`
	Section    string   `json:"section"`
	Text       string   `json:"text"`
	Raw        string   `json:"raw"`
	State      string   `json:"state"`
	Checkbox   string   `json:"checkbox"`
	Priority   string   `json:"priority"`
	Due        string   `json:"due"`
	Scheduled  string   `json:"scheduled"`
	Start      string   `json:"start"`
	DoneDate   string   `json:"done_date"`
	Project    string   `json:"project"`
	Contexts   []string `json:"contexts"`
	Tags       []string `json:"tags"`
	Archived   bool     `json:"archived"`
	Updated    string   `json:"updated"`
}

// ListActionsOpts filters ListActions. Zero value = all non-archived actions.
type ListActionsOpts struct {
	SourcePath string // "" = any
	State      string // "" = any
	IncludeAll bool   // false = exclude archived rows
}

// ReplaceActions rebuilds the whole actions table: delete every row then insert
// in caller order (reindex passes them by source_path then line_no) so the
// projection stays in step with the vault and reindex is row-for-row
// deterministic (S9).
func ReplaceActions(ctx context.Context, q Execer, as []Action) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM actions;"); err != nil {
		return fmt.Errorf("clear actions: %w", err)
	}
	for _, a := range as {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO actions
			   (hash, source_path, line_no, section, text, raw, state, checkbox,
			    priority, due, scheduled, start, done_date, project, contexts, tags,
			    archived, updated)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);`,
			a.Hash, a.SourcePath, a.LineNo, nullify(a.Section), a.Text, a.Raw,
			a.State, a.Checkbox, nullify(a.Priority), nullify(a.Due),
			nullify(a.Scheduled), nullify(a.Start), nullify(a.DoneDate),
			nullify(a.Project), marshalStrings(a.Contexts), marshalStrings(a.Tags),
			boolInt(a.Archived), a.Updated); err != nil {
			return fmt.Errorf("insert action %q: %w", a.Text, err)
		}
	}
	return nil
}

// ListActions returns actions filtered by opts, ordered by source_path, line_no.
func ListActions(ctx context.Context, q Queryer2, opts ListActionsOpts) ([]Action, error) {
	query := `SELECT id, hash, source_path, line_no, COALESCE(section,''), text, raw,
	                 state, checkbox, COALESCE(priority,''), COALESCE(due,''),
	                 COALESCE(scheduled,''), COALESCE(start,''), COALESCE(done_date,''),
	                 COALESCE(project,''), COALESCE(contexts,'[]'), COALESCE(tags,'[]'),
	                 archived, updated
	            FROM actions`
	var conds []string
	var args []any
	if opts.SourcePath != "" {
		conds = append(conds, "source_path = ?")
		args = append(args, opts.SourcePath)
	}
	if opts.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, opts.State)
	}
	if !opts.IncludeAll {
		conds = append(conds, "archived = 0")
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY source_path, line_no;"
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

// MarkActionDone flips one derived row to done in place (the vault is already
// updated; this keeps the disposable index in step until the next reindex, which
// reproduces the same row from the now-[x] source line — S9-consistent). Returns
// rows affected (0 = unknown/already-done hash).
func MarkActionDone(ctx context.Context, q Execer, hash, doneDate string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE actions SET state='done', checkbox='x', done_date=? WHERE hash=? AND state='open';`,
		doneDate, hash)
	if err != nil {
		return 0, fmt.Errorf("mark action done: %w", err)
	}
	return res.RowsAffected()
}

// ActionStateCounts reports date-independent counts for doctor.
func ActionStateCounts(ctx context.Context, q Queryer) (total, open, done, cancelled, archived int, err error) {
	var t, o, d, c, a sql.NullInt64
	err = q.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        SUM(CASE WHEN state='open' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN state='done' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN state='cancelled' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END)
		   FROM actions;`).Scan(&t, &o, &d, &c, &a)
	if err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("action counts: %w", err)
	}
	return int(t.Int64), int(o.Int64), int(d.Int64), int(c.Int64), int(a.Int64), nil
}

func scanActions(rows *sql.Rows) ([]Action, error) {
	var out []Action
	for rows.Next() {
		var a Action
		var archived int
		var ctxJSON, tagJSON string
		if err := rows.Scan(&a.ID, &a.Hash, &a.SourcePath, &a.LineNo, &a.Section,
			&a.Text, &a.Raw, &a.State, &a.Checkbox, &a.Priority, &a.Due,
			&a.Scheduled, &a.Start, &a.DoneDate, &a.Project, &ctxJSON, &tagJSON,
			&archived, &a.Updated); err != nil {
			return nil, err
		}
		a.Archived = archived != 0
		a.Contexts = unmarshalStrings(ctxJSON)
		a.Tags = unmarshalStrings(tagJSON)
		out = append(out, a)
	}
	return out, rows.Err()
}

// marshalStrings/unmarshalStrings store a []string as a JSON array column
// (mirrors how notes.go stores tags inline).
func marshalStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalStrings(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
