package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jandro-es/axon/internal/db"
)

const (
	entitiesDir   = "Entities"
	mentionsBlock = "mentions"
)

// entityRef is a normalized named entity (person or project).
type entityRef struct {
	Type string // "person" | "project"
	Name string // display (first-seen casing)
}

func (e entityRef) key() string { return e.Type + "|" + strings.ToLower(e.Name) }

// normalizeEntity trims/collapses whitespace and rejects entries that are too
// short, letter-less (pure numbers/dates), or of an unknown type.
func normalizeEntity(typ, raw string) (entityRef, bool) {
	if typ != "person" && typ != "project" {
		return entityRef{}, false
	}
	name := strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(name) < 2 || !hasLetter(name) {
		return entityRef{}, false
	}
	return entityRef{Type: typ, Name: name}, true
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// entityFileName sanitises a display name into a vault-safe basename.
func entityFileName(name string) string {
	repl := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return ' '
		}
		if r < 0x20 {
			return ' '
		}
		return r
	}, name)
	return strings.Join(strings.Fields(repl), " ")
}

// entityPagePath is the vault path for an entity's page.
func entityPagePath(e entityRef) string {
	sub := "People"
	if e.Type == "project" {
		sub = "Projects"
	}
	return entitiesDir + "/" + sub + "/" + entityFileName(e.Name) + ".md"
}

// entityExtract is the classifier's structured reply.
type entityExtract struct {
	People   []string `json:"people"`
	Projects []string `json:"projects"`
}

// parseEntities extracts the JSON object from a model reply (tolerating prose
// around it, as parseTriage does).
func parseEntities(s string) (entityExtract, error) {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return entityExtract{}, fmt.Errorf("no JSON object in entity output")
	}
	var out entityExtract
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return entityExtract{}, fmt.Errorf("entity JSON: %w", err)
	}
	return out, nil
}

// scannableNote reports whether a note path should be scanned for entities:
// markdown notes outside Entities/ and .axon/, excluding folder READMEs (so
// entity pages never breed mentions of themselves).
func scannableNote(path string) bool {
	if strings.HasPrefix(path, entitiesDir+"/") || strings.HasPrefix(path, ".axon/") {
		return false
	}
	if strings.EqualFold(base(path), "README") {
		return false
	}
	return strings.HasSuffix(path, ".md")
}

// collectEntities normalizes an extract into distinct entity refs (people first,
// then projects), deduped by key within the note.
func collectEntities(ex entityExtract) []entityRef {
	var out []entityRef
	seen := map[string]bool{}
	add := func(typ string, names []string) {
		for _, raw := range names {
			if e, ok := normalizeEntity(typ, raw); ok && !seen[e.key()] {
				seen[e.key()] = true
				out = append(out, e)
			}
		}
	}
	add("person", ex.People)
	add("project", ex.Projects)
	return out
}

const (
	pendingStateKey  = "entity-pages/pending"
	pendingEntityCap = 1000
)

// pendingEntity is an entity seen in fewer than the threshold notes, held in
// automation_state until it materialises.
type pendingEntity struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Sources []string `json:"sources"` // distinct source note paths (no ext)
}

// loadPendingEntities reads the pending-mention map from automation_state
// (empty on any problem — worst case an entity is re-proposed once).
func loadPendingEntities(ctx context.Context, rc RunCtx) map[string]pendingEntity {
	out := map[string]pendingEntity{}
	raw, err := db.GetCursor(ctx, rc.DB, pendingStateKey)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// savePendingEntities persists the pending map, capped at the entities with the
// most sources (closest to materialising) when over pendingEntityCap.
func savePendingEntities(ctx context.Context, rc RunCtx, pending map[string]pendingEntity) {
	if len(pending) > pendingEntityCap {
		keys := make([]string, 0, len(pending))
		for k := range pending {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(a, b string) int { return len(pending[b].Sources) - len(pending[a].Sources) })
		trimmed := map[string]pendingEntity{}
		for _, k := range keys[:pendingEntityCap] {
			trimmed[k] = pending[k]
		}
		pending = trimmed
	}
	raw, err := json.Marshal(pending)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, pendingStateKey, string(raw), rc.now().UTC().Format("2006-01-02T15:04:05Z07:00")); err != nil {
		rc.Log.Warn("entity-pages: persist pending", "err", err)
	}
}

// mentionLine renders one mention: "- [[source]] (date)".
func mentionLine(source, date string) string {
	return fmt.Sprintf("- [[%s]] (%s)", source, date)
}

// mentionHasTarget reports whether the block already lists a mention of source.
func mentionHasTarget(block, source string) bool {
	return strings.Contains(block, "[["+source+"]]")
}

// appendMention adds a mention of source to the page's axon:mentions block if
// absent (wikilink-safe; managed block only). Returns added=true when written.
func appendMention(ctx context.Context, rc RunCtx, pagePath, source, date string) (bool, error) {
	n, err := rc.Vault.Read(ctx, pagePath)
	if err != nil {
		return false, err
	}
	block := managedBlock(n.Body, mentionsBlock)
	if mentionHasTarget(block, source) {
		return false, nil
	}
	content := mentionLine(source, date)
	if strings.TrimSpace(block) != "" {
		content = strings.TrimSpace(block) + "\n" + content
	}
	if err := rc.Vault.Patch(ctx, pagePath, mentionsBlock, content); err != nil {
		return false, err
	}
	return true, nil
}

// materializeEntity creates the entity page (never clobbering an existing one)
// with a mentions block seeded from sources. If the page already exists (race),
// each source is appended instead.
func materializeEntity(ctx context.Context, rc RunCtx, e entityRef, sources []string, date string) error {
	sub, et := "People", "person"
	if e.Type == "project" {
		sub, et = "Projects", "project"
	}
	if _, err := rc.Vault.EnsureDir(entitiesDir + "/" + sub); err != nil {
		return err
	}
	var lines []string
	seen := map[string]bool{}
	for _, s := range sources {
		if seen[s] {
			continue
		}
		seen[s] = true
		lines = append(lines, mentionLine(s, date))
	}
	created, err := rc.Vault.Create(entityPagePath(e), entityPageContent(e, et, date, strings.Join(lines, "\n")))
	if err != nil {
		return err
	}
	if !created {
		for _, s := range sources {
			if _, err := appendMention(ctx, rc, entityPagePath(e), s, date); err != nil {
				return err
			}
		}
	}
	return nil
}

// entityPageContent renders a fresh entity page: frontmatter + a human-owned
// preamble + the axon:mentions managed block. Prose outside the block is the
// human's (cardinal rule 2).
func entityPageContent(e entityRef, entityType, date, mentionLines string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %q\ntype: entity\nentity_type: %s\ncreated: %s\n---\n\n", e.Name, entityType, date)
	b.WriteString("> Auto-maintained entity page. AXON appends mentions inside the managed\n")
	b.WriteString("> block below; everything outside it is yours to edit.\n\n")
	b.WriteString("## Mentions\n\n")
	b.WriteString("<!-- axon:" + mentionsBlock + ":start -->\n" + mentionLines + "\n<!-- axon:" + mentionsBlock + ":end -->\n")
	return b.String()
}
