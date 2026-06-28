// Package identity is AXON's personal-memory & identity layer (Component 12,
// FR-70…FR-73): three plain-Markdown notes under 02-Areas/Profile/ that let the
// second brain *know the user*. USER.md is the profile, SOUL.md is the agent
// persona, MEMORY.md is durable, append-only memory inside an axon:memory
// managed block.
//
// The layer lives only in the vault (the source of truth) — there is no separate
// store (ADR-011). This package authors the layer from onboarding answers
// (Generate, never clobbering human prose), renders a token-bounded snapshot for
// the SessionStart hook with NO model call (Render), and appends durable memory
// entries wikilink-safely (Remember). It depends only on the vault seam, so it
// is reusable by the hook, the MCP tool and the onboarding wizard alike.
package identity

import (
	"fmt"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/vault"
)

// Layer locations and the managed-block name. The Profile area is a PARA "area"
// (ongoing, human-owned); the files are durable and editable in Obsidian.
const (
	Dir        = "02-Areas/Profile"
	UserPath   = Dir + "/USER.md"
	SoulPath   = Dir + "/SOUL.md"
	MemoryPath = Dir + "/MEMORY.md"
	// MemoryBlock is the axon:<name> managed block in MEMORY.md that AXON
	// maintains; prose outside it is the human's (cardinal rule 2).
	MemoryBlock = "memory"
)

// Values are the answers an onboarding interview gathers. Every field is
// optional; Generate fills sensible placeholders so a fully-skipped run still
// produces a valid, editable layer.
type Values struct {
	// USER.md
	Name          string
	Role          string
	Timezone      string
	Communication string
	Goals         []string
	People        []string // bare names; rendered as [[wikilinks]]
	Projects      []string // bare names; rendered as [[wikilinks]]
	Tools         []string

	// SOUL.md
	AgentName  string
	Tone       string
	Boundaries []string

	// Date stamps the frontmatter `updated:` field and the seed MEMORY entry.
	// Injected (not time.Now) so callers stay deterministic/testable.
	Date string
}

// Result reports which layer files were created vs already present, for the
// onboarding summary and idempotency checks.
type Result struct {
	Created []string
	Skipped []string
}

// Changed reports whether Generate wrote anything.
func (r Result) Changed() bool { return len(r.Created) > 0 }

// Present reports whether the identity layer already exists in the vault (used
// to decide first-run vs update mode, and to drive the `axon onboard` hint).
func Present(v *vault.FS) bool { return v.Exists(UserPath) }

// Generate writes USER.md, SOUL.md and MEMORY.md from vals, idempotently and
// WITHOUT clobbering existing files (cardinal rule 2 — converge, never
// overwrite). It makes no model call. A file that already exists is left exactly
// as the human last edited it and reported as skipped.
func Generate(v *vault.FS, vals Values) (Result, error) {
	if vals.Date == "" {
		vals.Date = time.Now().UTC().Format("2006-01-02")
	}
	var res Result
	files := []struct {
		path    string
		content string
	}{
		{UserPath, renderUser(vals)},
		{SoulPath, renderSoul(vals)},
		{MemoryPath, renderMemory(vals)},
	}
	for _, f := range files {
		created, err := v.Create(f.path, f.content)
		if err != nil {
			return res, fmt.Errorf("write identity file %q: %w", f.path, err)
		}
		if created {
			res.Created = append(res.Created, f.path)
		} else {
			res.Skipped = append(res.Skipped, f.path)
		}
	}
	return res, nil
}

// --- rendering --------------------------------------------------------------

func renderUser(v Values) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "---\ntitle: \"User profile\"\ntype: user\nupdated: %s\n---\n\n", v.Date)
	b.WriteString("> Your profile. AXON reads this at the start of every Claude Code session so\n")
	b.WriteString("> the assistant knows who you are. Edit it freely in Obsidian — AXON never\n")
	b.WriteString("> overwrites this file.\n\n")

	b.WriteString("## Identity\n")
	fmt.Fprintf(b, "- name: %s\n", orPlaceholder(v.Name, "(your name)"))
	fmt.Fprintf(b, "- role: %s\n", orPlaceholder(v.Role, "(what you do)"))
	fmt.Fprintf(b, "- timezone: %s\n\n", orPlaceholder(v.Timezone, "(e.g. Europe/Madrid)"))

	b.WriteString("## Working style\n")
	fmt.Fprintf(b, "- communication: %s\n\n", orPlaceholder(v.Communication, "concise, no preamble; bullet points"))

	b.WriteString("## Now\n")
	writeList(b, "goals", v.Goals, false, "(current objectives)")
	writeList(b, "people", v.People, true, "")
	writeList(b, "projects", v.Projects, true, "")
	writeList(b, "tools", v.Tools, false, "")
	return b.String()
}

func renderSoul(v Values) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "---\ntitle: \"Agent persona\"\ntype: soul\nupdated: %s\n---\n\n", v.Date)
	b.WriteString("> The persona AXON's assistant adopts when working in your vault. This is\n")
	b.WriteString("> steering, not data — edit it to shape the assistant's voice and limits.\n\n")

	b.WriteString("## Persona\n")
	fmt.Fprintf(b, "- name: %s\n", orPlaceholder(v.AgentName, "Axon"))
	fmt.Fprintf(b, "- tone: %s\n\n", orPlaceholder(v.Tone, "direct, warm, pragmatic"))

	b.WriteString("## Boundaries\n")
	if len(v.Boundaries) == 0 {
		b.WriteString("- Never mutate the vault outside AXON's wikilink-safe tools.\n")
		b.WriteString("- Ask before any outward-facing or hard-to-reverse action.\n")
	} else {
		for _, bd := range v.Boundaries {
			fmt.Fprintf(b, "- %s\n", bd)
		}
	}
	return b.String()
}

func renderMemory(v Values) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "---\ntitle: \"Durable memory\"\ntype: memory\nupdated: %s\n---\n\n", v.Date)
	b.WriteString("> Durable decisions, lessons and learned preferences. AXON appends dated\n")
	b.WriteString("> entries inside the managed block below (newest first); everything outside\n")
	b.WriteString("> it is yours. The most recent entries are injected at session start.\n\n")
	b.WriteString("## Memory\n\n")
	seed := fmt.Sprintf("- %s — Identity layer created via `axon onboard`. (source: onboarding)", v.Date)
	b.WriteString(blockText(seed))
	b.WriteString("\n")
	return b.String()
}

// blockText wraps content in the axon:memory managed block markers.
func blockText(content string) string {
	return fmt.Sprintf("<!-- axon:%s:start -->\n%s\n<!-- axon:%s:end -->", MemoryBlock, content, MemoryBlock)
}

func writeList(b *strings.Builder, key string, items []string, wikilink bool, placeholder string) {
	if len(items) == 0 {
		if placeholder != "" {
			fmt.Fprintf(b, "- %s: %s\n", key, placeholder)
		} else {
			fmt.Fprintf(b, "- %s: []\n", key)
		}
		return
	}
	rendered := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if wikilink {
			rendered = append(rendered, "[["+it+"]]")
		} else {
			rendered = append(rendered, it)
		}
	}
	// Wikilink lists are written bare (e.g. "[[A]], [[B]]") so Obsidian renders
	// them as links; plain lists are wrapped in brackets for readability.
	if wikilink {
		fmt.Fprintf(b, "- %s: %s\n", key, strings.Join(rendered, ", "))
	} else {
		fmt.Fprintf(b, "- %s: [%s]\n", key, strings.Join(rendered, ", "))
	}
}

func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}
