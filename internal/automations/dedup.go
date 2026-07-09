package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/review"
)

const mergeProposalState = "merge-proposals/proposed"

// MergeProposals sweeps note mean-vectors for near-duplicate pairs and proposes
// merges to the review queue (R7, FR-154). Zero model calls: the vectors already
// exist and the cosine IS the rationale. Accepting a proposal runs the wikilink-
// safe vault.Merge (never a delete); this automation only surfaces candidates.
type MergeProposals struct{}

func (MergeProposals) Name() string    { return "merge-proposals" }
func (MergeProposals) Essential() bool { return false }

func (MergeProposals) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	n, err := db.CountVectors(ctx, rc.DB)
	if err != nil {
		return Change{}, err
	}
	if n == 0 {
		return Change{Changed: false, Reason: "no embeddings yet"}, nil
	}
	year, week := rc.now().UTC().ISOWeek()
	cursor := fmt.Sprintf("merge:%d:%d-%d", n, year, week)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no new embeddings this week"}, nil
	}
	return Change{Changed: true, Reason: "embeddings or week changed", Cursor: cursor}, nil
}

func (MergeProposals) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	threshold := rc.Config.Merge.ThresholdOr()
	maxProps := rc.Config.Merge.MaxProposalsOr()

	// All scannable notes (the all-notes idiom: since 0001-01-01).
	all, err := db.NotesUpdatedSince(ctx, rc.DB, "0001-01-01", 5000)
	if err != nil {
		return RunResult{}, err
	}
	var notes []db.NoteStamp
	present := map[int64]bool{}
	for _, n := range all {
		if !scannableNote(n.Path) {
			continue
		}
		notes = append(notes, n)
		present[n.ID] = true
	}
	if len(notes) < 2 {
		return RunResult{Summary: "merge-proposals: fewer than 2 scannable notes"}, nil
	}

	means, err := db.NoteMeanVectors(ctx, rc.DB, present)
	if err != nil {
		return RunResult{}, err
	}

	// Pending pairs already in the queue — never duplicate.
	pending := map[string]bool{}
	if items, lerr := review.Load(ctx, rc.Vault); lerr == nil {
		for _, it := range items {
			if it.Checked || it.Kind != "merge" {
				continue
			}
			pending[pairKey(it.Note, it.Target)] = true
		}
	}
	// Dismissed pairs — proposal memory suppresses re-nagging.
	proposed := loadProposalMemory(ctx, rc, mergeProposalState)

	type cand struct {
		a, b string
		key  string
		sim  float64
	}
	var cands []cand
	for i := 0; i < len(notes); i++ {
		vi, ok := means[notes[i].ID]
		if !ok {
			continue
		}
		for j := i + 1; j < len(notes); j++ {
			vj, ok := means[notes[j].ID]
			if !ok {
				continue
			}
			sim := db.Cosine(vi, vj)
			if sim < threshold {
				continue
			}
			a, b := stripExt(notes[i].Path), stripExt(notes[j].Path)
			key := pairKey(a, b)
			if pending[key] || proposed[key] {
				continue
			}
			cands = append(cands, cand{a: a, b: b, key: key, sim: sim})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].sim > cands[j].sim })
	if len(cands) > maxProps {
		cands = cands[:maxProps]
	}

	var changes, queue []string
	for _, c := range cands {
		a, b := c.a, c.b
		if b < a {
			a, b = b, a // stable lexical rendering
		}
		line := fmt.Sprintf("merge [[%s]] + [[%s]] (sim %.2f)", a, b, c.sim)
		changes = append(changes, line)
		queue = append(queue, "- [ ] "+line)
	}

	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would propose %d merge(s)", len(changes)), Changes: changes}, nil
	}
	if len(queue) > 0 {
		header := fmt.Sprintf("\n## Near-duplicate merges (%s)\n", rc.now().UTC().Format("2006-01-02 15:04"))
		if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
			return RunResult{}, aerr
		}
		for _, c := range cands {
			proposed[c.key] = true
		}
		saveProposalMemory(ctx, rc, mergeProposalState, proposed)
	}
	return RunResult{Summary: fmt.Sprintf("proposed %d merge(s)", len(changes)), Changes: changes}, nil
}
