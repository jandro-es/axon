package health

import (
	"context"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

// seededProfile enables two non-essential automations and allows everything.
func seededProfile() config.Profile {
	return config.Profile{
		Automations: map[string]config.Automation{
			"daily-log":    {Enabled: true, Schedule: "30 21 * * *"},
			"inbox-triage": {Enabled: true, Schedule: "*/30 * * * *"},
		},
		Policy: config.PolicyConfig{AllowedAutomations: []string{"*"}},
	}
}

func TestComputeHealthyVault(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		if _, err := d.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	// 2 notes, 4 chunks all embedded (100% coverage), 3 links of which 1 broken.
	exec(`INSERT INTO notes (id, path, word_count) VALUES (1,'a.md',100),(2,'b.md',200);`)
	exec(`INSERT INTO chunks (id, note_id, ordinal) VALUES (1,1,0),(2,1,1),(3,2,0),(4,2,1);`)
	for i := 1; i <= 4; i++ {
		exec(`INSERT INTO vec_chunks (chunk_id, dim, embedding) VALUES (?,1,?);`, i, []byte{0, 0, 0, 0})
	}
	exec(`INSERT INTO links (src_note_id, dst_path, kind, dst_note_id) VALUES
		(1,'b.md','wikilink',2),(2,'a.md','wikilink',1),(1,'missing.md','wikilink',NULL);`)

	// A source fetched 2 days ago; daily-log ok yesterday; inbox-triage never ran.
	exec(`INSERT INTO sources (fetched_at, status) VALUES (?, 'ok');`, now.AddDate(0, 0, -2).Format(time.RFC3339))
	exec(`INSERT INTO runs (automation, started_at, finished_at, status) VALUES ('daily-log', ?, ?, 'ok');`,
		now.AddDate(0, 0, -1).Format(time.RFC3339), now.AddDate(0, 0, -1).Format(time.RFC3339))

	rep, err := ComputeAt(ctx, d, seededProfile(), now)
	if err != nil {
		t.Fatal(err)
	}

	if len(rep.Dimensions) != 3 {
		t.Fatalf("want 3 dimensions, got %d", len(rep.Dimensions))
	}
	byKey := map[string]Dimension{}
	for _, dim := range rep.Dimensions {
		byKey[dim.Key] = dim
	}

	// Integrity: 100% coverage, 1/3 links broken → ~87.
	if s := byKey["integrity"].Score; s < 80 || s > 92 {
		t.Errorf("integrity score = %d, want ~87", s)
	}
	// Reliability: daily-log healthy, inbox-triage never run → (1.0+0.5)/2 = 75.
	if s := byKey["reliability"].Score; s != 75 {
		t.Errorf("reliability score = %d, want 75", s)
	}
	// Freshness: last activity 1 day ago → 90.
	if s := byKey["freshness"].Score; s != 90 {
		t.Errorf("freshness score = %d, want 90", s)
	}
	if rep.Grade != "B" {
		t.Errorf("grade = %q, want B (score %d)", rep.Grade, rep.Score)
	}
}

func TestComputeEmptyVault(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}

	rep, err := ComputeAt(ctx, d, config.Profile{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// An empty, fresh vault should not be scored as unhealthy.
	if rep.Score < 90 {
		t.Errorf("empty vault score = %d, want >= 90", rep.Score)
	}
}

func TestReliabilityPenalisesFailures(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	// Both enabled automations' last run failed → reliability 0.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO runs (automation, started_at, finished_at, status, error) VALUES
		 ('daily-log','t','t','failed','boom'),('inbox-triage','t','t','failed','boom');`); err != nil {
		t.Fatal(err)
	}
	rep, err := ComputeAt(ctx, d, seededProfile(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, dim := range rep.Dimensions {
		if dim.Key == "reliability" && dim.Score != 0 {
			t.Errorf("reliability with all-failed runs = %d, want 0", dim.Score)
		}
	}
}
