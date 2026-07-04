// Package review parses AXON's review queue (.axon/review-queue.md) into
// typed items and resolves them through the vault's wikilink-safe ops
// (ADR-020): accepted links land in axon:links managed blocks, accepted
// triage proposals perform the wikilink-safe move, and the queue line flips
// via the .axon/-guarded RewriteSystemFile. Prose is never touched.
package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/vault"
)

const (
	queuePath = ".axon/review-queue.md"
	// archivePath receives resolved lines older than archiveAfterDays when a
	// resolution rewrites the queue (FR-103). Append-only, human-prunable,
	// parsed by nothing.
	archivePath      = ".axon/review-queue-archive.md"
	archiveAfterDays = 7
)

// Item is one review-queue entry.
type Item struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // link | pair | triage | resurface | info
	Section string   `json:"section"`
	Line    string   `json:"line"`
	Checked bool     `json:"checked"`
	Note    string   `json:"note,omitempty"`   // the note an accept writes to / moves
	Target  string   `json:"target,omitempty"` // the note an accept links to
	Folder  string   `json:"folder,omitempty"` // triage destination
	Tags    []string `json:"tags,omitempty"`
}

var (
	sectionRe    = regexp.MustCompile(`^## (.+)$`)
	sectionForRe = regexp.MustCompile(`^## Link suggestions for \[\[([^\]]+)\]\]`)
	lineRe       = regexp.MustCompile(`^- \[([ x])\] (.*)$`)
	linkToRe     = regexp.MustCompile(`^link to \[\[([^\]]+)\]\]`)
	pairRe       = regexp.MustCompile(`^\[\[([^\]]+)\]\] ↔ \[\[([^\]]+)\]\]`)
	triageRe     = regexp.MustCompile(`^triage \[\[([^\]]+)\]\] → (\S+) \(tags: ([^)]*)\)`)
	resurfaceRe  = regexp.MustCompile(`^resurface \[\[([^\]]+)\]\] — related to recent \[\[([^\]]+)\]\]`)
)

// Load parses the queue file. A missing file is an empty queue.
func Load(ctx context.Context, v *vault.FS) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(queuePath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read review queue: %w", err)
	}

	var items []Item
	section, sectionNote := "", ""
	for _, raw := range strings.Split(string(data), "\n") {
		if m := sectionRe.FindStringSubmatch(raw); m != nil {
			section = m[1]
			sectionNote = ""
			if fm := sectionForRe.FindStringSubmatch(raw); fm != nil {
				sectionNote = fm[1]
			}
			continue
		}
		m := lineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		it := Item{
			Section: section,
			Line:    raw,
			Checked: m[1] == "x",
			Kind:    "info",
		}
		body := m[2]
		switch {
		case linkToRe.MatchString(body):
			lm := linkToRe.FindStringSubmatch(body)
			if sectionNote != "" {
				it.Kind, it.Note, it.Target = "link", sectionNote, lm[1]
			}
		case pairRe.MatchString(body):
			pm := pairRe.FindStringSubmatch(body)
			it.Kind, it.Note, it.Target = "pair", pm[1], pm[2]
		case triageRe.MatchString(body):
			tm := triageRe.FindStringSubmatch(body)
			it.Kind, it.Note, it.Folder = "triage", tm[1], tm[2]
			for _, tag := range strings.Split(tm[3], ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					it.Tags = append(it.Tags, tag)
				}
			}
		case resurfaceRe.MatchString(body):
			rm := resurfaceRe.FindStringSubmatch(body)
			it.Kind, it.Target, it.Note = "resurface", rm[1], rm[2]
		}
		// The ID hashes the normalized body (checkbox + resolution suffix
		// stripped) so an item keeps its identity across resolution — a
		// stale click gets "already resolved", not "not found".
		sum := sha256.Sum256([]byte(it.Section + "\x00" + normalizeLine(body)))
		it.ID = hex.EncodeToString(sum[:])[:12]
		items = append(items, it)
	}
	return items, nil
}

// Accept applies a pending item through wikilink-safe ops and resolves its
// queue line.
func Accept(ctx context.Context, v *vault.FS, id string) (Item, error) {
	it, err := find(ctx, v, id)
	if err != nil {
		return Item{}, err
	}
	switch it.Kind {
	case "link", "pair", "resurface":
		if err := appendToLinksBlock(ctx, v, it.Note, it.Target); err != nil {
			return Item{}, err
		}
	case "triage":
		dest := it.Folder + "/" + path.Base(it.Note) + ".md"
		if err := v.Move(ctx, it.Note+".md", dest); err != nil {
			return Item{}, err
		}
	default:
		return Item{}, fmt.Errorf("item %s (%s) is not actionable — dismiss it instead", id, it.Kind)
	}
	return mark(ctx, v, it, "✓ applied")
}

// Dismiss resolves a pending item without applying it.
func Dismiss(ctx context.Context, v *vault.FS, id string) (Item, error) {
	it, err := find(ctx, v, id)
	if err != nil {
		return Item{}, err
	}
	return mark(ctx, v, it, "✗ dismissed")
}

