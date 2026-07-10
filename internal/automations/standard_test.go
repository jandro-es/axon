package automations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

var fixedNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

// newRC builds a RunCtx wired with real components and fakes for Claude/Ollama,
// for testing the standard automations directly.
func newRC(t *testing.T, files map[string]string) (RunCtx, *agent.Fake) {
	t.Helper()
	vdir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(vdir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	fake := agent.NewFake()
	emb := embeddings.NewFake()
	searcher := search.New(d, emb)
	v := vault.NewFS(vdir)
	profile := config.Profile{
		Models: config.ModelsConfig{Classify: "haiku", Routine: "sonnet", Synthesis: "opus"},
		Limits: genLimits(),
	}
	mgr := tokens.New(d, fake, searcher, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription", Models: profile.Models, Limits: profile.Limits,
	})
	pipeline := &ingestion.Pipeline{
		Vault: v, DB: d, Embedder: emb, Enricher: ingestion.Heuristic{},
		Fetcher: ingestion.NewHTTPFetcher(config.PolicyConfig{}), Profile: "test",
	}
	// A real run row so model automations can ledger against it (the engine
	// opens one before invoking an automation; mirror that here).
	runID, err := db.InsertRun(context.Background(), d, "test", fixedNow.Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	rc := RunCtx{
		Profile: "test", Config: profile, DB: d, Vault: v, Manager: mgr,
		Searcher: searcher, Embedder: emb, Pipeline: pipeline, RunID: runID,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return fixedNow },
	}
	return rc, fake
}

func TestBudgetGuard(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	ch, _ := BudgetGuard{}.DetectChange(ctx, rc)
	if !ch.Changed {
		t.Error("budget-guard should always run")
	}
	res, err := BudgetGuard{}.Run(ctx, rc)
	if err != nil || !strings.Contains(res.Summary, "budget ok") {
		t.Errorf("budget-guard run = %+v, %v", res, err)
	}
	if !(BudgetGuard{}).Essential() {
		t.Error("budget-guard must be essential")
	}
}

func TestKnowledgeReindexChangeGate(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"01-Projects/a.md": "links [[b]]\n", "b.md": "hi\n"})
	ctx := context.Background()
	a := KnowledgeReindex{}

	ch, err := a.DetectChange(ctx, rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first DetectChange = %+v, %v", ch, err)
	}
	res, err := a.Run(ctx, rc)
	if err != nil || !strings.Contains(res.Summary, "indexed") {
		t.Fatalf("run = %+v, %v", res, err)
	}
	// With the cursor persisted, an unchanged vault should not re-run.
	rc.LastCursor = ch.Cursor
	ch2, _ := a.DetectChange(ctx, rc)
	if ch2.Changed {
		t.Error("knowledge-reindex should skip when the vault is unchanged")
	}
}

func TestContextExportWritesBundle(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"01-Projects/p.md": "a project\n"})
	ctx := context.Background()
	res, err := ContextExport{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 {
		t.Errorf("expected manifest + core-context, got %v", res.Changes)
	}
	if !rc.Vault.Exists(res.Changes[0]) {
		t.Errorf("export bundle file %q not written", res.Changes[0])
	}
}

func TestHeartbeatWritesDailyNote(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"00-Inbox/thought.md": "an idea"})
	ctx := context.Background()
	res, err := Heartbeat{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "inbox: 1") {
		t.Errorf("heartbeat summary = %q", res.Summary)
	}
	n, err := rc.Vault.Read(ctx, "Daily/2026-06-28.md")
	if err != nil {
		t.Fatalf("daily note not created: %v", err)
	}
	if !strings.Contains(n.Body, "axon:heartbeat:start") {
		t.Errorf("heartbeat block not written:\n%s", n.Body)
	}
}

