package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
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

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "axon doctor")
			fmt.Fprintln(out, strings.Repeat("─", 40))
			if cfgErr != nil {
				fmt.Fprintf(out, "  note: %v\n", cfgErr)
			}
			for _, c := range report.Checks {
				fmt.Fprintf(out, "  %s  %-20s %s\n", glyph(c.Status), c.Name, c.Detail)
			}
			fmt.Fprintln(out, strings.Repeat("─", 40))
			if report.HasFailure() {
				fmt.Fprintln(out, "status: FAIL")
				return fmt.Errorf("doctor found blocking issues")
			}
			fmt.Fprintln(out, "status: OK")
			return nil
		},
	}
}

func glyph(s core.CheckStatus) string {
	switch s {
	case core.StatusOK:
		return "✔"
	case core.StatusWarn:
		return "⚠"
	default:
		return "✘"
	}
}
