package ingestion

import (
	"context"
	"regexp"
	"strings"
)

// Enrichment is the metadata produced for a source: a title, a short summary,
// tags and grounded suggested links to existing notes. It also carries the
// accounting for how it was produced so ingestion can surface token usage.
type Enrichment struct {
	Title          string
	Summary        string
	Tags           []string
	SuggestedLinks []string // vault-relative note paths

	// Accounting (set by the enricher): Kind is "heuristic" (no model call) or
	// "claude"; Model and token counts are populated only for a real model call.
	Kind         string
	Model        string
	InputTokens  int
	OutputTokens int
}

// EnrichInput is what an Enricher works from: the extracted title and cleaned
// Markdown, plus related existing notes surfaced by a pre-enrichment hybrid
// search (so link suggestions are grounded, not invented).
type EnrichInput struct {
	Title    string
	Markdown string
	Related  []string // candidate existing-note paths, best-first
}

// Enricher produces source metadata. The Phase 2 default (Heuristic) makes NO
// Claude call; the Claude-backed enricher arrives in Phase 3 routed through the
// token manager (cardinal rule 1). Keeping this an interface is what lets the
// upgrade be a drop-in.
type Enricher interface {
	Enrich(ctx context.Context, in EnrichInput) (Enrichment, error)
}

// Heuristic is a deterministic, no-Claude Enricher: the title is taken as-is,
// the summary is the lead prose, tags are left to the human, and suggested
// links are the grounded related notes. It spends zero tokens.
type Heuristic struct {
	// SummarySentences caps the heuristic summary length (default 3).
	SummarySentences int
	// MaxSuggestions caps suggested links (default 5).
	MaxSuggestions int
}

// Enrich implements Enricher deterministically.
func (h Heuristic) Enrich(ctx context.Context, in EnrichInput) (Enrichment, error) {
	if err := ctx.Err(); err != nil {
		return Enrichment{}, err
	}
	sentences := h.SummarySentences
	if sentences <= 0 {
		sentences = 3
	}
	maxSug := h.MaxSuggestions
	if maxSug <= 0 {
		maxSug = 5
	}
	links := in.Related
	if len(links) > maxSug {
		links = links[:maxSug]
	}
	return Enrichment{
		Title:          strings.TrimSpace(in.Title),
		Summary:        leadSummary(in.Markdown, sentences),
		Tags:           nil, // left to the human / Claude enricher in Phase 3
		SuggestedLinks: links,
		Kind:           "heuristic",
	}, nil
}

// sentenceRe splits on sentence-ending punctuation followed by whitespace.
var sentenceRe = regexp.MustCompile(`(?s)(.+?[.!?])(\s+|$)`)

// leadSummary returns the first n sentences of the first substantive prose
// paragraph (skipping headings, lists, code fences and blockquotes).
func leadSummary(md string, n int) string {
	para := firstProseParagraph(md)
	if para == "" {
		return ""
	}
	matches := sentenceRe.FindAllStringSubmatch(para, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(para)
	}
	var b strings.Builder
	for i, m := range matches {
		if i >= n {
			break
		}
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(m[1]))
	}
	return b.String()
}

// firstProseParagraph returns the first paragraph that looks like prose.
func firstProseParagraph(md string) string {
	for _, p := range splitParagraphs(md) {
		trimmed := strings.TrimSpace(p)
		switch {
		case strings.HasPrefix(trimmed, "#"),
			strings.HasPrefix(trimmed, "- "),
			strings.HasPrefix(trimmed, "* "),
			strings.HasPrefix(trimmed, ">"),
			strings.HasPrefix(trimmed, "```"),
			strings.HasPrefix(trimmed, "|"):
			continue
		}
		// Collapse internal newlines so a wrapped paragraph reads as one line.
		return strings.Join(strings.Fields(trimmed), " ")
	}
	return ""
}

// compile-time assertion that Heuristic satisfies Enricher.
var _ Enricher = Heuristic{}
