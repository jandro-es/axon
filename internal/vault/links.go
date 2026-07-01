package vault

import (
	"path"
	"regexp"
	"strings"
)

// LinkKind classifies an edge in the link graph.
type LinkKind string

const (
	KindWikilink LinkKind = "wikilink"
	KindEmbed    LinkKind = "embed"
	KindTag      LinkKind = "tag"
)

// Link is a single outbound reference parsed from a note body.
type Link struct {
	Target  string   // note reference, before any #heading, ^block or |display
	Heading string   // the #... portion (without the leading #), may be empty
	Display string   // the |... alias, may be empty
	Kind    LinkKind // wikilink | embed | tag
}

// wikilinkRe matches [[target]] and ![[target]] (embed), capturing the optional
// leading '!' and the inner reference. Inner content excludes brackets.
var wikilinkRe = regexp.MustCompile(`(!?)\[\[([^\[\]]+)\]\]`)

// tagRe matches inline #tags (allowing nested tags like #topic/sub), anchored to
// a word boundary so markdown headings ("# Heading") and things like "C#" do
// not match.
var tagRe = regexp.MustCompile(`(^|\s)#([A-Za-z0-9][A-Za-z0-9_/\-]*)`)

// ParseLinks extracts wikilinks, embeds and inline tags from a note body, in
// document order. Same-file links ("[[#heading]]") are skipped (no target).
func ParseLinks(body string) []Link {
	var links []Link
	for _, m := range wikilinkRe.FindAllStringSubmatch(body, -1) {
		embed := m[1] == "!"
		target, heading, display := splitWikilink(m[2])
		if target == "" {
			continue // same-file heading/block reference
		}
		kind := KindWikilink
		if embed {
			kind = KindEmbed
		}
		links = append(links, Link{Target: target, Heading: heading, Display: display, Kind: kind})
	}
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		links = append(links, Link{Target: m[2], Kind: KindTag})
	}
	return links
}

// splitWikilink decomposes a wikilink's inner content into target, heading and
// display. Order in Obsidian is target(#heading|#^block)(|display). A bare
// "^block" separator (without "#") is tolerated too; the block marker is kept
// in the heading part (prefixed "^") so rewrites preserve the reference —
// buildInner re-emits it as the canonical "#^block" form.
func splitWikilink(inner string) (target, heading, display string) {
	if i := strings.Index(inner, "|"); i >= 0 {
		display = strings.TrimSpace(inner[i+1:])
		inner = inner[:i]
	}
	if i := strings.IndexAny(inner, "#^"); i >= 0 {
		frag := strings.TrimSpace(inner[i+1:])
		if inner[i] == '^' {
			frag = "^" + frag // block reference: keep the ^ marker
		}
		heading = frag
		inner = inner[:i]
	}
	target = strings.TrimSpace(inner)
	return target, heading, display
}

// linkTargetKey normalises a wikilink target for comparison: it drops a trailing
// ".md" if present and trims surrounding space.
func linkTargetKey(target string) string {
	return strings.TrimSuffix(strings.TrimSpace(target), ".md")
}

// resolvesTo reports whether a wikilink target points at the note identified by
// basenameNoExt / relpathNoExt. A path-form target (contains "/") matches the
// relpath; a bare target matches the basename. Comparison is case-insensitive,
// matching Obsidian's link resolution ("[[beta]]" resolves to "Beta.md").
func resolvesTo(target, basenameNoExt, relpathNoExt string) bool {
	t := linkTargetKey(target)
	if strings.Contains(t, "/") {
		return strings.EqualFold(t, relpathNoExt)
	}
	return strings.EqualFold(t, basenameNoExt)
}

// RelNoExt returns a vault-relative path without its ".md" extension. It is the
// key used to resolve path-form wikilink targets.
func RelNoExt(rel string) string {
	return strings.TrimSuffix(rel, ".md")
}

// BaseNoExt returns the basename of a vault-relative path without ".md". It is
// the key used to resolve bare wikilink targets.
func BaseNoExt(rel string) string {
	return strings.TrimSuffix(path.Base(rel), ".md")
}

// relNoExt / baseNoExt are the internal short names used within this file.
func relNoExt(rel string) string  { return RelNoExt(rel) }
func baseNoExt(rel string) string { return BaseNoExt(rel) }

// TargetKey normalises a wikilink target into a lookup key and reports whether
// it is a path-form target (contains a slash) or a bare basename. Callers
// resolve a path-form key against RelNoExt keys and a bare key against
// BaseNoExt keys.
func TargetKey(target string) (key string, isPath bool) {
	k := linkTargetKey(target)
	return k, strings.Contains(k, "/")
}

// rewriteLinksForMove rewrites, in body, every wikilink/embed that resolves to
// the note moving from `from` to `to`, pointing it at the destination's vault-
// relative path (without extension) — the always-resolvable form — while
// preserving the embed marker, any #heading/^block and any |display alias.
// It returns the new body and the number of links rewritten.
func rewriteLinksForMove(body, from, to string) (string, int) {
	fromBase := baseNoExt(from)
	fromRel := relNoExt(from)
	toRel := relNoExt(to)

	count := 0
	out := wikilinkRe.ReplaceAllStringFunc(body, func(match string) string {
		m := wikilinkRe.FindStringSubmatch(match)
		bang, inner := m[1], m[2]
		target, heading, display := splitWikilink(inner)
		if target == "" || !resolvesTo(target, fromBase, fromRel) {
			return match
		}
		count++
		return bang + "[[" + buildInner(toRel, heading, display) + "]]"
	})
	return out, count
}

// buildInner reassembles a wikilink's inner content from its parts.
func buildInner(target, heading, display string) string {
	var b strings.Builder
	b.WriteString(target)
	if heading != "" {
		b.WriteString("#")
		b.WriteString(heading)
	}
	if display != "" {
		b.WriteString("|")
		b.WriteString(display)
	}
	return b.String()
}
