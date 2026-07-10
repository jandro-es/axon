package automations

import (
	"reflect"
	"testing"
)

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