func TestDailyLogChangeGate(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	// No daily note today -> no activity -> skip.
	ch, _ := DailyLog{}.DetectChange(ctx, rc)
	if ch.Changed {
		t.Error("daily-log should skip when there is no daily note")
	}
	// Create today's note -> changed.
	_, _ = rc.Vault.Create("Daily/2026-06-28.md", "---\ntype: daily\n---\n## Log\n- shipped\n")
	ch, _ = DailyLog{}.DetectChange(ctx, rc)
	if !ch.Changed {
		t.Error("daily-log should run when today's note has content")
	}
}

func TestInboxTriageProposesToReviewQueue(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/idea.md": "a half-formed project idea about vectors"})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: `{"folder":"01-Projects","tags":["vectors"]}`, Model: r.Model, Usage: agent.Usage{InputTokens: 30, OutputTokens: 8}}, nil
	}
	ctx := context.Background()

	ch, _ := InboxTriage{}.DetectChange(ctx, rc)
	if !ch.Changed {
		t.Fatal("inbox-triage should detect the inbox item")
	}
	res, err := InboxTriage{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Error("expected a triage proposal")
	}
	if !rc.Vault.Exists(".axon/review-queue.md") {
		t.Error("triage proposals not written to the review queue")
	}
	if fake.CallCount() != 1 {
		t.Errorf("expected one classify call, got %d", fake.CallCount())
	}
}

func TestCompactionDistilsOversizedNote(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"01-Projects/long.md": "# Long\n\n" + strings.Repeat("word ", 50),
	})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- distilled point", Model: r.Model, Usage: agent.Usage{InputTokens: 60, OutputTokens: 12}}, nil
	}
	ctx := context.Background()
	// Index so word_count is populated.
	mustReindex(t, rc)

	c := Compaction{WordThreshold: 5}
	ch, err := c.DetectChange(ctx, rc)
	if err != nil || !ch.Changed {
		t.Fatalf("compaction DetectChange = %+v, %v", ch, err)
	}
	res, err := c.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Fatal("compaction wrote nothing")
	}
	n, _ := rc.Vault.Read(ctx, "01-Projects/long.md")
	if !strings.Contains(n.Body, "axon:summary:start") {
		t.Errorf("compaction did not write a summary block:\n%s", n.Body)
	}
}

func TestKnowledgeDigestGatesOnNewSources(t *testing.T) {
	rc, fake := newRC(t, nil)
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "Weekly themes: vectors, search.", Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 10}}, nil
	}
	ctx := context.Background()

	// No sources yet -> skip.
	ch, _ := KnowledgeDigest{}.DetectChange(ctx, rc)
	if ch.Changed {
		t.Error("knowledge-digest should skip with no new sources")
	}
	// Ingest a source this week.
	dir := t.TempDir()
	f := filepath.Join(dir, "s.md")
	_ = os.WriteFile(f, []byte("# Source\n\ncontent about embeddings\n"), 0o644)
	if _, err := rc.Pipeline.Ingest(ctx, f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
		t.Fatal(err)
	}
	ch, _ = KnowledgeDigest{}.DetectChange(ctx, rc)
	if !ch.Changed {
		t.Fatal("knowledge-digest should run after a new source")
	}
	res, err := KnowledgeDigest{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 || !strings.HasPrefix(res.Changes[0], "MOCs/") {
		t.Errorf("expected a digest note under MOCs/, got %v", res.Changes)
	}
}

func TestLinkSuggesterEndToEnd(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	dir := t.TempDir()
	// Ingest two related sources (real pipeline: writes notes + chunks + vectors).
	for name, body := range map[string]string{
		"a.md": "# Vector Databases\n\nVector databases index embeddings for similarity search.\n",
		"b.md": "# Semantic Search\n\nSemantic search uses embeddings and vector databases.\n",
	} {
		f := filepath.Join(dir, name)
		_ = os.WriteFile(f, []byte(body), 0o644)
		if _, err := rc.Pipeline.Ingest(ctx, f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
			t.Fatal(err)
		}
	}
	mustReindex(t, rc)

	ch, err := LinkSuggester{}.DetectChange(ctx, rc)
	if err != nil || !ch.Changed {
		t.Fatalf("link-suggester DetectChange = %+v, %v", ch, err)
	}
	res, err := LinkSuggester{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Error("expected at least one link suggestion between the related notes")
	}
}

