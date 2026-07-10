package vault

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/jandro-es/axon/internal/actions"
)

// ErrActionNotFound is returned (nothing written) when no OPEN checkbox line in
// the note has the given identity hash — a stale/unknown hash. The dashboard
// maps it to 409.
var ErrActionNotFound = errors.New("no matching open action")

// CompleteAction toggles the single open checkbox line whose T1 identity hash
// equals lineHash: [ ]→[x] and appends " ✅ <date>". Byte-precise and atomic;
// human prose around the line is untouched. It is the ONE vault mutation that
// edits a human-authored line rather than a managed block (ADR-034) —
// user-initiated only, never model/agent-driven. Returns ErrActionNotFound
// (nothing written) when no open line matches.
func (v *FS) CompleteAction(ctx context.Context, path, lineHash, date string) error {
	abs, err := v.safeAbs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	fm, body := splitFrontmatter(string(data))
	// Reuse T1's Extract so we match the EXACT line the index hashed (same
	// fenced-code / axon:actions-block skips, same body-relative LineNo).
	for _, a := range actions.Extract(path, body, false) {
		if a.State != actions.StateOpen || a.Hash() != lineHash {
			continue
		}
		lines := strings.Split(body, "\n")
		newLine, ok := actions.Complete(lines[a.LineNo], date)
		if !ok {
			return ErrActionNotFound
		}
		lines[a.LineNo] = newLine
		return v.writeRaw(path, reassemble(fm, strings.Join(lines, "\n")))
	}
	return ErrActionNotFound
}

// TagAction appends " #<tag>" to the FIRST open checkbox line in the note whose
// T1 text matches actionText, if the tag isn't already present. Byte-precise +
// atomic; additive (never removes/reorders). Returns ErrActionNotFound (nothing
// written) if no open line matches. Like CompleteAction (ADR-034) it edits a
// human-authored checkbox line, user-initiated via the review queue only.
func (v *FS) TagAction(ctx context.Context, path, actionText, tag string) error {
	abs, err := v.safeAbs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	fm, body := splitFrontmatter(string(data))
	lines := strings.Split(body, "\n")
	for _, a := range actions.Extract(path, body, false) {
		if a.State != actions.StateOpen || a.Text != actionText {
			continue
		}
		if strings.Contains(lines[a.LineNo], "#"+tag) {
			return nil // already tagged — idempotent
		}
		lines[a.LineNo] = strings.TrimRight(lines[a.LineNo], " ") + " #" + tag
		return v.writeRaw(path, reassemble(fm, strings.Join(lines, "\n")))
	}
	return ErrActionNotFound
}
