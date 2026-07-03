package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
