package automations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/ask"
	"github.com/jandro-es/axon/internal/db"
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

// ResearchQuestions answers user-authored standing questions from the whole
// vault (A3, FR-116/117): grounded ask per question, rendered into the
// axon:answers managed block. Feature is off until the note exists with a
// question; the human region is never edited (cardinal rule 2).
type ResearchQuestions struct{}

func (ResearchQuestions) Name() string    { return "research-questions" }
func (ResearchQuestions) Essential() bool { return false }

func (ResearchQuestions) questions(ctx context.Context, rc RunCtx) ([]string, error) {
	if !rc.Vault.Exists(rqNotePath) {
		return nil, nil
	}
	n, err := rc.Vault.Read(ctx, rqNotePath)
	if err != nil {
		return nil, err
	}
	return parseQuestions(n.Body), nil
}

func (r ResearchQuestions) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	qs, err := r.questions(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	if len(qs) == 0 {
		return Change{Changed: false, Reason: "no research questions"}, nil
	}
	since := weekStart(rc).Format(time.RFC3339)
	n, err := db.CountSourcesSince(ctx, rc.DB, since)
	if err != nil {
		return Change{}, err
	}
	sum := sha256.Sum256([]byte(strings.Join(qs, "\n")))
	cursor := fmt.Sprintf("%s:%s:%d", hex.EncodeToString(sum[:8]), weekStart(rc).Format("2006-01-02"), n)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "questions + sources unchanged"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d question(s), %d source(s) this week", len(qs), n), Cursor: cursor}, nil
}

func (r ResearchQuestions) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	qs, err := r.questions(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	if len(qs) == 0 {
		return RunResult{Summary: "no research questions (feature inactive)"}, nil
	}
	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would answer %d research question(s)", len(qs)), Changes: []string{rqNotePath}}, nil
	}

	deps := ask.Deps{Searcher: rc.Searcher, Manager: rc.Manager, Config: rc.Config}
	results := make([]rqResult, 0, len(qs))
	answered, est := 0, 0
	for _, q := range qs {
		a, aerr := ask.Ask(ctx, deps, q, 0)
		if aerr != nil {
			// Unexpected transport error: treat as open, keep going.
			a = ask.Answer{Refused: true, Reason: aerr.Error()}
		}
		est += a.Tokens
		if !a.Refused && a.Text != "" {
			answered++
		}
		results = append(results, rqResult{Question: q, Answer: a})
	}

	block := renderAnswers(results, weekStart(rc).Format("2006-01-02"))
	if err := rc.Vault.Patch(ctx, rqNotePath, rqAnswersBlock, block); err != nil {
		return RunResult{}, err
	}
	return RunResult{
		Summary:         fmt.Sprintf("answered %d/%d research question(s)", answered, len(qs)),
		Changes:         []string{rqNotePath},
		EstimatedTokens: est,
	}, nil
}
