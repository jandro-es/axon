package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tokens"
)

// managerConfig builds the token-manager config from a profile.
func managerConfig(name string, p config.Profile, cfg *config.Config) tokens.Config {
	return tokens.Config{
		Profile:  name,
		AuthMode: p.Claude.AuthMode,
		Models:   p.Models,
		Limits:   p.Limits,
		Prices:   cfg.Prices,
	}
}

func newStatusCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show remaining token budget (day/week) and guard state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			// Status is read-only: no agent or searcher needed.
			mgr := tokens.New(deps.db, nil, nil, nil, managerConfig(deps.name, deps.profile, deps.cfg))
			st, err := mgr.Status(cmd.Context(), deps.name)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			fmt.Fprintf(out, "axon status — profile %q (auth: %s)\n", deps.name, deps.profile.Claude.AuthMode)
			printWindow(cmd, "day ", st.Day)
			printWindow(cmd, "week", st.Week)
			guard := "ok"
			if st.GuardPaused {
				guard = fmt.Sprintf("PAUSED (≥ %d%%)", st.GuardPct)
			}
			fmt.Fprintf(out, "budget-guard: %s\n", guard)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit status as JSON")
	return cmd
}

func printWindow(cmd *cobra.Command, label string, w tokens.Window) {
	remaining := w.Limit - w.Used
	if remaining < 0 {
		remaining = 0
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  %s: %d / %d tokens used (%.1f%%), %d remaining\n",
		label, w.Used, w.Limit, w.Pct, remaining)
}
