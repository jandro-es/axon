package automations

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/ask"
)

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
