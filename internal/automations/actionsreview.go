package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/review"
)

// ActionsReview is the zero-model weekly GTD hygiene sweep (T5): it proposes open,
// undated actions in notes untouched for > actions.stale_after_days to the review
// queue. Accepting a proposal demotes the task to Someday/Maybe (#someday); dismiss
// silences it (proposal memory). Off by default.
type ActionsReview struct{}

func (ActionsReview) Name() string    { return "actions-review" }
func (ActionsReview) Essential() bool { return false }

const (
	actionsReviewState = "actions-review/proposed"
	staleMaxProposals  = 10
)

// staleCandidates returns open, undated actions whose source note's `updated`
// predates today − stale_after_days, plus the stale-note → updated map.
func (ActionsReview) staleCandidates(ctx context.Context, rc RunCtx) ([]db.Action, map[string]string, error) {
	cutoff := rc.now().UTC().AddDate(0, 0, -rc.Config.Actions.StaleAfterDaysOr()).Format("2006-01-02")
	stale, err := db.NotesUpdatedBefore(ctx, rc.DB, cutoff)
	if err != nil {
		return nil, nil, err
	}
	updated := map[string]string{}
	for _, n := range stale {
		if scannableNote(n.Path) {
			updated[n.Path] = n.Updated
		}
	}
	open, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})
	if err != nil {
		return nil, nil, err
	}
	var cands []db.Action
	for _, act := range open {
		if act.Due != "" {
			continue // only undated
		}
		if _, ok := updated[act.SourcePath]; !ok {
			continue // note not stale
		}
		cands = append(cands, act)
	}
	return cands, updated, nil
}

func (a ActionsReview) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	cands, _, err := a.staleCandidates(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	var sb strings.Builder
	for _, c := range cands {
		sb.WriteString(c.Hash)
		sb.WriteByte('\n')
	}
	cursor := fmt.Sprintf("stale:%s:%s", weekStart(rc).Format("2006-01-02"), hashShort(sb.String()))
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no new stale actions this week"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d stale candidate(s)", len(cands)), Cursor: cursor}, nil
}

func (a ActionsReview) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	cands, updated, err := a.staleCandidates(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	pending := map[string]bool{}
	if items, lerr := review.Load(ctx, rc.Vault); lerr == nil {
		for _, it := range items {
			if !it.Checked && it.Kind == "stalled" {
				pending[it.Note+"\x00"+it.Target] = true
			}
		}
	}
	proposed := loadProposalMemory(ctx, rc, actionsReviewState)
	today := rc.now().UTC()

	type prop struct{ hash, line string }
	var props []prop
	for _, c := range cands {
		if proposed[c.Hash] {
			continue
		}
		note := stripExt(c.SourcePath)
		if pending[note+"\x00"+c.Text] {
			continue
		}
		age := int(today.Sub(parseDay(updated[c.SourcePath])).Hours() / 24)
		props = append(props, prop{
			hash: c.Hash,
			line: fmt.Sprintf("stalled action %q in [[%s]] (%dd) — still relevant?", c.Text, note, age),
		})
	}
	sort.Slice(props, func(i, j int) bool { return props[i].line < props[j].line })
	if len(props) > staleMaxProposals {
		props = props[:staleMaxProposals]
	}

	changes := make([]string, 0, len(props))
	queue := make([]string, 0, len(props))
	for _, p := range props {
		changes = append(changes, p.line)
		queue = append(queue, "- [ ] "+p.line)
	}
	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would propose %d stale action(s)", len(changes)), Changes: changes}, nil
	}
	if len(queue) > 0 {
		header := fmt.Sprintf("\n## Stale actions (%s)\n", today.Format("2006-01-02 15:04"))
		if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
			return RunResult{}, aerr
		}
		for _, p := range props {
			proposed[p.hash] = true
		}
		saveProposalMemory(ctx, rc, actionsReviewState, proposed)
	}
	return RunResult{Summary: fmt.Sprintf("actions-review proposed %d stale action(s)", len(changes)), Changes: changes}, nil
}

// parseDay turns a YYYY-MM-DD stamp into a time; zero on failure.
func parseDay(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}
