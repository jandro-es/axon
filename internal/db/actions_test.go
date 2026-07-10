package db

import (
	"context"
	"testing"
)

func TestMarkActionDone(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)
	if err := ReplaceActions(ctx, d, []Action{
		{Hash: "h1", SourcePath: "a.md", LineNo: 1, Text: "x", Raw: "- [ ] x",
			State: "open", Checkbox: " ", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	n, err := MarkActionDone(ctx, d, "h1", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	got, _ := ListActions(ctx, d, ListActionsOpts{IncludeAll: true})
	if got[0].State != "done" || got[0].DoneDate != "2026-07-10" {
		t.Errorf("row not marked done: %+v", got[0])
	}
	if n2, _ := MarkActionDone(ctx, d, "nope", "2026-07-10"); n2 != 0 {
		t.Errorf("unknown hash affected %d rows, want 0", n2)
	}
}

func TestReplaceAndListActions(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)
	rows := []Action{
		{Hash: "h1", SourcePath: "01-Projects/a.md", LineNo: 3, Section: "Todo",
			Text: "call bob", Raw: "- [ ] call bob 📅 2026-07-15", State: "open",
			Checkbox: " ", Due: "2026-07-15", Contexts: []string{"phone"},
			Tags: []string{"waiting"}, Updated: "2026-07-10T00:00:00Z"},
		{Hash: "h2", SourcePath: "01-Projects/a.md", LineNo: 5, Text: "ship",
			Raw: "- [x] ship", State: "done", Checkbox: "x", DoneDate: "2026-07-09",
			Updated: "2026-07-10T00:00:00Z"},
		{Hash: "h3", SourcePath: "04-Archive/old.md", LineNo: 1, Text: "old",
			Raw: "- [ ] old", State: "open", Checkbox: " ", Archived: true,
			Updated: "2026-07-10T00:00:00Z"},
	}
	if err := ReplaceActions(ctx, d, rows); err != nil {
		t.Fatal(err)
	}
	// default (archived excluded)
	got, err := ListActions(ctx, d, ListActionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d actions (archived should be excluded), want 2", len(got))
	}
	if got[0].Tags == nil || got[0].Tags[0] != "waiting" || got[0].Contexts[0] != "phone" {
		t.Errorf("json arrays lost: tags=%v contexts=%v", got[0].Tags, got[0].Contexts)
	}
	// IncludeAll
	all, _ := ListActions(ctx, d, ListActionsOpts{IncludeAll: true})
	if len(all) != 3 {
		t.Fatalf("IncludeAll got %d, want 3", len(all))
	}
	// state filter
	open, _ := ListActions(ctx, d, ListActionsOpts{State: "open"})
	if len(open) != 1 {
		t.Fatalf("state=open got %d, want 1 (non-archived)", len(open))
	}
	// counts
	total, o, done, cancelled, archived, err := ActionStateCounts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || o != 2 || done != 1 || cancelled != 0 || archived != 1 {
		t.Errorf("counts total=%d open=%d done=%d cancelled=%d archived=%d", total, o, done, cancelled, archived)
	}
	// replace is delete-all
	if err := ReplaceActions(ctx, d, rows[:1]); err != nil {
		t.Fatal(err)
	}
	again, _ := ListActions(ctx, d, ListActionsOpts{IncludeAll: true})
	if len(again) != 1 {
		t.Fatalf("after replace got %d, want 1", len(again))
	}
}
