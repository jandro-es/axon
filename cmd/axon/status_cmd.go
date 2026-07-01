package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/ui"
)

// managerConfig builds the token-manager config from a profile.
func managerConfig(name string, p config.Profile, cfg *config.Config) tokens.Config {
	return tokens.Config{
		Profile:        name,
		AuthMode:       p.Claude.AuthMode,
		Models:         p.Models,
		Limits:         p.Limits,
		Prices:         cfg.Prices,
		RedactionRules: p.Policy.RedactionRules,
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
			s := ui.For(out)
			fmt.Fprintf(out, "%s %s\n",
				s.Header(ui.IconChart, "axon status"),
				s.Dim(fmt.Sprintf("— profile %q (auth: %s)", deps.name, deps.profile.Claude.AuthMode)))
			printWindow(cmd, "day ", st.Day)
			printWindow(cmd, "week", st.Week)
			if st.GuardPaused {
				fmt.Fprintf(out, "budget-guard: %s\n", s.Bold(s.Red(fmt.Sprintf("%s PAUSED (≥ %d%%)", ui.IconWarn, st.GuardPct))))
				if st.GuardReason != "" {
					fmt.Fprintf(out, "  %s\n", s.Dim(st.GuardReason))
				}
			} else {
				fmt.Fprintf(out, "budget-guard: %s\n", s.Green(ui.IconOK+" ok"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit status as JSON")
	return cmd
}

func printWindow(cmd *cobra.Command, label string, w tokens.Window) {
	out := cmd.OutOrStdout()
	s := ui.For(out)
	remaining := w.Limit - w.Used
	if remaining < 0 {
		remaining = 0
	}
	// Colour the usage percentage by pressure: green under 75%, amber up to 90%,
	// red beyond — so a tight budget is obvious at a glance.
	pct := fmt.Sprintf("%.1f%%", w.Pct)
	switch {
	case w.Pct >= 90:
		pct = s.Red(pct)
	case w.Pct >= 75:
		pct = s.Yellow(pct)
	default:
		pct = s.Green(pct)
	}
	fmt.Fprintf(out, "  %s: %d / %d tokens used (%s), %d remaining\n",
		label, w.Used, w.Limit, pct, remaining)
}
