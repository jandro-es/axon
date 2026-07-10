package automations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
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

// DeepResearch turns #deep questions with curated seed URLs into fetched
// Knowledge notes + one cited synthesis report (1.3 H2, FR-174…176). Off by
// default; every fetch obeys the profile's existing ingest allow-list.
type DeepResearch struct{}

func (DeepResearch) Name() string    { return "deep-research" }
func (DeepResearch) Essential() bool { return false }

// deepQuestionsFrom reads and parses the questions note, or nil when absent.
func deepQuestionsFrom(ctx context.Context, rc RunCtx) ([]deepQuestion, error) {
	if !rc.Vault.Exists(rqNotePath) {
		return nil, nil
	}
	n, err := rc.Vault.Read(ctx, rqNotePath)
	if err != nil {
		return nil, err
	}
	return parseDeepQuestions(n.Body), nil
}

func (DeepResearch) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	if !rc.Config.Research.Enabled {
		return Change{Changed: false, Reason: "deep research off"}, nil
	}
	qs, err := deepQuestionsFrom(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	if len(qs) == 0 {
		return Change{Changed: false, Reason: "no #deep questions"}, nil
	}
	// Cursor over each question's text + sorted URLs + whether its report exists.
	var sb strings.Builder
	for _, q := range qs {
		urls := append([]string(nil), q.URLs...)
		sort.Strings(urls)
		fmt.Fprintf(&sb, "%s|%s|%t\n", q.Question, strings.Join(urls, ","), rc.Vault.Exists(reportPathFor(q.Question)))
	}
	cursor := hashShort(sb.String())
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "deep questions + reports unchanged"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d #deep question(s)", len(qs)), Cursor: cursor}, nil
}

// Run: the off / no-questions / dry-run cases here; the fetch+synthesis core is
// added in Task 4.
func (DeepResearch) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	if !rc.Config.Research.Enabled {
		return RunResult{Summary: "deep research off"}, nil
	}
	qs, err := deepQuestionsFrom(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	if len(qs) == 0 {
		return RunResult{Summary: "no #deep questions"}, nil
	}
	if rc.DryRun {
		changes := make([]string, 0, len(qs))
		for _, q := range qs {
			changes = append(changes, fmt.Sprintf("%s (would fetch %d source(s))", reportPathFor(q.Question), len(q.URLs)))
		}
		return RunResult{Summary: fmt.Sprintf("would research %d #deep question(s)", len(qs)), Changes: changes}, nil
	}
	// Task 4 implements the fetch + synthesis + report core.
	return RunResult{Summary: fmt.Sprintf("%d #deep question(s)", len(qs))}, nil
}

// reportPathFor is the report note path for a question.
func reportPathFor(question string) string {
	return ResearchDir + "/" + deepSlug(question) + ".md"
}

var deepSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// deepSlug renders a filesystem/link-friendly slug from a question (private copy
// of the ingestion slug behaviour — a 6-line helper, no cross-package import).
func deepSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = deepSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	if s == "" {
		s = "research"
	}
	return s
}
