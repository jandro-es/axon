package automations

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
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
