package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/tui"
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
			// Styled table on a TTY; the plain lines below stay canonical.
			if tui.Interactive(out) {
				guard := ui.IconOK + " ok"
				if st.GuardPaused {
					guard = fmt.Sprintf("%s PAUSED (≥ %d%%) %s", ui.IconWarn, st.GuardPct, st.GuardReason)
				}
				rows := [][]string{
					{"day", fmt.Sprintf("%d", st.Day.Used), fmt.Sprintf("%d", st.Day.Limit), fmt.Sprintf("%.1f%%", st.Day.Pct)},
					{"week", fmt.Sprintf("%d", st.Week.Used), fmt.Sprintf("%d", st.Week.Limit), fmt.Sprintf("%.1f%%", st.Week.Pct)},
				}
				fmt.Fprintf(out, "%s %s\n", ui.For(out).Header(ui.IconChart, "axon status"),
					ui.For(out).Dim(fmt.Sprintf("— profile %q (auth: %s)", deps.name, deps.profile.Claude.AuthMode)))
				tui.Table(out, []string{"WINDOW", "USED", "LIMIT", "PCT"}, rows)
				fmt.Fprintf(out, "budget-guard: %s\n", guard)
				return nil
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
	// Dollar tracking appears only in api_key mode (FR-42): capped on the day
	// window, informational on the week window.
	if w.CostCap > 0 {
		fmt.Fprintf(out, "        cost $%.2f / $%.2f (%.1f%% of daily cap)\n", w.CostUsed, w.CostCap, w.CostPct)
	} else if w.CostUsed > 0 {
		fmt.Fprintf(out, "        cost $%.2f\n", w.CostUsed)
	}
}
