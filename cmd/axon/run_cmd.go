package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newRunCmd(gf *globalFlags) *cobra.Command {
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "run <automation>",
		Short: "Run a single automation on demand (same code path as the scheduler)",
		Long: "Run one automation through the full lifecycle: change-gate (skip with no\n" +
			"model call when nothing changed), budget pre-check, work, accounting. With\n" +
			"--dry-run it prints intended edits + a token estimate and writes nothing.\n\n" +
			"Automations: " + strings.Join(automations.Names(config.Profile{}), ", "),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			if !automations.AllowedByPolicy(deps.profile, name) {
				return fmt.Errorf("automation %q is not permitted by policy.allowed_automations", name)
			}
			a, err := automations.Get(deps.profile, name)
			if err != nil {
				return err
			}

			engine := deps.buildEngine(nil)
			w := cmd.OutOrStdout()

			// Live spinner on a TTY; plain path below stays canonical.
			if !asJSON && tui.Interactive(w) {
				var out automations.Outcome
				_ = tui.Spin(w, "running "+name+"…", func() (string, error) {
					var rerr error
					out, rerr = engine.Run(cmd.Context(), a, dryRun)
					if rerr != nil {
						return "", rerr
					}
					return name + ": " + out.Status, nil
				})
				printOutcome(w, out)
				if out.Status == "failed" {
					return fmt.Errorf("%s", out.Err)
				}
				return nil
			}

			out, runErr := engine.Run(cmd.Context(), a, dryRun)

			if asJSON {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
				return runErr
			}
			printOutcome(w, out)
			return runErr
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "compute and print intended edits + token estimate, write nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the outcome as JSON")
	return cmd
}

func printOutcome(w io.Writer, out automations.Outcome) {
	st := ui.For(w)
	name := st.Bold(out.Automation)
	switch out.Status {
	case "skipped":
		// The skip reason is the useful part (change-gate, budget guard, locked) —
		// give it room and dim it so it reads as an explanation, not an error.
		fmt.Fprintf(w, "%s %s %s\n", st.Cyan(ui.IconAlready), name, st.Cyan("skipped"))
		fmt.Fprintf(w, "  %s %s\n", st.Dim("reason:"), st.Dim(out.SkipReason))
	case "failed":
		fmt.Fprintf(w, "%s %s %s\n", st.Red(ui.IconError), name, st.Red("failed"))
		fmt.Fprintf(w, "  %s %s\n", st.Dim("error: "), st.Red(out.Err))
		fmt.Fprintf(w, "  %s %s\n", st.Yellow(ui.IconArrow),
			st.Dim("preview with `axon run "+out.Automation+" --dry-run`, or check prerequisites with `axon doctor`"))
	case "dry-run":
		fmt.Fprintf(w, "%s %s %s\n", st.Bold("dry-run"), name, st.Dim("— "+out.Summary))
		for _, c := range out.Changes {
			fmt.Fprintf(w, "  %s %s\n", st.Dim("·"), c)
		}
		if out.Estimated > 0 {
			fmt.Fprintf(w, "  %s ~%s tokens\n", st.Dim("estimated input:"), humanize.Comma(int64(out.Estimated)))
		}
	default:
		fmt.Fprintf(w, "%s %s %s\n", st.Green(ui.IconOK), name, st.Dim("— "+out.Summary))
		for _, c := range out.Changes {
			fmt.Fprintf(w, "  %s %s\n", st.Dim("·"), c)
		}
		if out.Tokens > 0 {
			fmt.Fprintf(w, "  %s %s\n", st.Dim("tokens:"), st.Bold(humanize.Comma(out.Tokens)))
		}
	}
}