func find(ctx context.Context, v *vault.FS, id string) (Item, error) {
	items, err := Load(ctx, v)
	if err != nil {
		return Item{}, err
	}
	for _, it := range items {
		if it.ID == id {
			if it.Checked {
				return Item{}, fmt.Errorf("item %s is already resolved", id)
			}
			return it, nil
		}
	}
	return Item{}, fmt.Errorf("item %s not found", id)
}

// appendToLinksBlock adds "- [[target]]" to the note's axon:links managed
// block (created if absent; idempotent). Prose is never touched (cardinal
// rule 2): Patch replaces only the managed region.
func appendToLinksBlock(ctx context.Context, v *vault.FS, note, target string) error {
	notePath := note + ".md"
	n, err := v.Read(ctx, notePath)
	if err != nil {
		return fmt.Errorf("accept target note: %w", err)
	}
	existing := extractBlock(n.Body, "links")
	linkLine := "- [[" + target + "]]"
	if strings.Contains(existing, linkLine) {
		return nil // already linked: accept is idempotent
	}
	content := linkLine
	if strings.TrimSpace(existing) != "" {
		content = strings.TrimSpace(existing) + "\n" + linkLine
	}
	return v.Patch(ctx, notePath, "links", content)
}

// resolutionRe matches the suffix mark() appends, so IDs survive resolution.
var resolutionRe = regexp.MustCompile(` — [✓✗] (applied|dismissed) \d{4}-\d{2}-\d{2}$`)

// resolvedDateRe extracts the resolution date mark() appends.
var resolvedDateRe = regexp.MustCompile(` — [✓✗] (?:applied|dismissed) (\d{4}-\d{2}-\d{2})$`)

// normalizeLine strips the resolution suffix from a line body for hashing.
func normalizeLine(body string) string {
	return resolutionRe.ReplaceAllString(body, "")
}

// extractBlock returns the inner content of an axon:<name> managed block.
func extractBlock(body, name string) string {
	start := "<!-- axon:" + name + ":start -->"
	end := "<!-- axon:" + name + ":end -->"
	i := strings.Index(body, start)
	if i < 0 {
		return ""
	}
	rest := body[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// mark flips the item's queue line to checked with a resolution suffix.
func mark(ctx context.Context, v *vault.FS, it Item, suffix string) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	abs := filepath.Join(v.Root(), filepath.FromSlash(queuePath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return Item{}, err
	}
	newLine := strings.Replace(it.Line, "- [ ]", "- [x]", 1) +
		" — " + suffix + " " + time.Now().UTC().Format("2006-01-02")
	content := strings.Replace(string(data), it.Line, newLine, 1)
	if content == string(data) {
		return Item{}, fmt.Errorf("item %s: queue line changed underneath — reload", it.ID)
	}
	// Compaction (FR-103): resolved lines past the threshold move to the
	// archive during the rewrite this resolution already performs.
	// Archive-append precedes the queue rewrite — a crash between the two
	// duplicates an archive line at worst, never loses one.
	if kept, archived := compact(content, time.Now().UTC()); archived != "" {
		stamp := "\n<!-- archived " + time.Now().UTC().Format(time.RFC3339) + " -->\n"
		if err := v.Append(archivePath, stamp+archived); err != nil {
			return Item{}, err
		}
		content = kept
	}
	if err := v.RewriteSystemFile(queuePath, content); err != nil {
		return Item{}, err
	}
	it.Checked = true
	it.Line = newLine
	return it, nil
}

// compact splits queue content into what stays and what archives: resolved
// lines older than archiveAfterDays move out, grouped under their original
// section header; section headers left with no items are dropped from the
// kept content. Pending lines, fresh resolutions, and resolved lines whose
// date does not parse are kept — never archive on guesswork.
func compact(content string, now time.Time) (kept, archived string) {
	cutoff := now.AddDate(0, 0, -archiveAfterDays)
	type section struct {
		header  string
		keep    []string
		archive []string
	}
	cur := &section{} // preamble: lines before the first header
	sections := []*section{cur}
	for _, line := range strings.Split(content, "\n") {
		if sectionRe.MatchString(line) {
			cur = &section{header: line}
			sections = append(sections, cur)
			continue
		}
		if m := lineRe.FindStringSubmatch(line); m != nil && m[1] == "x" {
			if dm := resolvedDateRe.FindStringSubmatch(m[2]); dm != nil {
				if d, derr := time.Parse("2006-01-02", dm[1]); derr == nil && d.Before(cutoff) {
					cur.archive = append(cur.archive, line)
					continue
				}
			}
		}
		cur.keep = append(cur.keep, line)
	}

	var keepB, archB strings.Builder
	for _, s := range sections {
		if len(s.archive) > 0 {
			if s.header != "" {
				archB.WriteString(s.header + "\n")
			}
			archB.WriteString(strings.Join(s.archive, "\n") + "\n")
		}
		hasItem := false
		for _, l := range s.keep {
			if lineRe.MatchString(l) {
				hasItem = true
				break
			}
		}
		if s.header != "" && !hasItem {
			continue // emptied section: drop header and residual blanks
		}
		if s.header != "" {
			keepB.WriteString(s.header + "\n")
		}
		if len(s.keep) > 0 {
			keepB.WriteString(strings.Join(s.keep, "\n") + "\n")
		}
	}
	return keepB.String(), archB.String()
}
