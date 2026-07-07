package automations

import (
	"context"
	"fmt"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/eval"
)

// EvalDrift re-runs the eval harness for a gated local tier when its Ollama
// digest has changed since the last recorded eval (FR-143). Default off (S8) —
// it only runs when enabled in config; token-frugal, since the engine's
// change-gate (DetectChange) skips it whenever no digest changed. It evals
// against the concrete model ref, which the promotion gate never redirects, so
// it always measures the real local model.
type EvalDrift struct {
	// digestFn is a test seam; nil uses core.OllamaDigest.
	digestFn func(ctx context.Context, host, model string) (string, bool)
}

// Name is the stable config key.
func (EvalDrift) Name() string { return "eval-drift" }

// Essential reports false: drift re-eval is never budget-critical.
func (EvalDrift) Essential() bool { return false }

func (a EvalDrift) digest(ctx context.Context, host, model string) (string, bool) {
	if a.digestFn != nil {
		return a.digestFn(ctx, host, model)
	}
	return core.OllamaDigest(ctx, host, model)
}

// gatedOllamaTiers returns the (family, ref, model) of each local ollama
// classify/routine tier — the ones the promotion gate governs.
func gatedOllamaTiers(m config.ModelsConfig) []struct{ family, ref, model string } {
	var out []struct{ family, ref, model string }
	for _, t := range []struct{ family, ref string }{{"classify", m.Classify}, {"routine", m.Routine}} {
		if r := config.ParseModelRef(t.ref); r.Provider == config.ProviderOllama {
			out = append(out, struct{ family, ref, model string }{t.family, t.ref, r.Model})
		}
	}
	return out
}

// DetectChange builds a cursor from the current digests of gated local tiers;
// it reports Changed when that differs from the last run. Cheap, no model call.
func (a EvalDrift) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	m := rc.Config.Models
	if m.EvalMinPass == 0 {
		return Change{Changed: false, Reason: "eval gate disabled"}, nil
	}
	cursor := ""
	for _, t := range gatedOllamaTiers(m) {
		d, _ := a.digest(ctx, m.OllamaHost, t.model)
		cursor += fmt.Sprintf("%s=%s;", t.family, d)
	}
	if cursor == rc.LastCursor {
		return Change{Changed: false, Cursor: cursor}, nil
	}
	return Change{Changed: true, Reason: "model digest changed", Cursor: cursor}, nil
}

// Run re-evaluates each gated local tier whose current digest differs from its
// latest eval_runs row (or has none), recording fresh results. It routes eval
// calls through rc.Manager against the concrete model ref (never gate-redirected).
func (a EvalDrift) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	m := rc.Config.Models
	if m.EvalMinPass == 0 {
		return RunResult{}, nil
	}
	var changes []string
	for _, t := range gatedOllamaTiers(m) {
		cur, ok := a.digest(ctx, m.OllamaHost, t.model)
		if !ok {
			continue
		}
		row, have, err := db.LatestEvalRun(ctx, rc.DB, t.family, t.ref)
		if err != nil {
			return RunResult{}, err
		}
		if have && row.Digest == cur {
			continue // no drift
		}
		if rc.DryRun {
			changes = append(changes, fmt.Sprintf("would re-eval %s (%s)", t.ref, t.family))
			continue
		}
		cases, err := eval.LoadCases(t.family)
		if err != nil {
			return RunResult{}, err
		}
		model := t.model
		rep, err := eval.Run(ctx, rc.Manager, cases, eval.Options{
			Model: t.ref, Family: t.family,
			ExpectModel: func(string) string { return model },
		})
		if err != nil {
			return RunResult{}, err
		}
		for _, f := range rep.Families {
			pct := 0
			if f.Total > 0 {
				pct = f.Passed * 100 / f.Total
			}
			if err := db.RecordEvalRun(ctx, rc.DB, db.EvalRun{
				Family: string(f.Family), ModelRef: t.ref, Digest: cur,
				Passed: f.Passed, Total: f.Total, PassPct: pct, RanAt: rc.now(),
			}); err != nil {
				return RunResult{}, err
			}
			changes = append(changes, fmt.Sprintf("re-evaluated %s %s: %d/%d passed", t.ref, f.Family, f.Passed, f.Total))
		}
	}
	return RunResult{Summary: fmt.Sprintf("refreshed %d eval(s)", len(changes)), Changes: changes}, nil
}
