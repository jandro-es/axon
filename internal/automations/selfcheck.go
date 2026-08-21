package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/review"
)

const (
	selfCheckState        = "self-check/proposed"
	selfCheckMaxProposals = 5
)

// SelfCheck turns doctor findings into review-queue proposals (FR-207): the
// daemon files its own drifted-install work where the owner already reviews
// work. Zero model calls. **Accepting a proposal never applies anything** —
// it acknowledges, and the owner runs the command. Auto-application of system
// changes is deliberately out of scope (docs/20 G1).
type SelfCheck struct{}

func (SelfCheck) Name() string    { return "self-check" }
func (SelfCheck) Essential() bool { return false }

// proposal is one actionable finding: a check that failed or warned AND
// carries a remediation.
type proposal struct {
	name   string
	detail string
	fix    string
	key    string
}

// sanitizeCheckText makes a check's text safe to round-trip through the queue
// line's regex (review.fixRe). Check text is developer-authored, not user
// input, but the line is parsed back by regex: a stray double quote or
// backtick would break it.
func sanitizeCheckText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, `"`, "'")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}

// actionable filters the report to proposal-worthy checks: not ok, and
// carrying a Fix. The Check type already separates Detail (what is wrong)
// from Fix (what to do), so a check with no Fix has nothing to propose —
// filing it would only train the owner to skim the queue.
func (SelfCheck) actionable(ctx context.Context, rc RunCtx) []proposal {
	if rc.SelfCheck == nil {
		return nil
	}
	var out []proposal
	for _, c := range rc.SelfCheck(ctx) {
		if c.Status == core.StatusOK || strings.TrimSpace(c.Fix) == "" {
			continue
		}
		p := proposal{name: c.Name, detail: sanitizeCheckText(c.Detail), fix: sanitizeCheckText(c.Fix)}
		p.key = hashShort(p.name + "\x00" + p.fix)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// DetectChange hashes the actionable set, so an unchanged system skips with
// no work. A nil seam idles rather than erroring.
func (s SelfCheck) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	if rc.SelfCheck == nil {
		return Change{Changed: false, Reason: "self-check unavailable (no doctor seam wired)"}, nil
	}
	props := s.actionable(ctx, rc)
	var b strings.Builder
	for _, p := range props {
		b.WriteString(p.key)
		b.WriteByte('\n')
	}
	cursor := "selfcheck:" + hashShort(b.String())
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no change in actionable checks"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d actionable check(s)", len(props)), Cursor: cursor}, nil
}

func (s SelfCheck) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	if rc.SelfCheck == nil {
		return RunResult{Summary: "self-check unavailable (no doctor seam wired)"}, nil
	}
	props := s.actionable(ctx, rc)

	// Already pending in the queue — matched on check NAME alone, deliberately
	// coarser than the memory key: if a fix for this check is already waiting
	// for the owner, a second one with different wording is noise, not news.
	pending := map[string]bool{}
	if items, lerr := review.Load(ctx, rc.Vault); lerr == nil {
		for _, it := range items {
			if !it.Checked && it.Kind == "fix" {
				pending[it.Note] = true
			}
		}
	}
	proposed := loadProposalMemory(ctx, rc, selfCheckState)

	var queue []string
	var fresh []proposal
	for _, p := range props {
		if pending[p.name] || proposed[p.key] {
			continue
		}
		queue = append(queue, fmt.Sprintf("- [ ] fix %s — %q → `%s`", p.name, p.detail, p.fix))
		fresh = append(fresh, p)
		if len(queue) >= selfCheckMaxProposals {
			break
		}
	}
	if len(queue) == 0 {
		return RunResult{Summary: "no new fixes to propose"}, nil
	}
	if rc.DryRun {
		return RunResult{
			Summary: fmt.Sprintf("would propose %d fix(es) to review", len(queue)),
			Changes: queue,
		}, nil
	}
	header := fmt.Sprintf("\n## Self-check (%s)\n", today(rc))
	if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
		return RunResult{}, aerr
	}
	for _, p := range fresh {
		proposed[p.key] = true
	}
	saveProposalMemory(ctx, rc, selfCheckState, proposed)
	return RunResult{
		Summary: fmt.Sprintf("proposed %d fix(es) to review", len(queue)),
		Changes: []string{".axon/review-queue.md"},
	}, nil
}
