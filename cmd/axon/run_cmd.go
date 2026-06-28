package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
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
			out, runErr := engine.Run(cmd.Context(), a, dryRun)

			w := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
				return runErr
			}
			printOutcome(cmd, out)
			return runErr
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "compute and print intended edits + token estimate, write nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the outcome as JSON")
	return cmd
}

func printOutcome(cmd *cobra.Command, out automations.Outcome) {
	w := cmd.OutOrStdout()
	switch out.Status {
	case "skipped":
		fmt.Fprintf(w, "↻ %s skipped: %s\n", out.Automation, out.SkipReason)
	case "failed":
		fmt.Fprintf(w, "✗ %s failed: %s\n", out.Automation, out.Err)
	case "dry-run":
		fmt.Fprintf(w, "dry-run %s: %s\n", out.Automation, out.Summary)
		for _, c := range out.Changes {
			fmt.Fprintf(w, "  - %s\n", c)
		}
		if out.Estimated > 0 {
			fmt.Fprintf(w, "  estimated input: ~%d tokens\n", out.Estimated)
		}
	default:
		fmt.Fprintf(w, "✓ %s: %s\n", out.Automation, out.Summary)
		for _, c := range out.Changes {
			fmt.Fprintf(w, "  - %s\n", c)
		}
		if out.Tokens > 0 {
			fmt.Fprintf(w, "  tokens: %d\n", out.Tokens)
		}
	}
}
