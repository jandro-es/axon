package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/eval"
	"github.com/jandro-es/axon/internal/ui"
)

func newEvalCmd(gf *globalFlags) *cobra.Command {
	var family, model string
	var asJSON bool
	var minPass int
	var noSave bool
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate a local (or any) model against AXON's golden task sets",
		Long: "Runs in-repo golden sets (classify + routine families) through the token\n" +
			"chokepoint and grades them hybrid: deterministic for classify, must_include +\n" +
			"a Claude judge for routine. Eval calls run fail-fast (local_fallback: fail) so a\n" +
			"broken local model is scored failed/escalated, never silently answered by Claude\n" +
			"(FR-140/141). With no --model each family runs against its configured tier.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cases, err := eval.LoadCases(family)
			if err != nil {
				return err
			}
			if len(cases) == 0 {
				return fmt.Errorf("no golden cases for family %q", family)
			}
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()
			mgr, expect := deps.evalManager(nil)

			rep, err := eval.Run(cmd.Context(), mgr, cases, eval.Options{
				Model: model, Family: family, ExpectModel: expect,
			})
			if err != nil {
				return err
			}

			if !noSave {
				host := deps.profile.Models.OllamaHost
				digestOf := func(ref string) string {
					r := config.ParseModelRef(ref)
					if r.Provider != config.ProviderOllama {
						return ""
					}
					dg, _ := core.OllamaDigest(cmd.Context(), host, r.Model)
					return dg
				}
				if err := persistEvalRuns(cmd.Context(), deps.db, rep, digestOf); err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			if asJSON {
				if err := writeJSON(out, rep); err != nil {
					return err
				}
			} else {
				writeScorecard(out, rep)
			}
			if minPass > 0 && !rep.MinPass(minPass) {
				return fmt.Errorf("eval below --min-pass %d%%", minPass)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&family, "family", "all", "task family: classify|routine|synthesis|all")
	cmd.Flags().StringVar(&model, "model", "", "model ref to evaluate (e.g. ollama:qwen2.5); default: configured tier")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	cmd.Flags().IntVar(&minPass, "min-pass", 0, "exit non-zero if any family's pass rate is below this percent")
	cmd.Flags().BoolVar(&noSave, "no-save", false, "do not persist results to eval_runs")
	return cmd
}

// persistEvalRuns writes one eval_runs row per family. digestOf resolves a
// family's model ref to its current digest ("" when unavailable / not ollama).
func persistEvalRuns(ctx context.Context, ex db.Execer, rep eval.Report, digestOf func(ref string) string) error {
	for _, f := range rep.Families {
		pct := 0
		if f.Total > 0 {
			pct = f.Passed * 100 / f.Total
		}
		if err := db.RecordEvalRun(ctx, ex, db.EvalRun{
			Family: string(f.Family), ModelRef: f.Model, Digest: digestOf(f.Model),
			Passed: f.Passed, Total: f.Total, PassPct: pct, RanAt: time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// writeJSON emits the machine-readable report.
func writeJSON(w io.Writer, rep eval.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// writeScorecard prints a per-family scorecard plus per-case pass/fail lines.
func writeScorecard(w io.Writer, rep eval.Report) {
	sty := ui.For(w)
	for _, f := range rep.Families {
		fmt.Fprintf(w, "\n%s  %s %s\n", sty.Bold(string(f.Family)), sty.Dim("model:"), f.Model)
		fmt.Fprintf(w, "  %s %d/%d passed", pctLabel(sty, f), f.Passed, f.Total)
		if f.Escalated > 0 {
			fmt.Fprintf(w, ", %s escalated", sty.Yellow(fmt.Sprintf("%d", f.Escalated)))
		}
		if f.Failed > 0 {
			fmt.Fprintf(w, ", %s failed", sty.Red(fmt.Sprintf("%d", f.Failed)))
		}
		fmt.Fprintln(w)
		for _, c := range f.Cases {
			mark, detail := sty.Green("✓"), ""
			switch {
			case c.Verdict.Escalated:
				mark, detail = sty.Yellow("↑"), "  "+sty.Dim(c.Verdict.Reason)
			case !c.Verdict.Pass:
				mark, detail = sty.Red("✗"), "  "+sty.Dim(c.Verdict.Reason)
			}
			fmt.Fprintf(w, "    %s %s%s\n", mark, c.Name, detail)
		}
	}
}

// pctLabel colours the pass rate green at 100%, else yellow.
func pctLabel(sty ui.Styler, f eval.FamilyReport) string {
	pct := 0
	if f.Total > 0 {
		pct = f.Passed * 100 / f.Total
	}
	s := fmt.Sprintf("%d%%", pct)
	if pct == 100 {
		return sty.Green(s)
	}
	return sty.Yellow(s)
}
