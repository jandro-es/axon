package automations

import (
	"net/url"
	"regexp"
	"strings"
)

// ResearchDir is where deep-research report notes are written.
const ResearchDir = "03-Resources/Research"

// deepTag marks a research question as deep (web-sourced).
const deepTag = "#deep"

// deepQuestion is one #deep question and its curated seed URLs, in order.
type deepQuestion struct {
	Question string
	URLs     []string
}

// deepTopItemRe matches a top-level (unindented) markdown list item.
var deepTopItemRe = regexp.MustCompile(`^[-*] +(.*\S)\s*$`)

// deepNestedItemRe matches an indented list item (a seed under a question).
var deepNestedItemRe = regexp.MustCompile(`^\s+[-*] +(.*\S)\s*$`)

// parseDeepQuestions extracts #deep questions and their nested seed URLs from
// the note's HUMAN region (above the research-questions axon:answers marker; the
// axon:deep pointer block this automation writes is below that marker too and is
// never re-parsed). A deep question is a top-level list item containing #deep
// whose text (tag removed) ends with '?'. Its seeds are the immediately
// following indented items that parse as http(s) URLs.
func parseDeepQuestions(body string) []deepQuestion {
	human := body
	if i := strings.Index(body, rqMarkerStart); i >= 0 {
		human = body[:i]
	}
	var out []deepQuestion
	var cur *deepQuestion
	inFence := false
	for _, line := range strings.Split(human, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := deepNestedItemRe.FindStringSubmatch(line); m != nil && cur != nil {
			link := strings.TrimSpace(m[1])
			if u, err := url.Parse(link); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
				if !containsStr(cur.URLs, link) {
					cur.URLs = append(cur.URLs, link)
				}
			}
			continue
		}
		if m := deepTopItemRe.FindStringSubmatch(line); m != nil {
			// A new top-level item closes any question in progress.
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			text := strings.TrimSpace(m[1])
			idx := strings.Index(text, deepTag)
			if idx < 0 {
				continue
			}
			// Accept only if it reads as a question once the tag is removed.
			cleaned := strings.TrimSpace(strings.ReplaceAll(text, deepTag, ""))
			if !strings.HasSuffix(cleaned, "?") {
				continue
			}
			// The question is the text before the tag; fall back to the cleaned
			// text (sans trailing '?') when the tag leads the line.
			q := strings.TrimSpace(text[:idx])
			if q == "" {
				q = strings.TrimSpace(strings.TrimSuffix(cleaned, "?"))
			}
			cur = &deepQuestion{Question: q}
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// containsStr reports whether s is in xs.
func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
