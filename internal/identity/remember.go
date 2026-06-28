package identity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/vault"
)

// Entry is a durable memory record to append.
type Entry struct {
	Text   string // the fact/decision/lesson (single line)
	Kind   string // optional: decision | lesson | preference
	Source string // optional provenance, e.g. "session" or an ADR id
	Date   string // YYYY-MM-DD; defaults to today (UTC) if empty
}

// Remember prepends a dated entry to the axon:memory managed block in MEMORY.md
// (newest first), wikilink-safe and never touching human prose (cardinal rule
// 2). It is a read-modify-write of the managed block via vault.Patch: it reads
// the current block, prepends the new line and re-writes only that block. The
// layer is created first if absent. It makes no model call.
func Remember(ctx context.Context, v *vault.FS, e Entry) (string, error) {
	if strings.TrimSpace(e.Text) == "" {
		return "", fmt.Errorf("memory entry text is empty")
	}
	if e.Date == "" {
		e.Date = time.Now().UTC().Format("2006-01-02")
	}
	// Ensure the layer exists so the managed block is present to patch.
	if !Present(v) {
		if _, err := Generate(v, Values{Date: e.Date}); err != nil {
			return "", err
		}
	}
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return "", err
	}
	existing := parseEntries(extractBlock(body, MemoryBlock))
	line := FormatEntry(e)
	all := append([]string{line}, existing...) // newest first
	if err := v.Patch(ctx, MemoryPath, MemoryBlock, strings.Join(all, "\n")); err != nil {
		return "", err
	}
	return line, nil
}

// FormatEntry renders a single MEMORY bullet: "- DATE — text [kind] (source: …)".
func FormatEntry(e Entry) string {
	// Collapse internal newlines so one entry stays one line (the block is parsed
	// line-by-line and injected verbatim).
	text := strings.Join(strings.Fields(e.Text), " ")
	var b strings.Builder
	fmt.Fprintf(&b, "- %s — %s", e.Date, text)
	if k := strings.TrimSpace(e.Kind); k != "" {
		fmt.Fprintf(&b, " [%s]", k)
	}
	if s := strings.TrimSpace(e.Source); s != "" {
		fmt.Fprintf(&b, " (source: %s)", s)
	}
	return b.String()
}

// CountEntries returns how many memory entries are stored (for distillation
// gating and the dashboard).
func CountEntries(ctx context.Context, v *vault.FS) (int, error) {
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return 0, err
	}
	return len(parseEntries(extractBlock(body, MemoryBlock))), nil
}
