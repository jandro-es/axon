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
	Kind   string // optional: fact | decision | lesson | preference
	Source string // optional provenance, e.g. "session", "reconcile", or a [[wikilink]]
	Date   string // YYYY-MM-DD; defaults to today (UTC) if empty
	// ValidFrom, when set, is the fact's leading date (valid_from). It takes
	// precedence over Date for the emitted line; existing callers that set only
	// Date are unaffected.
	ValidFrom string
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

// Reconcile supersedes an existing memory entry with a new one inside the
// axon:memory managed block (cardinal rule 2). It tombstones the first
// non-struck line whose text contains oldText — striking it and closing its
// validity interval as " (until DATE; superseded by \"newText\")" — and
// prepends a fresh open entry for newText (source: reconcile, valid_from DATE),
// then re-writes only the block via vault.Patch. If no line matches oldText
// (e.g. it was compacted since the proposal), the new entry is still prepended
// and matched is false so the caller can report it. Makes no model call. Params
// are oldText/newText to avoid shadowing `new`.
func Reconcile(ctx context.Context, v *vault.FS, oldText, newText, date string) (bool, error) {
	if strings.TrimSpace(newText) == "" {
		return false, fmt.Errorf("reconcile: new entry text is empty")
	}
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	if !Present(v) {
		if _, err := Generate(v, Values{Date: date}); err != nil {
			return false, err
		}
	}
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return false, err
	}
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
	if err := v.Patch(ctx, MemoryPath, MemoryBlock, strings.Join(all, "\n")); err != nil {
		return false, err
	}
	return matched, nil
}

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

// CountEntries returns how many memory entries are stored (for distillation
// gating and the dashboard).
func CountEntries(ctx context.Context, v *vault.FS) (int, error) {
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return 0, err
	}
	return len(parseEntries(extractBlock(body, MemoryBlock))), nil
}
