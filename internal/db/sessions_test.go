package db

import (
	"context"
	"testing"
)

func TestPendingSessionsRoundTrip(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()

	m, err := LoadPendingSessions(ctx, d)
	if err != nil || len(m) != 0 {
		t.Fatalf("empty load = %v, %v", m, err)
	}
	m["sess-1"] = PendingSession{TranscriptPath: "/t/1.jsonl", LastStop: "2026-07-04T10:00:00Z"}
	if err := SavePendingSessions(ctx, d, m, "2026-07-04T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPendingSessions(ctx, d)
	if err != nil || got["sess-1"].TranscriptPath != "/t/1.jsonl" {
		t.Fatalf("round trip = %v, %v", got, err)
	}
}
