package automations

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func deepResearchNote(body string) map[string]string {
	return map[string]string{"03-Resources/Research Questions.md": body}
}

func TestParseDeepQuestions(t *testing.T) {
	body := "" +
		"# Research Questions\n\n" +
		"- A normal vault question?\n" +
		"- How does RAG reranking affect latency? #deep\n" +
		"    - https://arxiv.org/abs/2312.001\n" +
		"    - https://blog.vespa.ai/x\n" +
		"    - not-a-url\n" +
		"    - https://arxiv.org/abs/2312.001\n" + // duplicate
		"- Another deep one #deep ?\n" +
		"    - https://example.com/a\n" +
		"```\n- Fenced #deep\n    - https://nope.example\n```\n" +
		"<!-- axon:answers:start -->\n- Below marker #deep\n    - https://ignored.example\n"

	got := parseDeepQuestions(body)
	want := []deepQuestion{
		{Question: "How does RAG reranking affect latency?", URLs: []string{"https://arxiv.org/abs/2312.001", "https://blog.vespa.ai/x"}},
		{Question: "Another deep one", URLs: []string{"https://example.com/a"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDeepQuestions:\n got %#v\nwant %#v", got, want)
	}
}

func TestDeepResearchRegisteredAndInertWhenOff(t *testing.T) {
	if _, err := Get(config.Profile{}, "deep-research"); err != nil {
		t.Fatalf("deep-research not registered: %v", err)
	}
	rc, _ := newRC(t, deepResearchNote("- Q? #deep\n    - https://example.com/a\n"))
	// research.enabled defaults false → inert.
	res, err := (DeepResearch{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "off") {
		t.Fatalf("summary = %q, want off marker", res.Summary)
	}
}

func TestDeepResearchDetectChange(t *testing.T) {
	rc, _ := newRC(t, deepResearchNote("- Q? #deep\n    - https://example.com/a\n"))
	rc.Config.Research = config.ResearchConfig{Enabled: true}

	off, _ := newRC(t, deepResearchNote("- Q? #deep\n    - https://example.com/a\n"))
	if ch, _ := (DeepResearch{}).DetectChange(context.Background(), off); ch.Changed {
		t.Fatal("must be unchanged when research is off")
	}

	ch, err := (DeepResearch{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first run should be changed with a cursor: %+v", ch)
	}
	// Same cursor replayed → unchanged.
	rc.LastCursor = ch.Cursor
	if ch2, _ := (DeepResearch{}).DetectChange(context.Background(), rc); ch2.Changed {
		t.Fatalf("unchanged inputs should not re-fire: %+v", ch2)
	}
}
