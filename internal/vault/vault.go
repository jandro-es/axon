// Package vault is the seam to the Markdown vault — the source of truth. The
// production implementation performs frontmatter-aware, wikilink-safe reads and
// writes; Phase 0 defines the contract and an in-memory fake.
//
// Cardinal rule: no vault mutation that isn't wikilink-safe. Moves rewrite
// inbound links (Move); content edits land inside axon:* managed blocks
// (Patch) and never clobber human prose. There is deliberately NO Delete.
package vault

import (
	"context"
	"time"
)

// Note is a parsed vault note: frontmatter plus raw body, with its vault-
// relative path. Frontmatter is kept as a generic map so unknown keys survive
// round-trips untouched (the agent must never reorder or strip them).
type Note struct {
	Path        string         // vault-relative, e.g. "01-Projects/foo.md"
	Frontmatter map[string]any // YAML frontmatter, order-preserving concerns aside
	Body        string         // markdown body (without frontmatter)
	Updated     time.Time
}

// Vault is the wikilink-safe interface to Markdown notes. Implementations must
// be safe for concurrent use.
type Vault interface {
	// List returns vault-relative paths of all notes.
	List(ctx context.Context) ([]string, error)
	// Read returns the parsed note at a vault-relative path.
	Read(ctx context.Context, path string) (*Note, error)
	// Write creates or replaces a note's full content. Used for new notes;
	// existing human prose is protected by callers using Patch instead.
	Write(ctx context.Context, path string, n *Note) error
	// Patch updates the content of a single axon:* managed block by name,
	// leaving everything else (including human prose) untouched.
	Patch(ctx context.Context, path, block, content string) error
	// Move renames/moves a note and rewrites inbound wikilinks so none break.
	Move(ctx context.Context, from, to string) error
}
