package automations

import (
	"context"
	"testing"
)

func TestScheduleRoundTrip(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()

	const key = "resurfacer:schedule"
	if got := loadSchedule(ctx, rc, key); len(got) != 0 {
		t.Fatalf("empty load = %v", got)
	}

	in := resurfaceSchedule{
		"a\x00b": {Rung: 1, Due: "2026-02-01", LastOutcome: "2026-01-18"},
		"c\x00d": {Rung: 0, Due: "2026-01-08"},
	}
	saveSchedule(ctx, rc, key, in)

	got := loadSchedule(ctx, rc, key)
	if len(got) != 2 || got["a\x00b"].Rung != 1 || got["a\x00b"].Due != "2026-02-01" {
		t.Fatalf("round-trip = %#v", got)
	}
	if got["a\x00b"].LastOutcome != "2026-01-18" {
		t.Fatalf("lastOutcome lost = %#v", got["a\x00b"])
	}
}
