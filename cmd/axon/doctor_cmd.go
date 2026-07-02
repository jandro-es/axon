package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/selfupdate"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newDoctorCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report prerequisite and configuration health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Load secrets first so checks can see env-provided values; a
			// missing .env is fine.
			_ = config.LoadDotEnv(gf.envPath)

			// Config is best-effort: doctor still reports prerequisite status
			// even when the config is missing or invalid.
			cfg, cfgErr := config.Load(gf.configPath)
			activeProfile := gf.profile
			if cfg != nil {
				activeProfile = cfg.ResolveProfileName(gf.profile)
			}

			report := core.Doctor(cfg, activeProfile)
			report.Checks = append(report.Checks, updateAvailabilityCheck())

			out := cmd.OutOrStdout()

			// Live step view on a TTY; the plain report below stays canonical.
			if tui.Interactive(out) {
				steps := tui.NewSteps(out, "axon doctor", nil)
				steps.Start()
				for _, c := range report.Checks {
					steps.Set(c.Name, c.Detail, doctorStepStatus(c.Status))
				}
				if report.HasFailure() {
					_ = steps.Finish("status: FAIL")
					return fmt.Errorf("doctor found blocking issues — see the failing check(s) above")
				}
				return steps.Finish("status: OK")
			}

			st := ui.For(out)
			fmt.Fprintln(out, st.Header(ui.IconDoctor, "axon doctor"))
			fmt.Fprintln(out, st.Divider(40))
			if cfgErr != nil {
				fmt.Fprintf(out, "  %s %s\n", st.Yellow(ui.IconWarn), st.Dim(fmt.Sprintf("config: %v", cfgErr)))
			}
			for _, c := range report.Checks {
				detail := c.Detail
				switch c.Status {
				case core.StatusWarn:
					detail = st.Yellow(detail)
				case core.StatusFail:
					detail = st.Red(detail)
				}
				fmt.Fprintf(out, "  %s  %-20s %s\n", glyph(st, c.Status), c.Name, detail)
			}
			fmt.Fprintln(out, st.Divider(40))
			if report.HasFailure() {
				fmt.Fprintf(out, "%s %s\n", st.Red(ui.IconError), st.Bold(st.Red("status: FAIL")))
				return fmt.Errorf("doctor found blocking issues — see the failing check(s) above")
			}
			fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconOK), st.Bold(st.Green("status: OK")))
			return nil
		},
	}
}

// updateAvailabilityCheck reads ONLY the daily update-check cache (written by
// `axon update`, `axon version --check` and the daemon's background check) —
// doctor itself never touches the network for this.
func updateAvailabilityCheck() core.Check {
	const name = "update-available"
	current, _, _ := buildVersion()
	c, ok := readUpdateCache()
	if !ok {
		return core.Check{Name: name, Status: core.StatusOK,
			Detail: "no release check recorded yet — run `axon version --check`"}
	}
	if selfupdate.IsNewer(current, c.Latest) {
		return core.Check{Name: name, Status: core.StatusWarn,
			Detail: fmt.Sprintf("v%s available (running %s) — run `axon update`", c.Latest, current)}
	}
	return core.Check{Name: name, Status: core.StatusOK,
		Detail: fmt.Sprintf("up to date (latest known release: %s, checked %s)", c.Latest, c.CheckedAt.Format("2006-01-02"))}
}

// doctorStepStatus maps a doctor check status onto the tui step vocabulary.
func doctorStepStatus(s core.CheckStatus) tui.StepStatus {
	switch s {
	case core.StatusOK:
		return tui.StatusDone
	case core.StatusWarn:
		return tui.StatusWarn
	default:
		return tui.StatusFailed
	}
}

func glyph(st ui.Styler, s core.CheckStatus) string {
	switch s {
	case core.StatusOK:
		return st.Green(ui.IconOK)
	case core.StatusWarn:
		return st.Yellow(ui.IconWarn)
	default:
		return st.Red(ui.IconError)
	}
}
