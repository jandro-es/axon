package eval

import (
	"context"
	"fmt"
	"sort"

	"github.com/jandro-es/axon/internal/tokens"
)

// Chokepoint is the minimal surface the runner needs — satisfied by
// *tokens.Manager. Defined at the consumer so the runner is unit-testable with a
// fake and never imports internal/agent (cardinal rule 1).
type Chokepoint interface {
	Run(ctx context.Context, call tokens.AgentCall) (tokens.AgentResult, error)
}

// Options parameterise a run.
type Options struct {
	// Model overrides the target for every case (e.g. "ollama:qwen2.5"); "" runs
	// each family against its configured tier alias (the family name).
	Model string
	// Family filters to one family; "" or "all" runs all present families.
	Family string
	// ExpectModel resolves the model key a call was sent with to the bare model
	// string the chokepoint should return. A mismatch is scored Escalated. The
	// CLI supplies it from config; a nil resolver disables the check.
	ExpectModel func(modelKey string) string
}

// CaseResult is one graded case.
type CaseResult struct {
	Name    string
	Verdict Verdict
}

// FamilyReport aggregates one family's results.
type FamilyReport struct {
	Family    Family
	Model     string // the target ref cases ran against (display)
	Total     int
	Passed    int
	Escalated int
	Failed    int
	Cases     []CaseResult
}

// Report is the full scorecard.
type Report struct {
	Families []FamilyReport
}

// MinPass reports whether every family's pass rate is >= pct percent.
func (r Report) MinPass(pct int) bool {
	for _, f := range r.Families {
		if f.Total == 0 {
			continue
		}
		if f.Passed*100 < pct*f.Total {
			return false
		}
	}
	return true
}

// Run evaluates cases through cp and returns a Report. For each case it issues
// one target call; records escalation by comparing AgentResult.Model against the
// intended bare model; grades; and — for routine cases with a Rubric that pass
// the must_include gate — issues one judge call. Never mutates the vault.
func Run(ctx context.Context, cp Chokepoint, cases []Case, opts Options) (Report, error) {
	byFamily := map[Family][]Case{}
	for _, c := range cases {
		byFamily[c.Family] = append(byFamily[c.Family], c)
	}
	fams := make([]Family, 0, len(byFamily))
	for f := range byFamily {
		fams = append(fams, f)
	}
	sort.Slice(fams, func(i, j int) bool { return fams[i] < fams[j] })

	var rep Report
	for _, fam := range fams {
		modelKey := opts.Model
		if modelKey == "" {
			modelKey = string(fam)
		}
		fr := FamilyReport{Family: fam, Model: modelKey}
		for _, c := range byFamily[fam] {
			v := runCase(ctx, cp, c, modelKey, opts.ExpectModel)
			fr.Cases = append(fr.Cases, CaseResult{Name: c.Name, Verdict: v})
			fr.Total++
			switch {
			case v.Escalated:
				fr.Escalated++
			case v.Pass:
				fr.Passed++
			default:
				fr.Failed++
			}
		}
		rep.Families = append(rep.Families, fr)
	}
	return rep, nil
}

// runCase issues the target call, checks escalation, then grades.
func runCase(ctx context.Context, cp Chokepoint, c Case, modelKey string, expect func(string) string) Verdict {
	res, err := cp.Run(ctx, tokens.AgentCall{
		Operation: "eval.target",
		ModelKey:  modelKey,
		System:    c.System,
		Messages:  []tokens.Message{{Role: "user", Content: c.Prompt}},
	})
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("target call failed: %v", err)}
	}
	if expect != nil {
		if want := expect(modelKey); want != "" && res.Model != want {
			return Verdict{Escalated: true, Reason: fmt.Sprintf("answer came from %q, not target %q", res.Model, want)}
		}
	}
	if c.Family == FamilyClassify {
		return gradeClassify(c, res.Text)
	}
	return gradeRoutine(ctx, cp, c, res.Text)
}

// gradeRoutine applies the must_include gate then, if a Rubric is set, one judge
// call through the chokepoint.
func gradeRoutine(ctx context.Context, cp Chokepoint, c Case, got string) Verdict {
	if ok, missing := mustInclude(c.Grade.MustInclude, got); !ok {
		return Verdict{Reason: fmt.Sprintf("missing required anchor %q", missing)}
	}
	if c.Grade.Rubric == "" {
		return Verdict{Pass: true}
	}
	res, err := cp.Run(ctx, tokens.AgentCall{
		Operation:      "eval.judge",
		ModelKey:       "synthesis", // always Claude (ADR-015); ledgered like any call
		System:         judgeSystem,
		Messages:       []tokens.Message{{Role: "user", Content: judgePrompt(c.Grade.Rubric, got)}},
		ValidateOutput: validateJudgeOutput,
	})
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("judge call failed: %v", err)}
	}
	pass, reason, err := parseJudge(res.Text)
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("judge verdict unparseable: %v", err)}
	}
	if !pass {
		return Verdict{Reason: "judge: " + reason}
	}
	return Verdict{Pass: true}
}
