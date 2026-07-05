package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/ask"
	"github.com/jandro-es/axon/internal/config"
)

// rqFiller gives the tiny test index a realistic bm25 IDF so a genuine lexical
// match scores with strength (FTS5 IDF is ~0 in a single-document index).
var rqFiller = map[string]string{
	"Notes/f1.md": "# Gardening\n\nTomatoes need full sun and regular watering through summer.\n",
	"Notes/f2.md": "# Cooking\n\nSlow braising tough cuts renders collagen into gelatin.\n",
	"Notes/f3.md": "# Travel\n\nShoulder season flights cost less and queues are shorter.\n",
	"Notes/f4.md": "# Fitness\n\nProgressive overload drives strength adaptation over weeks.\n",
}

func rqFiles(extra map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range rqFiller {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestParseQuestions(t *testing.T) {
	body := `# Research Questions

Some intro prose — not a question? still prose because it is not a list item.

- How does spaced repetition work?
- [ ] What did I decide about SQLite vs Postgres?
- [x] Already answered but still re-checked?
- a plain bullet with no question mark
* Star bullet question here?

<!-- axon:answers:start -->
### Old answer? this line is inside the block and must be ignored
<!-- axon:answers:end -->
`
	got := parseQuestions(body)
	want := []string{
		"How does spaced repetition work?",
		"What did I decide about SQLite vs Postgres?",
		"Already answered but still re-checked?",
		"Star bullet question here?",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d questions %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("q[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseQuestionsEmpty(t *testing.T) {
	if q := parseQuestions("# Research Questions\n\nno list items here.\n"); len(q) != 0 {
		t.Fatalf("want 0 questions, got %v", q)
	}
}

// The scaffolded template's example questions are fenced and must NOT parse as
// live questions — otherwise the inert template would spend tokens once enabled.
func TestParseQuestionsIgnoresFenced(t *testing.T) {
	body := "# Research Questions\n\nExamples:\n\n```\n- What did I conclude about spaced repetition?\n- How do notes connect?\n```\n\n<!-- axon:answers:start -->\n<!-- axon:answers:end -->\n"
	if q := parseQuestions(body); len(q) != 0 {
		t.Fatalf("fenced examples parsed as live questions: %v", q)
	}
}

func TestRenderAnswers(t *testing.T) {
	results := []rqResult{
		{Question: "What is X?", Answer: ask.Answer{Text: "X is a thing.", Citations: []string{"a/b", "c/d"}}},
		{Question: "What is Y?", Answer: ask.Answer{Text: "Y is one thing.", Citations: []string{"e/f"}}},
		{Question: "What is Z?", Answer: ask.Answer{Refused: true}},
	}
	out := renderAnswers(results, "2026-07-06")
	if !strings.Contains(out, "### What is X?") || !strings.Contains(out, "✅ **Answered**") ||
		!strings.Contains(out, "[[a/b]], [[c/d]]") {
		t.Fatalf("answered entry malformed:\n%s", out)
	}
	if !strings.Contains(out, "📝 **Tentative**") {
		t.Fatalf("tentative (1 citation) entry missing:\n%s", out)
	}
	if !strings.Contains(out, "### What is Z?") || !strings.Contains(out, "🔍 **Open**") {
		t.Fatalf("open entry missing:\n%s", out)
	}
	if !strings.Contains(out, "_Updated 2026-07-06 · 2 answered · 1 open_") {
		t.Fatalf("footer wrong:\n%s", out)
	}
}

func TestResearchQuestionsAbsentNoteNoOp(t *testing.T) {
	rc, _ := newRC(t, rqFiles(nil)) // no research-questions note
	ch, err := ResearchQuestions{}.DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Changed {
		t.Fatalf("absent note should not be a change: %+v", ch)
	}
	res, err := ResearchQuestions{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run on absent note errored: %v", err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("absent note run should write nothing: %+v", res)
	}
}

func TestResearchQuestionsAnswersAndPreservesHuman(t *testing.T) {
	note := "# Research Questions\n\n- What is spaced repetition?\n- What is quantum flavour physics?\n\n<!-- axon:answers:start -->\n<!-- axon:answers:end -->\n"
	rc, fake := newRC(t, rqFiles(map[string]string{
		rqNotePath: note,
		"03-Resources/Knowledge/Spaced Repetition.md": "---\ntitle: Spaced Repetition\n---\n\nSpaced repetition schedules reviews at increasing intervals to fight forgetting.\n",
	}))
	rc.Config.Retrieval = config.RetrievalConfig{TopK: 8, MaxContextTokens: 12_000}
	mustReindex(t, rc)
	// The quantum question has no answering note → NOT_FOUND (grounded refusal);
	// the spaced-repetition question gets a cited answer. Key off the exact
	// "QUESTION:" line ask appends (the Research Questions note itself is indexed,
	// so both questions leak into each other's retrieved context — match the
	// question, not the context).
	fake.RespondFn = func(req agent.Request) (*agent.Response, error) {
		if strings.Contains(req.Prompt, "QUESTION: What is quantum flavour physics?") {
			return &agent.Response{Text: "NOT_FOUND", Model: req.Model}, nil
		}
		return &agent.Response{Text: "Spaced repetition schedules reviews at increasing intervals. [[03-Resources/Knowledge/Spaced Repetition]]", Model: req.Model}, nil
	}

	if _, err := (ResearchQuestions{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	out, err := rc.Vault.Read(context.Background(), rqNotePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Body, "- What is spaced repetition?") ||
		!strings.Contains(out.Body, "- What is quantum flavour physics?") {
		t.Fatalf("human questions altered:\n%s", out.Body)
	}
	if !strings.Contains(out.Body, "### What is spaced repetition?") ||
		!strings.Contains(out.Body, "[[03-Resources/Knowledge/Spaced Repetition]]") {
		t.Fatalf("grounded answer missing:\n%s", out.Body)
	}
	if !strings.Contains(out.Body, "### What is quantum flavour physics?") ||
		!strings.Contains(out.Body, "🔍 **Open**") {
		t.Fatalf("open entry missing:\n%s", out.Body)
	}
}

func TestResearchQuestionsDryRunWritesNothing(t *testing.T) {
	note := "# RQ\n\n- What is X?\n\n<!-- axon:answers:start -->\n<!-- axon:answers:end -->\n"
	rc, _ := newRC(t, rqFiles(map[string]string{rqNotePath: note}))
	rc.Config.Retrieval = config.RetrievalConfig{TopK: 8, MaxContextTokens: 12_000}
	mustReindex(t, rc)
	rc.DryRun = true
	res, err := ResearchQuestions{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "would answer") {
		t.Fatalf("dry-run summary = %q", res.Summary)
	}
	out, _ := rc.Vault.Read(context.Background(), rqNotePath)
	if strings.Contains(out.Body, "###") {
		t.Fatalf("dry-run wrote answers:\n%s", out.Body)
	}
}

func TestResearchQuestionsRegistered(t *testing.T) {
	if _, err := Get(config.Profile{}, "research-questions"); err != nil {
		t.Fatalf("research-questions not registered: %v", err)
	}
}
