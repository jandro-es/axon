package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/vault"
)

func TestActionsListTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{})
	d := tools.deps.DB
	if err := db.ReplaceActions(ctx, d, []db.Action{
		{Hash: "h-over", SourcePath: "01-Projects/w.md", Text: "fix bug", State: "open", Checkbox: " ", Due: "2000-01-01", Priority: "high", Updated: "u"},
		{Hash: "h-some", SourcePath: "Ideas.md", Text: "learn rust", State: "open", Checkbox: " ", Tags: []string{"someday"}, Updated: "u"},
		{Hash: "h-done", SourcePath: "01-Projects/w.md", Text: "shipped", State: "done", Checkbox: "x", DoneDate: "2000-01-02", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ActionsList(ctx, ActionsListIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Actions) != 2 {
		t.Fatalf("default list = %d, want 2 (open only): %+v", len(out.Actions), out.Actions)
	}
	if out.Counts["open"] != 2 || out.Counts["overdue"] != 1 {
		t.Errorf("counts = %+v", out.Counts)
	}
	for _, a := range out.Actions {
		if a.Hash == "" || a.Bucket == "" {
			t.Errorf("row missing hash/bucket: %+v", a)
		}
	}
	od, _ := tools.ActionsList(ctx, ActionsListIn{Status: "overdue"})
	if len(od.Actions) != 1 || od.Actions[0].Text != "fix bug" {
		t.Errorf("status=overdue = %+v", od.Actions)
	}
}

func TestActionCompleteTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"p.md": "- [ ] finish spec\n"})
	d := tools.deps.DB
	var hash string
	for _, a := range actions.Extract("p.md", "- [ ] finish spec\n", false) {
		hash = a.Hash()
	}
	if err := db.ReplaceActions(ctx, d, []db.Action{
		{Hash: hash, SourcePath: "p.md", LineNo: 0, Text: "finish spec", Raw: "- [ ] finish spec", State: "open", Checkbox: " ", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.ActionComplete(ctx, ActionCompleteIn{Path: "p.md", Hash: "bogus"}); !errors.Is(err, vault.ErrActionNotFound) {
		t.Fatalf("stale hash: want ErrActionNotFound, got %v", err)
	}
	out, err := tools.ActionComplete(ctx, ActionCompleteIn{Path: "p.md", Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Applied {
		t.Error("Applied should be true")
	}
	n, _ := tools.deps.Vault.Read(ctx, "p.md")
	if !strings.Contains(n.Body, "- [x] finish spec ✅ ") {
		t.Errorf("source line not flipped:\n%s", n.Body)
	}
	got, _ := db.ListActions(ctx, d, db.ListActionsOpts{IncludeAll: true})
	if got[0].State != "done" {
		t.Errorf("DB row not marked done: %+v", got[0])
	}
}

func TestActionCompleteDryRun(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"p.md": "- [ ] x\n"})
	tools.deps.DryRun = true
	out, err := tools.ActionComplete(ctx, ActionCompleteIn{Path: "p.md", Hash: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Applied {
		t.Error("dry-run must not apply")
	}
	n, _ := tools.deps.Vault.Read(ctx, "p.md")
	if !strings.Contains(n.Body, "- [ ] x") {
		t.Error("dry-run must not flip the line")
	}
}

func TestRelatedTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{})
	d := tools.deps.DB // the migrated in-memory DB the Searcher reads
	seed := func(path string, vec []float32) {
		id, err := db.UpsertNote(ctx, d, db.NoteRow{Path: path, Title: path})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &id, Text: path, ContentHash: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
	}
	seed("a.md", []float32{1, 0, 0, 0})
	seed("b.md", []float32{0.95, 0.05, 0, 0})
	seed("c.md", []float32{0, 1, 0, 0})

	out, err := tools.Related(ctx, RelatedIn{Path: "a.md", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Related) != 1 || out.Related[0].Path != "b.md" {
		t.Fatalf("want [b.md], got %+v", out.Related)
	}
}

func TestReadTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"n.md": "---\ntitle: N\ntype: note\n---\nbody text\n"})
	out, err := tools.Read(ctx, ReadIn{Path: "n.md"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Body != "body text\n" || out.Frontmatter["title"] != "N" {
		t.Errorf("read = %+v", out)
	}
	if _, err := tools.Read(ctx, ReadIn{Path: "missing.md"}); err == nil {
		t.Error("reading a missing note should error")
	}
}

func TestLinksTool(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, map[string]string{
		"01-Projects/a.md": "see [[02-Areas/b]]\n",
		"02-Areas/b.md":    "the target\n",
	})
	if _, err := core.Reindex(ctx, v, tools.deps.DB); err != nil {
		t.Fatal(err)
	}
	out, err := tools.Links(ctx, LinksIn{Path: "02-Areas/b.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Backlinks) != 1 || out.Backlinks[0] != "01-Projects/a.md" {
		t.Errorf("backlinks = %v, want [01-Projects/a.md]", out.Backlinks)
	}
	from, err := tools.Links(ctx, LinksIn{Path: "01-Projects/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(from.Outbound) == 0 {
		t.Error("expected outbound links from a.md")
	}
}

func TestDailyAppendTool(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, nil)
	out, err := tools.DailyAppend(ctx, DailyAppendIn{Content: "- a quick capture"}, "2026-06-28")
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != "Daily/2026-06-28.md" {
		t.Errorf("path = %q", out.Path)
	}
	n, _ := v.Read(ctx, out.Path)
	if want := "a quick capture"; !contains(n.Body, want) {
		t.Errorf("daily note missing appended content:\n%s", n.Body)
	}
	// Appending again keeps the note and adds the new line.
	if _, err := tools.DailyAppend(ctx, DailyAppendIn{Content: "- second"}, "2026-06-28"); err != nil {
		t.Fatal(err)
	}
	n, _ = v.Read(ctx, out.Path)
	if !contains(n.Body, "second") {
		t.Error("second append not present")
	}
}

func TestMetricsQueryTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, nil)
	out, err := tools.Metrics(ctx, MetricsIn{SinceDays: 7}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// With no ledger activity the aggregates are zero but the shape is valid and
	// budget windows are populated from the manager.
	if out.SinceDays != 7 || out.ByModel == nil || out.ByOperation == nil {
		t.Errorf("metrics out = %+v", out)
	}
	if out.DayLimit == 0 {
		t.Error("expected a day budget limit from the manager")
	}
}

func TestMemoryRememberTool(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, nil)
	out, err := tools.Remember(ctx, RememberIn{Text: "Prefers Go for daemons", Kind: "preference", Source: "session"}, "2026-06-28")
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || !contains(out.Entry, "Prefers Go for daemons") || !contains(out.Entry, "[preference]") {
		t.Errorf("remember out = %+v", out)
	}
	// The entry must land inside the axon:memory managed block, leaving the file
	// otherwise human-owned.
	n, err := v.Read(ctx, out.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(n.Body, "axon:memory:start") || !contains(n.Body, "Prefers Go for daemons") {
		t.Errorf("memory entry not written to managed block:\n%s", n.Body)
	}
}

func TestAutomationsListAndRunTools(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"01-Projects/p.md": "a note\n"})

	list, err := tools.ListAutomations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Automations) != 21 {
		t.Errorf("expected 21 automations, got %d", len(list.Automations))
	}

	// Run a no-model automation through the engine path.
	out, err := tools.RunAutomation(ctx, RunIn{Name: "context-export"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" {
		t.Errorf("automations_run status = %q, want ok", out.Status)
	}
	if _, err := tools.RunAutomation(ctx, RunIn{Name: "no-such-automation"}); err == nil {
		t.Error("running an unknown automation should error")
	}
}

func TestIngestToolRefusesLocalFiles(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, nil)
	// The MCP path must not let an agent ingest arbitrary local files.
	if _, err := tools.Ingest(ctx, IngestIn{Target: "/etc/passwd"}); err == nil {
		t.Error("knowledge_ingest of a local path must be refused on the agent path")
	}
	if _, err := tools.Ingest(ctx, IngestIn{Target: "file:///etc/passwd"}); err == nil {
		t.Error("knowledge_ingest of a file:// path must be refused")
	}
}
