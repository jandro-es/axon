package automations

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jandro-es/axon/internal/ask"
)

const (
	rqNotePath     = "03-Resources/Research Questions.md"
	rqAnswersBlock = "answers"
	rqMarkerStart  = "<!-- axon:answers:start -->"
)

// rqResult pairs a question with the grounded answer it produced this run.
type rqResult struct {
	Question string
	Answer   ask.Answer
}

// rqItemRe matches a top-level markdown list item, capturing an optional
// checkbox and the item text. Leading indentation excludes nested items.
var rqItemRe = regexp.MustCompile(`^[-*] +(?:\[[ xX]\] +)?(.*\S)\s*$`)

// parseQuestions extracts standing questions from the note's HUMAN region: the
// body above the axon:answers marker. A question is a top-level list item whose
// text ends with '?'. AXON's own answer block is never re-parsed.
func parseQuestions(body string) []string {
	human := body
	if i := strings.Index(body, rqMarkerStart); i >= 0 {
		human = body[:i]
	}
	var out []string
	for _, line := range strings.Split(human, "\n") {
		m := rqItemRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[1])
		if strings.HasSuffix(text, "?") {
			out = append(out, text)
		}
	}
	return out
}

// confidenceMarker derives a coarse confidence from the answer, with no extra
// model call: refused → Open, 1 citation → Tentative, ≥2 → Answered.
func confidenceMarker(a ask.Answer) string {
	switch {
	case a.Refused || a.Text == "":
		return "🔍 **Open**"
	case len(a.Citations) >= 2:
		return "✅ **Answered**"
	default:
		return "📝 **Tentative**"
	}
}

// renderAnswers builds the axon:answers block content: one entry per question
// in order, plus a footer with counts. The block is rebuilt whole each run.
func renderAnswers(results []rqResult, weekLabel string) string {
	var b strings.Builder
	answered := 0
	for _, r := range results {
		fmt.Fprintf(&b, "### %s\n", r.Question)
		if r.Answer.Refused || r.Answer.Text == "" {
			b.WriteString("🔍 **Open** — no grounded answer in the vault yet; will re-attempt next week.\n\n")
			continue
		}
		answered++
		cites := make([]string, len(r.Answer.Citations))
		for i, c := range r.Answer.Citations {
			cites[i] = "[[" + c + "]]"
		}
		fmt.Fprintf(&b, "%s · sources: %s\n\n%s\n\n", confidenceMarker(r.Answer), strings.Join(cites, ", "), strings.TrimSpace(r.Answer.Text))
	}
	fmt.Fprintf(&b, "_Updated %s · %d answered · %d open_", weekLabel, answered, len(results)-answered)
	return b.String()
}
