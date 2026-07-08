package automations

import (
	"testing"
	"time"
)

func TestLadderDays(t *testing.T) {
	if got := ladderDays(nil); got[0] != 7 || got[4] != 112 {
		t.Fatalf("default ladder = %v", got)
	}
	if got := ladderDays([]int{2, 6}); got[0] != 14 || got[1] != 42 {
		t.Fatalf("ladder = %v", got)
	}
}

func TestDueAfterClampsRung(t *testing.T) {
	ld := ladderDays(nil) // [7,14,28,56,112]
	if got := dueAfter(mustDate("2026-01-01"), 0, ld); got != "2026-01-08" {
		t.Fatalf("rung0 due = %s", got)
	}
	if got := dueAfter(mustDate("2026-01-01"), 99, ld); got != "2026-04-23" {
		t.Fatalf("clamped due = %s", got)
	}
}

func TestIsDue(t *testing.T) {
	if !isDue(schedItem{Due: ""}, "2026-01-01") {
		t.Fatal("empty Due should be due")
	}
	if isDue(schedItem{Due: "2026-01-08"}, "2026-01-01") {
		t.Fatal("future Due should not be due")
	}
	if !isDue(schedItem{Due: "2026-01-01"}, "2026-01-08") {
		t.Fatal("past Due should be due")
	}
}

func TestAdvanceDismissStepsOne(t *testing.T) {
	ld := ladderDays(nil)
	got := advance(schedItem{Rung: 0}, false, "2026-01-01", ld)
	if got.Rung != 1 {
		t.Fatalf("dismiss rung = %d, want 1", got.Rung)
	}
	if got.Due != "2026-01-15" {
		t.Fatalf("dismiss due = %s, want 2026-01-15", got.Due)
	}
	if got.LastOutcome != "2026-01-01" {
		t.Fatalf("last = %s", got.LastOutcome)
	}
}

func TestAdvanceAcceptStepsTwo(t *testing.T) {
	ld := ladderDays(nil)
	got := advance(schedItem{Rung: 0}, true, "2026-01-01", ld)
	if got.Rung != 2 {
		t.Fatalf("accept rung = %d, want 2", got.Rung)
	}
	if got.Due != "2026-01-29" {
		t.Fatalf("accept due = %s, want 2026-01-29", got.Due)
	}
}

func mustDate(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}
