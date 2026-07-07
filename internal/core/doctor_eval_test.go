package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func TestVettingCheckStates(t *testing.T) {
	ref := "ollama:qwen"
	pass := db.EvalRun{Family: "classify", ModelRef: ref, Digest: "d1", PassPct: 90}
	cases := []struct {
		name     string
		minPass  int
		row      db.EvalRun
		haveRow  bool
		curDig   string
		digKnown bool
		want     CheckStatus
		substr   string
	}{
		{"ungated", 0, db.EvalRun{}, false, "d1", true, StatusWarn, "ungated"},
		{"not-vetted", 80, db.EvalRun{}, false, "d1", true, StatusWarn, "not vetted"},
		{"below", 80, db.EvalRun{PassPct: 60, Digest: "d1"}, true, "d1", true, StatusWarn, "below"},
		{"drift", 80, pass, true, "d2", true, StatusWarn, "changed"},
		{"vetted", 80, pass, true, "d1", true, StatusOK, "vetted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := vettingCheck("eval-vetting:classify", "classify", ref, c.minPass, c.row, c.haveRow, c.curDig, c.digKnown)
			if got.Status != c.want {
				t.Fatalf("status = %v, want %v (%s)", got.Status, c.want, got.Detail)
			}
			if !strings.Contains(got.Detail, c.substr) {
				t.Fatalf("detail %q missing %q", got.Detail, c.substr)
			}
		})
	}
}
