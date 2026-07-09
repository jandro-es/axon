package automations

import (
	"context"
	"strings"
	"testing"
)

func TestMergeProposalsEmitsNearDuplicatePair(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	d := rc.now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	// Two near-identical notes (cosine 1.0) and one unrelated note.
	seedVecNote(t, rc, "notes/alpha.md", d, []float32{1, 0, 0})
	seedVecNote(t, rc, "notes/alpha-copy.md", d, []float32{1, 0, 0})
	seedVecNote(t, rc, "notes/other.md", d, []float32{0, 1, 0})

	res, err := (MergeProposals{}).Run(ctx, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %v, want 1 pair", res.Changes)
	}
	if !strings.HasPrefix(res.Changes[0], "merge [[") || !strings.Contains(res.Changes[0], "alpha") {
		t.Fatalf("unexpected proposal line: %q", res.Changes[0])
	}
	// Second run: the pair is now pending in the queue → not re-proposed.
	res2, err := (MergeProposals{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Changes) != 0 {
		t.Fatalf("second run re-proposed a pending pair: %v", res2.Changes)
	}
}

func TestMergeProposalsDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	ctx := context.Background()
	d := rc.now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	seedVecNote(t, rc, "a.md", d, []float32{1, 0, 0})
	seedVecNote(t, rc, "b.md", d, []float32{1, 0, 0})
	res, err := (MergeProposals{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("dry-run should still compute 1 pair, got %v", res.Changes)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("dry-run wrote the queue")
	}
}
