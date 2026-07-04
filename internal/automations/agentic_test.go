package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/tokens"
)

func TestRunModelInjectsConfiguredBudget(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.BudgetTokens = 77_000
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "ok", Usage: agent.Usage{InputTokens: 10, OutputTokens: 5}}, nil
	}
	// One-shot: the config budget becomes the pre-flight input cap on the
	// AgentCall; it is not threaded into the Request (no RunBudgetTokens).
	_, _, _, err := runModel(context.Background(), rc, tokens.AgentCall{
		Operation: "t", ModelKey: "routine",
		Messages: []tokens.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.RunBudgetTokens != 0 {
		t.Fatalf("one-shot request carries RunBudgetTokens %d, want 0", got.RunBudgetTokens)
	}
}

func TestRunAgenticThreadsToolsBudgetAndTurns(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.BudgetTokens = 90_000
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "researched", Turns: 4,
			Usage: agent.Usage{InputTokens: 500, OutputTokens: 100}}, nil
	}
	text, _, degraded, err := runAgentic(context.Background(), rc, tokens.AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages: []tokens.Message{{Role: "user", Content: "go"}},
	}, []string{"vault_search", "vault_read"}, 6)
	if err != nil || degraded {
		t.Fatalf("err=%v degraded=%v", err, degraded)
	}
	if text != "researched" {
		t.Fatalf("text = %q", text)
	}
	if len(got.Tools) != 2 || got.MaxTurns != 6 || got.RunBudgetTokens != 90_000 {
		t.Fatalf("request = %+v, want tools/turns/config budget", got)
	}
}

func TestRunAgenticKillDegradesGracefully(t *testing.T) {
	rc, fake := newRC(t, nil)
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Usage: agent.Usage{InputTokens: 999, OutputTokens: 1}},
			agent.ErrRunBudgetExceeded
	}
	_, _, degraded, err := runAgentic(context.Background(), rc, tokens.AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages: []tokens.Message{{Role: "user", Content: "go"}},
	}, []string{"vault_read"}, 4)
	if err != nil {
		t.Fatalf("kill must degrade, not fail: %v", err)
	}
	if !degraded {
		t.Fatal("degraded = false, want true on kill")
	}
}

func TestRunAgenticDryRunMakesNoCall(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.DryRun = true
	_, est, _, err := runAgentic(context.Background(), rc, tokens.AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages: []tokens.Message{{Role: "user", Content: "estimate me"}},
	}, []string{"vault_read"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 0 {
		t.Fatalf("dry-run called the agent %d times", fake.CallCount())
	}
	if est == 0 {
		t.Fatal("dry-run should return the estimate")
	}
}

func TestAgenticEnabled(t *testing.T) {
	rc, _ := newRC(t, nil)
	if !agenticEnabled(rc, "knowledge-digest", true) {
		t.Fatal("default true when config silent")
	}
	f := false
	rc.Config.Automations = map[string]config.Automation{
		"knowledge-digest": {Enabled: true, Agentic: &f},
	}
	if agenticEnabled(rc, "knowledge-digest", true) {
		t.Fatal("explicit agentic:false must win")
	}
}

// ingestTempSource seeds one recent source via the real pipeline.
func ingestTempSource(t *testing.T, rc RunCtx) {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "src.md")
	if err := os.WriteFile(f, []byte("# Source\n\ncontent about embeddings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Pipeline.Ingest(context.Background(), f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeDigestAgenticPath(t *testing.T) {
	rc, fake := newRC(t, nil)
	ingestTempSource(t, rc)
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "## Themes\n- theme", Turns: 3,
			Usage: agent.Usage{InputTokens: 400, OutputTokens: 80}}, nil
	}
	res, err := (KnowledgeDigest{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 3 || got.MaxTurns != 8 {
		t.Fatalf("digest request tools=%v turns=%d, want 3 tools / 8 turns", got.Tools, got.MaxTurns)
	}
	if !strings.Contains(strings.Join(got.Tools, ","), "knowledge_search") {
		t.Fatalf("tools = %v", got.Tools)
	}
	if !strings.Contains(res.Summary, "agentic") {
		t.Fatalf("summary = %q, want agentic marker", res.Summary)
	}
}

func TestKnowledgeDigestAgenticFalseFallsBack(t *testing.T) {
	rc, fake := newRC(t, nil)
	ingestTempSource(t, rc)
	f := false
	rc.Config.Automations = map[string]config.Automation{
		"knowledge-digest": {Enabled: true, Agentic: &f},
	}
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "digest text"}, nil
	}
	if _, err := (KnowledgeDigest{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 0 {
		t.Fatalf("agentic:false must run one-shot, got tools %v", got.Tools)
	}
}

func TestKnowledgeDigestDegradesToOneShot(t *testing.T) {
	rc, fake := newRC(t, nil)
	ingestTempSource(t, rc)
	calls := 0
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		calls++
		if len(r.Tools) > 0 {
			return &agent.Response{Usage: agent.Usage{InputTokens: 999}}, agent.ErrRunBudgetExceeded
		}
		return &agent.Response{Text: "fallback digest"}, nil
	}
	res, err := (KnowledgeDigest{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want agentic attempt + one-shot fallback", calls)
	}
	if !strings.Contains(res.Summary, "degraded") {
		t.Fatalf("summary = %q, want degraded marker", res.Summary)
	}
}

func TestCompactionAgenticTools(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"01-Projects/big.md": "# Big\n\n" + strings.Repeat("word ", 50),
	})
	mustReindex(t, rc)
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "- bullet summary"}, nil
	}
	if _, err := (Compaction{WordThreshold: 5}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 3 || got.MaxTurns != 4 {
		t.Fatalf("compaction request tools=%v turns=%d, want [vault_read vault_links vault_patch] / 4", got.Tools, got.MaxTurns)
	}
	var hasPatch bool
	for _, tn := range got.Tools {
		if tn == "vault_patch" {
			hasPatch = true
		}
	}
	if !hasPatch {
		t.Fatalf("compaction agentic path must include vault_patch (ADR-022); got %v", got.Tools)
	}
}
