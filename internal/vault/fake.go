package vault

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Fake is an in-memory Vault for tests and the Phase 0 skeleton. It models
// notes as a path->Note map and implements just enough wikilink-safe behaviour
// to exercise callers: Move rewrites simple [[path]] references in other notes'
// bodies. It is NOT the production implementation.
type Fake struct {
	mu    sync.Mutex
	notes map[string]*Note
}

// NewFake returns an empty in-memory vault.
func NewFake() *Fake {
	return &Fake{notes: make(map[string]*Note)}
}

// List returns all note paths in sorted order for determinism.
func (f *Fake) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := make([]string, 0, len(f.notes))
	for p := range f.notes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// Read returns a copy of the note at path.
func (f *Fake) Read(ctx context.Context, path string) (*Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[path]
	if !ok {
		return nil, fmt.Errorf("note %q not found", path)
	}
	cp := *n
	return &cp, nil
}

// Write stores the note at path.
func (f *Fake) Write(ctx context.Context, path string, n *Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *n
	cp.Path = path
	f.notes[path] = &cp
	return nil
}

// Patch replaces the body of an axon:<block> managed region, appending one if
// absent. Human prose outside the markers is preserved.
func (f *Fake) Patch(ctx context.Context, path, block, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[path]
	if !ok {
		return fmt.Errorf("note %q not found", path)
	}
	start := fmt.Sprintf("<!-- axon:%s:start -->", block)
	end := fmt.Sprintf("<!-- axon:%s:end -->", block)
	replacement := start + "\n" + content + "\n" + end
	if i := strings.Index(n.Body, start); i >= 0 {
		j := strings.Index(n.Body[i:], end)
		if j < 0 {
			return fmt.Errorf("note %q: unterminated managed block %q", path, block)
		}
		j += i + len(end)
		n.Body = n.Body[:i] + replacement + n.Body[j:]
	} else {
		if n.Body != "" && !strings.HasSuffix(n.Body, "\n") {
			n.Body += "\n"
		}
		n.Body += replacement + "\n"
	}
	return nil
}

// Move renames a note and rewrites [[from]] wikilinks in every other note.
func (f *Fake) Move(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[from]
	if !ok {
		return fmt.Errorf("note %q not found", from)
	}
	if _, exists := f.notes[to]; exists {
		return fmt.Errorf("destination %q already exists", to)
	}
	n.Path = to
	delete(f.notes, from)
	f.notes[to] = n

	fromLink := wikilinkTarget(from)
	toLink := wikilinkTarget(to)
	for _, other := range f.notes {
		other.Body = strings.ReplaceAll(other.Body, "[["+fromLink+"]]", "[["+toLink+"]]")
	}
	return nil
}

// wikilinkTarget reduces a vault path to the form used inside [[...]] (drop the
// .md extension; keep the rest).
func wikilinkTarget(path string) string {
	return strings.TrimSuffix(path, ".md")
}

// compile-time assertion that Fake satisfies Vault.
var _ Vault = (*Fake)(nil)
