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

// IsSystemPath reports whether a vault-relative path has any segment inside a
// system directory (.obsidian, .axon, .claude, .git, .trash), compared
// case-insensitively. The MCP boundary uses this to refuse agent-supplied
// writes into configuration/instruction directories — a prompt-injected agent
// writing .claude/CLAUDE.md would otherwise rewrite its own rules for the next
// session. AXON's internal writers (review queue, scaffolding) legitimately
// use these directories and call the FS helpers directly.
func IsSystemPath(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	for _, seg := range strings.Split(clean, "/") {
		if systemDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
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

// safeAbs maps a vault-relative (slash-separated) path to an OS path under root,
// REFUSING any path that is absolute or escapes the vault via "..". This is the
// security boundary for the vault: MCP tools pass agent-supplied paths straight
// to Read/Write/Patch/Move, so without containment a crafted path like
// "../../etc/passwd" would read or overwrite arbitrary host files. Every helper
// that touches the filesystem goes through here.
func (v *FS) safeAbs(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("empty vault path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(filepath.ToSlash(rel), "/") {
		return "", fmt.Errorf("vault path %q must be relative, not absolute", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("vault path %q escapes the vault root", rel)
	}
	abs := filepath.Join(v.root, clean)
	if err := v.checkNoSymlinkEscape(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// checkNoSymlinkEscape refuses paths whose nearest existing ancestor (or the
// target itself) resolves, via symlinks, to somewhere outside the vault root.
// The lexical ".." check above cannot see a symlink planted inside the vault
// that points at /etc or the user's home.
func (v *FS) checkNoSymlinkEscape(abs string) error {
	rootReal, err := filepath.EvalSymlinks(v.root)
	if err != nil {
		return nil // vault root doesn't exist yet (pre-init); nothing to escape
	}
	for dir := abs; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
				return fmt.Errorf("vault path %q resolves outside the vault root (symlink)", abs)
			}
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
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
	abs, err := v.safeAbs(rel)
	if err != nil {
		return nil, err
	}
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
	abs, err := v.safeAbs(rel)
	if err != nil {
		return err
	}
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
	fromAbs, err := v.safeAbs(from)
	if err != nil {
		return err
	}
	toAbs, err := v.safeAbs(to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(fromAbs); err != nil {
		return fmt.Errorf("move source %q: %w", from, err)
	}
	if _, err := os.Stat(toAbs); err == nil {
		return fmt.Errorf("move destination %q already exists", to)
	}

	// Stage all inbound-link rewrites BEFORE touching the filesystem, so a read
	// error aborts the move with nothing changed (no half-rewritten vault).
	paths, err := v.List(ctx)
	if err != nil {
		return err
	}
	toSlash := filepath.ToSlash(to)
	type rewrite struct{ path, content string }
	var staged []rewrite
	for _, p := range paths {
		if p == filepath.ToSlash(from) || p == toSlash {
			continue // the moved note's own outbound links are unaffected
		}
		abs, err := v.safeAbs(p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("scan %q for links: %w", p, err)
		}
		fm, body := splitFrontmatter(string(data))
		newBody, n := rewriteLinksForMove(body, from, to)
		if n == 0 {
			continue
		}
		staged = append(staged, rewrite{p, reassemble(fm, newBody)})
	}

	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return fmt.Errorf("create destination dir for %q: %w", to, err)
	}
	// Move the file (atomic rename), then apply the staged, already-computed
	// rewrites (each write is itself atomic).
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", from, to, err)
	}
	for _, rw := range staged {
		if err := v.writeRaw(rw.path, rw.content); err != nil {
			return fmt.Errorf("rewrite inbound links in %q (note moved; some links may need repair): %w", rw.path, err)
		}
	}
	return nil
}

// --- creation helpers (used by the scaffold; never clobber existing files) ---

// Exists reports whether a vault-relative path exists. An unsafe (escaping)
// path is reported as non-existent.
func (v *FS) Exists(rel string) bool {
	abs, err := v.safeAbs(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// EnsureDir creates a vault-relative directory if missing. created reports
// whether it had to be made.
func (v *FS) EnsureDir(rel string) (created bool, err error) {
	abs, err := v.safeAbs(rel)
	if err != nil {
		return false, err
	}
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
	abs, err := v.safeAbs(rel)
	if err != nil {
		return err
	}
	existing := ""
	if data, rerr := os.ReadFile(abs); rerr == nil {
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
	abs, err := v.safeAbs(rel)
	if err != nil {
		return err
	}
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
	// Best-effort fsync of the parent directory so the rename itself is durable
	// (NFR-06). Ignored where unsupported (e.g. Windows).
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
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
