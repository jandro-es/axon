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

func TestDeepResearchProducesReport(t *testing.T) {
	rc, fake := newRC(t, deepResearchNote(
		"# Research Questions\n\n- How does X work? #deep\n    - https://example.com/a\n    - https://example.com/b\n"))
	rc.Config.Research = config.ResearchConfig{Enabled: true}
	fake.Reply = "X works by combining [[a]] and [[b]] into one pipeline."

	f := newURLFetcher()
	f.addHTML("https://example.com/a", "Source A")
	f.addHTML("https://example.com/b", "Source B")
	rc.Pipeline.Fetcher = f
	ctx := context.Background()

	res, err := (DeepResearch{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedTokens == 0 {
		t.Fatalf("expected synthesis token estimate > 0; summary=%q", res.Summary)
	}
	// Two sources fetched.
	if f.calls["https://example.com/a"] != 1 || f.calls["https://example.com/b"] != 1 {
		t.Fatalf("fetch counts wrong: %v", f.calls)
	}
	// Report note exists with the axon:report block + a Sources list.
	reportPath := reportPathFor("How does X work?")
	if !rc.Vault.Exists(reportPath) {
		t.Fatalf("report not written at %s", reportPath)
	}
	n, _ := rc.Vault.Read(ctx, reportPath)
	if !strings.Contains(n.Body, "axon:report:start") {
		t.Fatal("report missing managed block")
	}
	if !strings.Contains(n.Body, "**Sources**") || !strings.Contains(n.Body, "[[03-Resources/Knowledge/") {
		t.Fatalf("report missing deterministic sources list:\n%s", n.Body)
	}
	// Pointer block written into the questions note.
	qn, _ := rc.Vault.Read(ctx, rqNotePath)
	if !strings.Contains(qn.Body, "axon:deep:start") || !strings.Contains(qn.Body, "[[03-Resources/Research/") {
		t.Fatalf("questions note missing axon:deep pointer:\n%s", qn.Body)
	}
}

func TestDeepResearchDeniedDomainNeverFetched(t *testing.T) {
	rc, fake := newRC(t, deepResearchNote(
		"- Q about Y? #deep\n    - https://allowed.example/a\n    - https://denied.example/b\n"))
	rc.Config.Research = config.ResearchConfig{Enabled: true}
	fake.Reply = "A grounded answer citing [[a]]."
	// Restrict the pipeline egress: allow one host, deny the rest.
	rc.Pipeline.Policy = config.PolicyConfig{
		EgressAllowlist:    []string{"*"},
		IngestDomainsAllow: []string{"allowed.example"},
		IngestDomainsDeny:  []string{"*"},
	}
	f := newURLFetcher()
	f.addHTML("https://allowed.example/a", "Allowed A")
	f.addHTML("https://denied.example/b", "Denied B")
	rc.Pipeline.Fetcher = f

	if _, err := (DeepResearch{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if f.calls["https://denied.example/b"] != 0 {
		t.Fatalf("denied host was fetched %d time(s); must be zero", f.calls["https://denied.example/b"])
	}
	if f.calls["https://allowed.example/a"] != 1 {
		t.Fatalf("allowed host fetch count = %d, want 1", f.calls["https://allowed.example/a"])
	}
}

func TestDeepResearchOffMakesNoCalls(t *testing.T) {
	rc, _ := newRC(t, deepResearchNote("- Q? #deep\n    - https://example.com/a\n"))
	// research.enabled defaults false.
	f := newURLFetcher()
	f.addHTML("https://example.com/a", "A")
	rc.Pipeline.Fetcher = f
	if _, err := (DeepResearch{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if f.calls["https://example.com/a"] != 0 {
		t.Fatal("research off must make zero fetches")
	}
}
