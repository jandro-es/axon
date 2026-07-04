package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/core"
)

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
	if len(list.Automations) != 15 {
		t.Errorf("expected 15 automations, got %d", len(list.Automations))
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
