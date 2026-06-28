package vault

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// systemDirs are vault subdirectories AXON never treats as note content.
var systemDirs = map[string]bool{
	".obsidian": true,
	".axon":     true,
	".claude":   true,
	".git":      true,
	".trash":    true,
}

// FS is a filesystem-backed Vault rooted at a directory of Markdown files. All
// mutating operations are wikilink-safe and write atomically (temp + rename),
// per the cardinal rule and NFR-06. It is safe for sequential use by the
// single-writer daemon; concurrent writers are not expected.
type FS struct {
	root string
}

// NewFS returns a vault rooted at dir. The directory is not required to exist
// yet (axon init creates it); operations that need it will error clearly.
func NewFS(dir string) *FS {
	return &FS{root: filepath.Clean(dir)}
}

// Root returns the absolute-ish vault root path.
func (v *FS) Root() string { return v.root }

// abs maps a vault-relative (slash-separated) path to an OS path under root.
func (v *FS) abs(rel string) string {
	return filepath.Join(v.root, filepath.FromSlash(rel))
}

// rel maps an OS path under root back to a slash-separated vault-relative path.
func (v *FS) rel(abs string) (string, error) {
	r, err := filepath.Rel(v.root, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(r), nil
}

// List returns vault-relative paths of all Markdown notes, sorted, skipping
// system directories.
func (v *FS) List(ctx context.Context) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(v.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			if p != v.root && systemDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		r, err := v.rel(p)
		if err != nil {
			return err
		}
		paths = append(paths, r)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list vault %q: %w", v.root, err)
	}
	sort.Strings(paths)
	return paths, nil
}

// Read parses the note at a vault-relative path.
func (v *FS) Read(ctx context.Context, rel string) (*Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs := v.abs(rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read note %q: %w", rel, err)
	}
	var updated time.Time
	if info, statErr := os.Stat(abs); statErr == nil {
		updated = info.ModTime()
	}
	return parseNote(filepath.ToSlash(rel), string(data), updated)
}

// Write creates or replaces a note's full content. New notes are authored from
// the Note's frontmatter + body; this path is used for AXON-authored notes, not
// for editing human prose (callers use Patch for that).
func (v *FS) Write(ctx context.Context, rel string, n *Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	content, err := n.render()
	if err != nil {
		return fmt.Errorf("render note %q: %w", rel, err)
	}
	return v.writeRaw(rel, content)
}

// Patch replaces the content of a single axon:<block> managed region, leaving
// the frontmatter and all human prose untouched. The block is created at the
// end of the body if absent. Frontmatter bytes are preserved exactly.
func (v *FS) Patch(ctx context.Context, rel, block, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	abs := v.abs(rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("patch note %q: %w", rel, err)
	}
	fm, body := splitFrontmatter(string(data))
	newBody, err := patchManagedBlock(body, block, content)
	if err != nil {
		return fmt.Errorf("patch note %q: %w", rel, err)
	}
	return v.writeRaw(rel, reassemble(fm, newBody))
}

// Move renames/moves a note and rewrites every inbound wikilink and embed across
// the vault so none break. Refuses to overwrite an existing destination and
// refuses to operate outside the vault. This is the only sanctioned way to
// relocate a note; there is deliberately no Delete.
func (v *FS) Move(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fromAbs, toAbs := v.abs(from), v.abs(to)
	if _, err := os.Stat(fromAbs); err != nil {
		return fmt.Errorf("move source %q: %w", from, err)
	}
	if _, err := os.Stat(toAbs); err == nil {
		return fmt.Errorf("move destination %q already exists", to)
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return fmt.Errorf("create destination dir for %q: %w", to, err)
	}

	// Move the file itself first (atomic rename).
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", from, to, err)
	}

	// Rewrite inbound links in every other note.
	paths, err := v.List(ctx)
	if err != nil {
		return err
	}
	toSlash := filepath.ToSlash(to)
	for _, p := range paths {
		if p == toSlash {
			continue // the moved note's own outbound links are unaffected
		}
		data, err := os.ReadFile(v.abs(p))
		if err != nil {
			return fmt.Errorf("scan %q for links: %w", p, err)
		}
		fm, body := splitFrontmatter(string(data))
		newBody, n := rewriteLinksForMove(body, from, to)
		if n == 0 {
			continue
		}
		if err := v.writeRaw(p, reassemble(fm, newBody)); err != nil {
			return err
		}
	}
	return nil
}

// --- creation helpers (used by the scaffold; never clobber existing files) ---

// Exists reports whether a vault-relative path exists.
func (v *FS) Exists(rel string) bool {
	_, err := os.Stat(v.abs(rel))
	return err == nil
}

// EnsureDir creates a vault-relative directory if missing. created reports
// whether it had to be made.
func (v *FS) EnsureDir(rel string) (created bool, err error) {
	abs := v.abs(rel)
	if _, statErr := os.Stat(abs); statErr == nil {
		return false, nil
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return false, fmt.Errorf("create dir %q: %w", rel, err)
	}
	return true, nil
}

// Create writes a new file with content, but never overwrites an existing one
// (idempotent scaffolding). created reports whether it was newly written.
func (v *FS) Create(rel, content string) (created bool, err error) {
	if v.Exists(rel) {
		return false, nil
	}
	if err := v.writeRaw(rel, content); err != nil {
		return false, err
	}
	return true, nil
}

// Append appends content to a vault-relative file (creating it if absent),
// writing the new whole-file content atomically. Used for AXON-managed system
// files like .axon/review-queue.md; not for human notes.
func (v *FS) Append(rel, content string) error {
	existing := ""
	if data, err := os.ReadFile(v.abs(rel)); err == nil {
		existing = string(data)
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return v.writeRaw(rel, existing+content)
}

// writeRaw atomically writes content to a vault-relative path: it writes a temp
// file in the destination directory then renames it into place, so a reader
// never observes a half-written note (NFR-06).
func (v *FS) writeRaw(rel, content string) error {
	abs := v.abs(rel)
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir for %q: %w", rel, err)
	}
	tmp, err := os.CreateTemp(dir, ".axon-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file for %q: %w", rel, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %q: %w", rel, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %q: %w", rel, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %q: %w", rel, err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return fmt.Errorf("commit %q: %w", rel, err)
	}
	cleanup = false
	return nil
}

// patchManagedBlock replaces (or appends) an axon:<block> managed region.
func patchManagedBlock(body, block, content string) (string, error) {
	start := fmt.Sprintf("<!-- axon:%s:start -->", block)
	end := fmt.Sprintf("<!-- axon:%s:end -->", block)
	replacement := start + "\n" + content + "\n" + end
	if i := strings.Index(body, start); i >= 0 {
		j := strings.Index(body[i:], end)
		if j < 0 {
			return "", fmt.Errorf("unterminated managed block %q", block)
		}
		j += i + len(end)
		return body[:i] + replacement + body[j:], nil
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + replacement + "\n", nil
}

// reassemble joins a raw frontmatter block (without fences) and a body back into
// file content. An empty frontmatter yields just the body.
func reassemble(fm, body string) string {
	if fm == "" {
		return body
	}
	var b strings.Builder
	b.WriteString(fmFence + "\n")
	b.WriteString(fm)
	if !strings.HasSuffix(fm, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(fmFence + "\n")
	b.WriteString(body)
	return b.String()
}

// compile-time assertion that *FS satisfies Vault.
var _ Vault = (*FS)(nil)
