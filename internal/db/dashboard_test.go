package db

import (
	"context"
	"strings"
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
	if len(runs) == 1 && runs[0].Error != "" {
		t.Errorf("ok run carries error %q, want empty", runs[0].Error)
	}

	// A failed run surfaces its recorded error so the dashboard can show WHY.
	fid, _ := InsertRun(ctx, d, "inbox-triage", now)
	_ = FinishRun(ctx, d, RunUpdate{ID: fid, Status: RunFailed, FinishedAt: now, Error: `exec: "claude": executable file not found in $PATH`})
	runs, err = RecentRuns(ctx, d, 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("RecentRuns after failed run = %+v, %v", runs, err)
	}
	if runs[0].Automation != "inbox-triage" || !strings.Contains(runs[0].Error, "claude") {
		t.Errorf("failed run error not surfaced: %+v", runs[0])
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

	g, err := GraphData(ctx, d, 100, false)
	if err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Errorf("GraphData = %d nodes %d edges, %v (want 2,1)", len(g.Nodes), len(g.Edges), err)
	}
	if len(g.Edges) == 1 && g.Edges[0].Kind != "link" {
		t.Errorf("wikilink edge kind = %q, want link", g.Edges[0].Kind)
	}

	if pend, err := PendingEmbeddings(ctx, d); err != nil || pend != 0 {
		t.Errorf("PendingEmbeddings = %d, %v", pend, err)
	}
}

// TestGraphSimilarityEdges (FR-61): notes with close embeddings get "similar"
// edges; distant ones don't; wikilink edges are unaffected.
func TestGraphSimilarityEdges(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	seed := func(path string, vec []float32) int64 {
		id, err := UpsertNote(ctx, d, NoteRow{Path: path, Type: "note"})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := InsertChunk(ctx, d, ChunkRow{NoteID: &id, Ordinal: 0, Text: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := seed("a.md", []float32{1, 0, 0})
	b := seed("b.md", []float32{0.99, 0.1, 0}) // very close to a
	seed("c.md", []float32{0, 0, 1})           // orthogonal to both

	g, err := GraphData(ctx, d, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	var similar []GraphEdge
	for _, e := range g.Edges {
		if e.Kind == "similar" {
			similar = append(similar, e)
		}
	}
	if len(similar) != 1 {
		t.Fatalf("similar edges = %+v, want exactly a<->b", similar)
	}
	e := similar[0]
	connectsAB := (e.Source == a && e.Target == b) || (e.Source == b && e.Target == a)
	if !connectsAB {
		t.Errorf("similar edge connects %d-%d, want %d-%d", e.Source, e.Target, a, b)
	}
	if e.Sim < simThreshold {
		t.Errorf("edge sim = %f, want >= %f", e.Sim, simThreshold)
	}

	// Toggle off: no similarity edges.
	g2, err := GraphData(ctx, d, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g2.Edges {
		if e.Kind == "similar" {
			t.Errorf("similarity edge present with includeSimilar=false: %+v", e)
		}
	}
}

// TestVaultGrowthSeries (FR-60): cumulative notes/words by creation date.
func TestVaultGrowthSeries(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	_, _ = UpsertNote(ctx, d, NoteRow{Path: "one.md", Created: "2026-01-01", WordCount: 100})
	_, _ = UpsertNote(ctx, d, NoteRow{Path: "two.md", Created: "2026-01-01", WordCount: 50})
	_, _ = UpsertNote(ctx, d, NoteRow{Path: "three.md", Created: "2026-02-01", WordCount: 25})

	g, err := VaultGrowth(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("growth points = %+v, want 2 days", g)
	}
	if g[0].Day != "2026-01-01" || g[0].Notes != 2 || g[0].Words != 150 {
		t.Errorf("day 1 = %+v, want 2 notes / 150 words", g[0])
	}
	if g[1].Day != "2026-02-01" || g[1].Notes != 3 || g[1].Words != 175 {
		t.Errorf("day 2 = %+v, want cumulative 3 notes / 175 words", g[1])
	}
}
