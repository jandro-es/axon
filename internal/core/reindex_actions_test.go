package core

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func TestReindexBuildsActions(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{
		"01-Projects/proj.md": "## Todo\n- [ ] alpha 📅 2026-07-15\n- [x] beta ✅ 2026-07-09\n",
		"04-Archive/old.md":   "- [ ] gamma\n",
	})
	d := migratedDB(t)

	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListActions(ctx, d, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d actions, want 3: %+v", len(all), all)
	}
	var archivedSeen bool
	for _, a := range all {
		if a.SourcePath == "04-Archive/old.md" && a.Archived {
			archivedSeen = true
		}
	}
	if !archivedSeen {
		t.Error("archived note's action not flagged archived")
	}

	// S9: reindex again (fresh DB) → byte-identical row set.
	before := actionSig(all)
	d2 := migratedDB(t)
	if _, err := Reindex(ctx, v, d2); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListActions(ctx, d2, db.ListActionsOpts{IncludeAll: true})
	if actionSig(after) != before {
		t.Errorf("reindex is not byte-equivalent (S9):\n%s\n---\n%s", before, actionSig(after))
	}
}

func actionSig(as []db.Action) string {
	s := ""
	for _, a := range as {
		s += a.SourcePath + "|" + a.Hash + "|" + a.State + "|" + a.Due + "\n"
	}
	return s
}
