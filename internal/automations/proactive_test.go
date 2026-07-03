package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func TestBriefingWritesBlockOncePerDay(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"01-Projects/p.md": "---\nupdated: 2026-06-28\n---\nbody\n"})
	mustReindex(t, rc)
	fake.Reply = "Yesterday centered on project work."
	ctx := context.Background()

	ch, err := (Briefing{}).DetectChange(ctx, rc)
	if err != nil || !ch.Changed || !strings.HasPrefix(ch.Cursor, "briefing:") {
		t.Fatalf("detect = %+v err=%v", ch, err)
	}
	res, err := (Briefing{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "briefing") {
		t.Fatalf("summary = %q", res.Summary)
	}
	note, err := rc.Vault.Read(ctx, "Daily/"+today(rc)+".md")
	if err != nil {
		t.Fatalf("daily note missing: %v", err)
	}
	if !strings.Contains(note.Body, "axon:briefing:start") {
		t.Fatalf("no briefing block:\n%s", note.Body)
	}
	if !strings.Contains(note.Body, "Yesterday centered on project work.") {
		t.Fatalf("narrative missing:\n%s", note.Body)
	}
	if !strings.Contains(note.Body, "Budget") {
		t.Fatalf("facts missing:\n%s", note.Body)
	}

	// Same day: gate closes.
	rc.LastCursor = ch.Cursor
	ch2, _ := (Briefing{}).DetectChange(ctx, rc)
	if ch2.Changed {
		t.Fatal("second detect same day must not change")
	}
}

func TestBriefingDegradesToFactsOnBudgetDefer(t *testing.T) {
	rc, _ := newRC(t, nil)
	// A 1-token per-call input cap (the ADR-017 budget_tokens wiring) forces
	// the narrative call to defer — the briefing must degrade, not fail.
	rc.BudgetTokens = 1
	res, err := (Briefing{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("briefing must degrade, not fail: %v", err)
	}
	note, rerr := rc.Vault.Read(context.Background(), "Daily/"+today(rc)+".md")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(note.Body, "narrative skipped: budget") {
		t.Fatalf("degradation marker missing:\n%s", note.Body)
	}
	_ = res
}

func TestBriefingDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	if _, err := (Briefing{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rc.Vault.Root(), "Daily", today(rc)+".md")); !os.IsNotExist(err) {
		t.Fatal("dry-run created the daily note")
	}
}

func TestBriefingCoexistsWithHeartbeat(t *testing.T) {
	rc, fake := newRC(t, nil)
	fake.Reply = "narrative"
	ctx := context.Background()
	if _, err := (Heartbeat{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if _, err := (Briefing{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	note, _ := rc.Vault.Read(ctx, "Daily/"+today(rc)+".md")
	if !strings.Contains(note.Body, "axon:heartbeat:start") || !strings.Contains(note.Body, "axon:briefing:start") {
		t.Fatalf("blocks must coexist:\n%s", note.Body)
	}
}

var _ = time.Now // used by later resurfacer tests

func seedVecNote(t *testing.T, rc RunCtx, path, updated string, vec []float32) {
	t.Helper()
	ctx := context.Background()
	res, err := rc.DB.ExecContext(ctx, `INSERT INTO notes (path, title, updated) VALUES (?, ?, ?)`, path, path, updated)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()
	cres, err := rc.DB.ExecContext(ctx, `INSERT INTO chunks (note_id, ordinal, text, token_count, content_hash) VALUES (?, 0, 'x', 1, ?)`, noteID, path)
	if err != nil {
		t.Fatal(err)
	}
	chunkID, _ := cres.LastInsertId()
	if _, err := rc.DB.ExecContext(ctx, `INSERT INTO vec_chunks (chunk_id, dim, model, embedding) VALUES (?, ?, 'test', ?)`,
		chunkID, len(vec), db.EncodeVector(vec)); err != nil {
		t.Fatal(err)
	}
	// The vault note must exist for linkTargets reads.
	full := filepath.Join(rc.Vault.Root(), filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: "+path+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResurfacerProposesRecentDormantPairs(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	recent := rc.now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	dormant := rc.now().UTC().AddDate(0, 0, -200).Format("2006-01-02")
	seedVecNote(t, rc, "01-Projects/current.md", recent, []float32{1, 0})
	seedVecNote(t, rc, "03-Resources/ancient.md", dormant, []float32{0.95, 0.05})
	seedVecNote(t, rc, "03-Resources/unrelated.md", dormant, []float32{0, 1})

	ch, err := (Resurfacer{}).DetectChange(ctx, rc)
	if err != nil || !ch.Changed {
		t.Fatalf("detect = %+v err=%v", ch, err)
	}
	res, err := (Resurfacer{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 || !strings.Contains(res.Changes[0], "ancient") {
		t.Fatalf("changes = %v, want the similar dormant note only", res.Changes)
	}
	q, _ := os.ReadFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"))
	if !strings.Contains(string(q), "resurface [[03-Resources/ancient]]") {
		t.Fatalf("queue:\n%s", q)
	}
	if !strings.Contains(string(q), "dormant since "+dormant) {
		t.Fatalf("queue missing dormant date:\n%s", q)
	}

	// Second run: proposal memory prevents re-proposing.
	res2, err := (Resurfacer{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Changes) != 0 {
		t.Fatalf("second run re-proposed: %v", res2.Changes)
	}
}

func TestResurfacerSkipsAlreadyLinked(t *testing.T) {
	rc, _ := newRC(t, nil)
	recent := rc.now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	dormant := rc.now().UTC().AddDate(0, 0, -200).Format("2006-01-02")
	seedVecNote(t, rc, "01-Projects/current.md", recent, []float32{1, 0})
	seedVecNote(t, rc, "03-Resources/ancient.md", dormant, []float32{0.99, 0.01})
	// The recent note already links to the dormant one.
	full := filepath.Join(rc.Vault.Root(), "01-Projects", "current.md")
	if err := os.WriteFile(full, []byte("---\ntitle: c\n---\nsee [[03-Resources/ancient]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := (Resurfacer{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("already-linked pair proposed: %v", res.Changes)
	}
}

func TestResurfacerDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	recent := rc.now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	dormant := rc.now().UTC().AddDate(0, 0, -200).Format("2006-01-02")
	seedVecNote(t, rc, "a.md", recent, []float32{1, 0})
	seedVecNote(t, rc, "b.md", dormant, []float32{0.9, 0.1})

	res, err := (Resurfacer{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Fatal("dry-run should report would-propose pairs")
	}
	if _, err := os.Stat(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote the review queue")
	}
}

func TestProactiveRegistered(t *testing.T) {
	p := config.Profile{}
	for _, name := range []string{"briefing", "resurfacer"} {
		if _, err := Get(p, name); err != nil {
			t.Fatalf("%s not registered: %v", name, err)
		}
		if Purpose(name) == "(no description)" {
			t.Fatalf("%s has no catalog purpose", name)
		}
	}
}
