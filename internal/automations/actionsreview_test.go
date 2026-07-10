package automations

import (
	"context"
	"strings"
	"testing"
)

func TestActionsReviewProposesStaleUndated(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{
		"01-Projects/old.md":   "---\nupdated: 2000-01-01\n---\n- [ ] tidy backlog\n",
		"01-Projects/fresh.md": "---\nupdated: 2099-01-01\n---\n- [ ] recent task\n",
		"01-Projects/dated.md": "---\nupdated: 2000-01-01\n---\n- [ ] has a due 📅 2099-01-01\n",
	})
	mustReindex(t, rc)

	res, err := ActionsReview{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedTokens != 0 {
		t.Errorf("zero-model automation must report 0 tokens, got %d", res.EstimatedTokens)
	}
	q := readReviewQueue(t, rc)
	if !strings.Contains(q, `stalled action "tidy backlog" in [[01-Projects/old]]`) {
		t.Errorf("stale undated task not proposed:\n%s", q)
	}
	if strings.Contains(q, "recent task") {
		t.Error("fresh note's task must not be proposed")
	}
	if strings.Contains(q, "has a due") {
		t.Error("dated task must not be proposed")
	}
	if _, err := (ActionsReview{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	q2 := readReviewQueue(t, rc)
	if strings.Count(q2, "tidy backlog") != 1 {
		t.Errorf("re-run must not re-propose:\n%s", q2)
	}
}
