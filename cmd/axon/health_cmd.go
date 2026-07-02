package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/health"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newHealthCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Score the health of your second brain (index, automations, freshness)",
		Long: "Compute a 0–100 health score for the vault from local state: index &\n" +
			"link integrity, automation reliability, and knowledge freshness. Read-only;\n" +
			"makes no model call and spends no tokens.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			rep, err := health.Compute(cmd.Context(), deps.db, deps.profile)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			// Styled table on a TTY; the plain renderer stays canonical.
			if tui.Interactive(out) {
				rows := make([][]string, 0, len(rep.Dimensions)+1)
				rows = append(rows, []string{"Overall", fmt.Sprintf("%d/100 (%s)", rep.Score, rep.Grade), ""})
				for _, d := range rep.Dimensions {
					rows = append(rows, []string{d.Label, fmt.Sprintf("%d", d.Score), d.Detail})
				}
				fmt.Fprintln(out, ui.For(out).Header(ui.IconHeart, fmt.Sprintf("axon health — profile %q", deps.name)))
				tui.Table(out, []string{"DIMENSION", "SCORE", "DETAIL"}, rows)
				return nil
			}
			renderHealth(out, deps.name, rep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the health report as JSON")
	return cmd
}

func renderHealth(out io.Writer, profileName string, rep health.Report) {
	st := ui.For(out)
	fmt.Fprintln(out, st.Header(ui.IconHeart, fmt.Sprintf("axon health — profile %q", profileName)))
	fmt.Fprintln(out, st.Divider(60))

	overall := fmt.Sprintf("%d/100", rep.Score)
	fmt.Fprintf(out, "  %-26s %s  %s  %s\n",
		st.Bold("Overall"),
		colorScore(st, rep.Score, overall),
		colorScore(st, rep.Score, "("+rep.Grade+")"),
		scoreBar(st, rep.Score, 22))
	fmt.Fprintln(out)

	for _, d := range rep.Dimensions {
		fmt.Fprintf(out, "  %-26s %s  %s\n",
			d.Label,
			colorScore(st, d.Score, fmt.Sprintf("%3d", d.Score)),
			scoreBar(st, d.Score, 22))
		fmt.Fprintf(out, "  %s\n", st.Dim(d.Detail))
	}

	fmt.Fprintln(out, st.Divider(60))
	fmt.Fprintf(out, "%s\n", st.Dim("tip: `axon automations` shows per-automation state; `axon doctor` checks prerequisites"))
}

// scoreBar renders a filled/empty bar of the given width, coloured by score.
func scoreBar(st ui.Styler, score, width int) string {
	filled := score * width / 100
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return colorScore(st, score, bar)
}

// colorScore colours text green/amber/red by score band (mirrors the grade).
func colorScore(st ui.Styler, score int, text string) string {
	switch {
	case score >= 80:
		return st.Green(text)
	case score >= 60:
		return st.Yellow(text)
	default:
		return st.Red(text)
	}
}
