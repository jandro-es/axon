package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/selfupdate"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// doctorJSONCheck is the machine-readable form of a core.Check. The daemon
// folds its remediation advice into Detail, so consumers render Detail as the
// remediation text (see apps/companion/CONTRACT.md).
type doctorJSONCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	// Remediation is the concrete command to run. Present only when there is
	// one; Detail alone says what is wrong, not what to do about it.
	Remediation string `json:"remediation,omitempty"`
}

// doctorJSONReport is what `axon doctor --json` emits. Status is the derived
// overall verdict so consumers need not re-implement HasFailure.
type doctorJSONReport struct {
	Profile string            `json:"profile"`
	Status  string            `json:"status"`
	Error   string            `json:"error,omitempty"`
	Checks  []doctorJSONCheck `json:"checks"`
}

func newDoctorCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
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

			report := core.Doctor(cfg, activeProfile, selfCheckExtras(cfg, activeProfile)...)

			out := cmd.OutOrStdout()

			// Machine-readable first: no styling, no TTY branch, and a
			// non-zero exit on failure exactly like the human renderer.
			if asJSON {
				rep := doctorJSONReport{
					Profile: activeProfile,
					Status:  "ok",
					Checks:  make([]doctorJSONCheck, 0, len(report.Checks)),
				}
				if report.HasFailure() {
					rep.Status = "fail"
				}
				if cfgErr != nil {
					rep.Error = cfgErr.Error()
				}
				for _, c := range report.Checks {
					rep.Checks = append(rep.Checks, doctorJSONCheck{
						Name: c.Name, Status: string(c.Status), Detail: c.Detail,
						Remediation: c.Fix,
					})
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
				if report.HasFailure() {
					return fmt.Errorf("doctor found blocking issues — see the failing check(s) in the report")
				}
				return nil
			}

			// Live step view on a TTY; the plain report below stays canonical.
			if tui.Interactive(out) {
				steps := tui.NewSteps(out, "axon doctor", nil)
				steps.Start()
				for _, c := range report.Checks {
					detail := c.Detail
					if c.Fix != "" {
						detail += "  ↳ " + c.Fix
					}
					steps.Set(c.Name, detail, doctorStepStatus(c.Status))
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
				if c.Fix != "" {
					fmt.Fprintf(out, "     %s %s\n", st.Dim("↳ fix:"), st.Bold(c.Fix))
				}
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
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the doctor report as JSON")
	return cmd
}

// selfCheckExtras builds the two checks core cannot compute: update
// availability (needs the build version) and recipes (lives in automations,
// which core cannot import). Both the `axon doctor` command and the daemon's
// self-check seam call this, so the two reports are identical by construction
// (FR-206) rather than by two lists someone has to keep in sync.
func selfCheckExtras(cfg *config.Config, activeProfile string) []core.Check {
	extras := []core.Check{updateAvailabilityCheck()}
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			extras = append(extras, automations.RecipesCheck(p))
		}
	}
	return extras
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
