package vault

import (
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// Note is a parsed vault note: frontmatter plus body, with its vault-relative
// path. Frontmatter is kept as a generic map so unknown keys survive round-trips
// untouched (the agent must never reorder or strip them). Mutating operations
// that must preserve frontmatter byte-for-byte (Patch, Move) work on the raw
// file content directly rather than re-serialising this map.
type Note struct {
	Path        string         // vault-relative, slash-separated, e.g. "01-Projects/foo.md"
	Frontmatter map[string]any // parsed YAML frontmatter (nil if none)
	Body        string         // markdown body (without the frontmatter block)
	Updated     time.Time
}

// fmFence is the YAML frontmatter delimiter.
const fmFence = "---"

// splitFrontmatter separates a leading YAML frontmatter block from the body. It
// returns the raw frontmatter text (without the fences) and the remaining body.
// If the content has no well-formed frontmatter, fm is empty and body is the
// whole input. This operates on raw text so callers can reassemble losslessly.
func splitFrontmatter(content string) (fm, body string) {
	// Frontmatter must start at the very beginning of the file.
	if !strings.HasPrefix(content, fmFence+"\n") && content != fmFence {
		return "", content
	}
	rest := content[len(fmFence):]
	rest = strings.TrimPrefix(rest, "\n")
	// Find the closing fence at the start of a line.
	end := findClosingFence(rest)
	if end < 0 {
		// Unterminated frontmatter: treat the whole file as body to avoid
		// corrupting content.
		return "", content
	}
	fm = rest[:end]
	body = rest[end:]
	// Drop the closing fence line from the body.
	body = strings.TrimPrefix(body, fmFence)
	body = strings.TrimPrefix(body, "\n")
	return fm, body
}

// findClosingFence returns the index in s where a line consisting solely of the
// frontmatter fence begins, or -1 if none.
func findClosingFence(s string) int {
	offset := 0
	for {
		line := s[offset:]
		nl := strings.IndexByte(line, '\n')
		var cur string
		if nl < 0 {
			cur = line
		} else {
			cur = line[:nl]
		}
		if strings.TrimRight(cur, "\r") == fmFence {
			return offset
		}
		if nl < 0 {
			return -1
		}
		offset += nl + 1
		if offset >= len(s) {
			return -1
		}
	}
}

// parseNote parses raw file content at a vault-relative path into a Note.
func parseNote(path, content string, updated time.Time) (*Note, error) {
	fm, body := splitFrontmatter(content)
	n := &Note{Path: path, Body: body, Updated: updated}
	if fm != "" {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
			// Malformed frontmatter is non-fatal for indexing: keep the body and
			// surface no metadata rather than failing the whole note.
			return n, nil
		}
		n.Frontmatter = m
	}
	return n, nil
}

// render serialises a Note back to file content. Used only when creating new
// notes from structured data; existing notes are mutated via raw-preserving
// paths. Frontmatter key order is not guaranteed here, which is acceptable for
// freshly authored notes.
func (n *Note) render() (string, error) {
	if len(n.Frontmatter) == 0 {
		return n.Body, nil
	}
	out, err := yaml.Marshal(n.Frontmatter)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(fmFence + "\n")
	b.Write(out)
	if !strings.HasSuffix(string(out), "\n") {
		b.WriteString("\n")
	}
	b.WriteString(fmFence + "\n")
	b.WriteString(n.Body)
	return b.String(), nil
}

// FrontmatterString returns a string-valued frontmatter field, or "".
func (n *Note) FrontmatterString(key string) string {
	if n.Frontmatter == nil {
		return ""
	}
	if v, ok := n.Frontmatter[key].(string); ok {
		return v
	}
	return ""
}

// Tags returns the note's frontmatter tags as a string slice (handles both a
// YAML list and a single scalar).
func (n *Note) Tags() []string {
	if n.Frontmatter == nil {
		return nil
	}
	switch v := n.Frontmatter["tags"].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, t := range v {
			if s, ok := t.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}
