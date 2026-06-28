package db

import (
	"context"
	"testing"
	"time"
)

func TestDashboardReadLayer(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)
	now := time.Now().UTC().Format(time.RFC3339)

	// Seed ledger (two operations/models), runs, sources, an event, and a graph.
	_, _ = InsertLedger(ctx, d, LedgerRow{TS: now, Profile: "p", Operation: "automation.daily-log", Model: "sonnet", InputTokens: 100, OutputTokens: 20})
	_, _ = InsertLedger(ctx, d, LedgerRow{TS: now, Profile: "p", Operation: "ingest.enrich", Model: "haiku", InputTokens: 50, OutputTokens: 5})
	rid, _ := InsertRun(ctx, d, "daily-log", now)
	_ = FinishRun(ctx, d, RunUpdate{ID: rid, Status: RunOK, FinishedAt: now, Tokens: 120})
	_, _ = UpsertSource(ctx, d, SourceRow{URL: "u", Kind: "url", FetchedAt: now, Status: "ok"})
	_ = InsertEvent(ctx, d, time.Now(), "info", "ingest.done", "ingested something", map[string]any{"k": "v"})

	a, _ := UpsertNote(ctx, d, NoteRow{Path: "a.md", Type: "note"})
	b, _ := UpsertNote(ctx, d, NoteRow{Path: "b.md", Type: "note"})
	_ = InsertLink(ctx, d, LinkRow{SrcNoteID: a, DstPath: "b", DstNoteID: &b, Kind: "wikilink"})

	since := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)

	ts, err := TokenSeries(ctx, d, since)
	if err != nil || len(ts) != 2 {
		t.Errorf("TokenSeries = %d buckets, %v (want 2 by operation/model)", len(ts), err)
	}

	runs, err := RecentRuns(ctx, d, 10)
	if err != nil || len(runs) != 1 || runs[0].Status != RunOK {
		t.Errorf("RecentRuns = %+v, %v", runs, err)
	}

	src, err := SourceSeries(ctx, d)
	if err != nil || len(src) != 1 {
		t.Errorf("SourceSeries = %+v, %v", src, err)
	}

	ev, err := RecentEvents(ctx, d, 10)
	if err != nil || len(ev) != 1 || ev[0].Kind != "ingest.done" {
		t.Errorf("RecentEvents = %+v, %v", ev, err)
	}

	stats, err := Stats(ctx, d)
	if err != nil || stats.Notes != 2 || stats.Links != 1 {
		t.Errorf("Stats = %+v, %v", stats, err)
	}

	g, err := GraphData(ctx, d, 100)
	if err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Errorf("GraphData = %d nodes %d edges, %v (want 2,1)", len(g.Nodes), len(g.Edges), err)
	}

	if pend, err := PendingEmbeddings(ctx, d); err != nil || pend != 0 {
		t.Errorf("PendingEmbeddings = %d, %v", pend, err)
	}
}