// mustReindex rebuilds the notes mirror so DB-backed automations see vault state.
func mustReindex(t *testing.T, rc RunCtx) {
	t.Helper()
	if _, err := (KnowledgeReindex{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
}

// TestLinkSuggesterProposalMemory proves a proposed pair is never re-queued
// (FR-102): the second run finds nothing new, and the memory row persists.
func TestLinkSuggesterProposalMemory(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.md": "# Vector Databases\n\nVector databases index embeddings for similarity search.\n",
		"b.md": "# Semantic Search\n\nSemantic search uses embeddings and vector databases.\n",
	} {
		f := filepath.Join(dir, name)
		_ = os.WriteFile(f, []byte(body), 0o644)
		if _, err := rc.Pipeline.Ingest(ctx, f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
			t.Fatal(err)
		}
	}
	mustReindex(t, rc)

	res, err := LinkSuggester{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Fatal("first run should propose at least one link")
	}
	raw, err := db.GetCursor(ctx, rc.DB, "link-suggester:proposed")
	if err != nil || raw == "" {
		t.Fatalf("proposal memory not persisted: %q, %v", raw, err)
	}

	res2, err := LinkSuggester{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Changes) != 0 {
		t.Fatalf("second run re-proposed: %v", res2.Changes)
	}
}

// TestLinkSuggesterDryRunPersistsNothing: dry-run proposes but leaves no
// memory row, so a later real run still queues the pairs.
func TestLinkSuggesterDryRunPersistsNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.md": "# Vector Databases\n\nVector databases index embeddings for similarity search.\n",
		"b.md": "# Semantic Search\n\nSemantic search uses embeddings and vector databases.\n",
	} {
		f := filepath.Join(dir, name)
		_ = os.WriteFile(f, []byte(body), 0o644)
		if _, err := rc.Pipeline.Ingest(ctx, f, ingestion.IngestOptions{AllowLocalFiles: true}); err != nil {
			t.Fatal(err)
		}
	}
	mustReindex(t, rc)

	rc.DryRun = true
	if _, err := (LinkSuggester{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if raw, _ := db.GetCursor(ctx, rc.DB, "link-suggester:proposed"); raw != "" {
		t.Fatalf("dry-run persisted memory: %q", raw)
	}
}

// TestProposalMemoryCap: the shared helper keeps at most proposalMemoryCap
// keys (lexicographic tail, matching the resurfacer's existing behaviour).
func TestProposalMemoryCap(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	m := map[string]bool{}
	for i := 0; i < proposalMemoryCap+50; i++ {
		m[fmt.Sprintf("pair-%04d", i)] = true
	}
	saveProposalMemory(ctx, rc, "test:proposed", m)
	got := loadProposalMemory(ctx, rc, "test:proposed")
	if len(got) != proposalMemoryCap {
		t.Fatalf("cap not enforced: got %d keys", len(got))
	}
}

// TestInboxTriageEmptyReplyFails proves the ADR-015 output validator turns an
// empty classification into a failed run instead of a silent empty queue line.
func TestInboxTriageEmptyReplyFails(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/item.md": "capture me\n"})
	fake.Reply = ""
	if _, err := (InboxTriage{}).Run(context.Background(), rc); err == nil {
		t.Fatal("empty model reply should fail the triage run")
	}
}

// TestHeartbeatSynthesis covers the docs/06 optional one-liner: toggle via
// TestHeartbeatTaskCounter: the heartbeat line reflects open/overdue actions
// from the derived index (FR-161).
func TestHeartbeatTaskCounter(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{})
	if err := db.ReplaceActions(ctx, rc.DB, []db.Action{
		{Hash: "h1", SourcePath: "a.md", Text: "overdue", State: "open", Checkbox: " ", Due: "2000-01-01", Updated: "u"},
		{Hash: "h2", SourcePath: "a.md", Text: "future", State: "open", Checkbox: " ", Due: "2999-01-01", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := Heartbeat{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "tasks: 2 open (1 overdue)") {
		t.Errorf("heartbeat line missing task counter: %q", res.Summary)
	}
}

// automations.heartbeat.model, deterministic noteworthy gate, absolute
// degradation (defer/error → plain line, run stays ok).
func TestHeartbeatSynthesis(t *testing.T) {
	plain := "inbox: 1 · tasks: 0 open · budget day 0% week 0%"

	t.Run("toggle off: zero calls, plain line", func(t *testing.T) {
		rc, fake := newRC(t, map[string]string{"00-Inbox/x.md": "item\n"})
		res, err := (Heartbeat{}).Run(context.Background(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if fake.CallCount() != 0 {
			t.Fatalf("toggle off must make no model call, got %d", fake.CallCount())
		}
		if !strings.Contains(res.Summary, plain) {
			t.Fatalf("summary = %q", res.Summary)
		}
	})

	t.Run("toggle on, nothing noteworthy: zero calls", func(t *testing.T) {
		rc, fake := newRC(t, nil) // empty inbox, no queue, no guard
		rc.Config.Automations = map[string]config.Automation{"heartbeat": {Model: "classify"}}
		if _, err := (Heartbeat{}).Run(context.Background(), rc); err != nil {
			t.Fatal(err)
		}
		if fake.CallCount() != 0 {
			t.Fatalf("nothing noteworthy must make no model call, got %d", fake.CallCount())
		}
	})

	t.Run("toggle on + noteworthy: synthesized second line", func(t *testing.T) {
		rc, fake := newRC(t, map[string]string{"00-Inbox/x.md": "item\n"})
		rc.Config.Automations = map[string]config.Automation{"heartbeat": {Model: "classify"}}
		fake.Reply = "One inbox item needs triage."
		res, err := (Heartbeat{}).Run(context.Background(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if fake.CallCount() != 1 {
			t.Fatalf("want exactly one call, got %d", fake.CallCount())
		}
		n, _ := rc.Vault.Read(context.Background(), "Daily/"+today(rc)+".md")
		if !strings.Contains(n.Body, "One inbox item needs triage.") {
			t.Fatalf("synthesis missing from block:\n%s", n.Body)
		}
		if !strings.Contains(res.Summary, "One inbox item needs triage.") {
			t.Fatalf("summary = %q", res.Summary)
		}
	})

	t.Run("budget defer: plain line + skip note, run ok", func(t *testing.T) {
		rc, fake := newRC(t, map[string]string{"00-Inbox/x.md": "item\n"})
		rc.Config.Automations = map[string]config.Automation{"heartbeat": {Model: "classify"}}
		rc.BudgetTokens = 1 // per-call cap forces a defer at the chokepoint
		res, err := (Heartbeat{}).Run(context.Background(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if fake.CallCount() != 0 {
			t.Fatal("deferred call must not reach the agent")
		}
		if !strings.Contains(res.Summary, "synthesis skipped: budget") {
			t.Fatalf("summary = %q", res.Summary)
		}
		n, _ := rc.Vault.Read(context.Background(), "Daily/"+today(rc)+".md")
		if !strings.Contains(n.Body, plain) || strings.Contains(n.Body, "·  ") {
			t.Fatalf("block must keep the plain line on defer:\n%s", n.Body)
		}
	})

	t.Run("agent error: plain line + failure note, run ok", func(t *testing.T) {
		rc, fake := newRC(t, map[string]string{"00-Inbox/x.md": "item\n"})
		rc.Config.Automations = map[string]config.Automation{"heartbeat": {Model: "classify"}}
		fake.Err = errors.New("boom")
		res, err := (Heartbeat{}).Run(context.Background(), rc)
		if err != nil {
			t.Fatalf("essential heartbeat must not fail on synthesis error: %v", err)
		}
		if !strings.Contains(res.Summary, "synthesis failed") {
			t.Fatalf("summary = %q", res.Summary)
		}
	})
}

func TestValidateHeartbeatLine(t *testing.T) {
	for _, tt := range []struct {
		name, in string
		ok       bool
	}{
		{"valid", "3 inbox items look project-related; budget 42% used", true},
		{"empty", "   ", false},
		{"multiline", "line one\nline two", false},
		{"too long", strings.Repeat("x", 300), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHeartbeatLine(tt.in)
			if (err == nil) != tt.ok {
				t.Fatalf("validateHeartbeatLine(%q) err = %v, want ok=%v", tt.in, err, tt.ok)
			}
		})
	}
}

func TestValidateAgenticTools(t *testing.T) {
	if err := validateAgenticTools([]string{"vault_read", "vault_links", "vault_patch"}); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}
	for _, bad := range [][]string{{"vault_move"}, {"knowledge_ingest"}, {"automations_run"}, {"not_a_tool"}} {
		if err := validateAgenticTools(bad); err == nil {
			t.Fatalf("expected %v to be rejected", bad)
		}
	}
}

func TestAgenticContainsWriteTool(t *testing.T) {
	if !agenticContainsWriteTool([]string{"vault_read", "vault_patch"}) {
		t.Fatal("vault_patch should be detected as a write tool")
	}
	if agenticContainsWriteTool([]string{"vault_read", "vault_links"}) {
		t.Fatal("read-only set must not be flagged as write-capable")
	}
}

// TestCompactionAgenticWritesSummary: on the agentic path the distilled
// summary lands in axon:summary and the original is archived first (FR-44).
// (newRC's fake agent returns text but does not itself call MCP tools, so the
// summary lands via the verify-and-fallback Go Patch — outcome is what's under
// test; a real agent tool call is covered by the Task 5 smoke + agentic e2e.)
func TestCompactionAgenticWritesSummary(t *testing.T) {
	seed := map[string]string{
		"03-Resources/long.md": "---\ntitle: long\n---\n" + strings.Repeat("Sentence about vectors. ", 80) + "\n",
	}
	rc, fake := newRC(t, seed)
	ctx := context.Background()
	fake.Reply = "- vectors summary\n- second point"
	mustReindex(t, rc)

	res, err := (Compaction{WordThreshold: 50}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Fatalf("no note compacted; res=%+v", res)
	}
	n, _ := rc.Vault.Read(ctx, "03-Resources/long.md")
	if !strings.Contains(n.Body, "axon:summary:start") || !strings.Contains(n.Body, "vectors summary") {
		t.Fatalf("summary not written:\n%s", n.Body)
	}
	entries, _ := os.ReadDir(filepath.Join(rc.Vault.Root(), ".axon", "archive"))
	if len(entries) == 0 {
		t.Fatal("original not archived before compaction (FR-44)")
	}
}

func TestCompactionAgenticDryRunNoMutation(t *testing.T) {
	seed := map[string]string{
		"03-Resources/long.md": "---\ntitle: long\n---\n" + strings.Repeat("Sentence about vectors. ", 80) + "\n",
	}
	rc, fake := newRC(t, seed)
	ctx := context.Background()
	fake.Reply = "- summary"
	mustReindex(t, rc)
	rc.DryRun = true

	before, _ := rc.Vault.Read(ctx, "03-Resources/long.md")
	if _, err := (Compaction{WordThreshold: 50}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	after, _ := rc.Vault.Read(ctx, "03-Resources/long.md")
	if before.Body != after.Body {
		t.Fatalf("dry-run mutated the note:\n%s", after.Body)
	}
}

// TestVaultAskNeverAgentic pins ADR-023: vault_ask must never be callable
// from an agentic automation (it would be a nested model call).
func TestVaultAskNeverAgentic(t *testing.T) {
	if agenticReadTools["vault_ask"] || agenticWriteTools["vault_ask"] {
		t.Fatal("vault_ask must never be in an agentic automation allowlist (ADR-023)")
	}
}
