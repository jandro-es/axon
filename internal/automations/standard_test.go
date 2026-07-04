package automations

import (
	"context"
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

// TestInboxTriageEmptyReplyFails proves the ADR-015 output validator turns an
// empty classification into a failed run instead of a silent empty queue line.
func TestInboxTriageEmptyReplyFails(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/item.md": "capture me\n"})
	fake.Reply = ""
	if _, err := (InboxTriage{}).Run(context.Background(), rc); err == nil {
		t.Fatal("empty model reply should fail the triage run")
	}
}
