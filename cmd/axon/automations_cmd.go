package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// automationView is one row of `axon automations`: static metadata plus the
// most recent run (nil when it has never run).
type automationView struct {
	automations.Info
	LastRun *db.RunRecord `json:"last_run,omitempty"`
}

func newAutomationsCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "automations",
		Aliases: []string{"autos"},
		Short:   "List automations: which are enabled, what they do, and their last run",
		Long: "Show every built-in automation with its purpose, whether it is enabled\n" +
			"(config + policy), its schedule, and the outcome of its most recent run.\n" +
			"Read-only; makes no model call.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			views, err := buildAutomationViews(cmd.Context(), deps.db, deps.profile)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(views)
			}
			// Styled table on a TTY; the plain renderer stays canonical.
			if tui.Interactive(out) {
				rows := make([][]string, 0, len(views))
				for _, v := range views {
					state := "disabled"
					switch {
					case v.Enabled:
						state = "enabled"
					case v.ConfigEnabled && !v.Allowed:
						state = "blocked"
					}
					model := v.Model
					if model == "" {
						model = "none"
					}
					last := "never run"
					if v.LastRun != nil {
						last = v.LastRun.Status
					}
					rows = append(rows, []string{v.Name, state, model, v.Schedule, last})
				}
				fmt.Fprintln(out, ui.For(out).Header(ui.IconRobot, fmt.Sprintf("axon automations — profile %q", deps.name)))
				tui.Table(out, []string{"NAME", "STATE", "MODEL", "SCHEDULE", "LAST RUN"}, rows)
				return nil
			}
			renderAutomations(out, deps.name, views)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the automation list as JSON")
	return cmd
}

// buildAutomationViews joins the automation catalog with each one's last run.
func buildAutomationViews(ctx context.Context, database *sql.DB, profile config.Profile) ([]automationView, error) {
	infos := automations.Catalog(profile)
	views := make([]automationView, 0, len(infos))
	for _, info := range infos {
		v := automationView{Info: info}
		if rec, found, err := db.LastRun(ctx, database, info.Name); err != nil {
			return nil, err
		} else if found {
			r := rec
			v.LastRun = &r
		}
		views = append(views, v)
	}
	return views, nil
}

func renderAutomations(out io.Writer, profileName string, views []automationView) {
	st := ui.For(out)
	fmt.Fprintln(out, st.Header(ui.IconRobot, fmt.Sprintf("axon automations — profile %q", profileName)))
	fmt.Fprintln(out, st.Divider(64))

	var enabled, essential int
	for _, v := range views {
		if v.Enabled {
			enabled++
		}
		if v.Essential {
			essential++
		}
		renderAutomationRow(out, st, v)
	}

	fmt.Fprintln(out, st.Divider(64))
	fmt.Fprintf(out, "%s\n", st.Dim(fmt.Sprintf("%d of %d enabled · %d essential · run one now with `axon run <name>`",
		enabled, len(views), essential)))
}

func renderAutomationRow(out io.Writer, st ui.Styler, v automationView) {
	// State badge: enabled (green) vs disabled/blocked (dim).
	var badge string
	switch {
	case v.Enabled:
		badge = st.Green(ui.IconOK + " enabled ")
	case v.ConfigEnabled && !v.Allowed:
		badge = st.Yellow(ui.IconWarn + " blocked ") // enabled in config but denied by policy
	default:
		badge = st.Dim(ui.IconDot + " disabled")
	}

	tags := ""
	if v.Essential {
		tags = " " + st.Cyan("[essential]")
	}
	model := v.Model
	if model == "" {
		model = "none"
	}

	fmt.Fprintf(out, "%s  %s%s\n", badge, st.Bold(v.Name), tags)
	fmt.Fprintf(out, "    %s\n", st.Dim(v.Purpose))
	meta := fmt.Sprintf("model %s", model)
	if v.Schedule != "" {
		meta += "  ·  schedule " + v.Schedule
	}
	fmt.Fprintf(out, "    %s\n", st.Dim(meta))
	fmt.Fprintf(out, "    %s %s\n", st.Dim("last run:"), lastRunLine(st, v.LastRun))
}

// lastRunLine renders a coloured one-line summary of the most recent run.
func lastRunLine(st ui.Styler, rec *db.RunRecord) string {
	if rec == nil {
		return st.Dim("never run")
	}
	var icon, label string
	switch rec.Status {
	case db.RunOK:
		icon, label = st.Green(ui.IconOK), st.Green("ok")
	case db.RunSkipped:
		icon, label = st.Cyan(ui.IconAlready), st.Cyan("skipped")
	case db.RunFailed:
		icon, label = st.Red(ui.IconError), st.Red("failed")
	case db.RunDryRun:
		icon, label = st.Dim(ui.IconDot), st.Dim("dry-run")
	default:
		icon, label = st.Yellow(ui.IconDot), st.Yellow(rec.Status)
	}

	when := rec.FinishedAt
	if when == "" {
		when = rec.StartedAt
	}
	parts := icon + " " + label
	if ago := relTime(when); ago != "" {
		parts += st.Dim(" · " + ago)
	}
	if rec.Tokens > 0 {
		parts += st.Dim(fmt.Sprintf(" · %s tokens", humanize.Comma(rec.Tokens)))
	}
	// The most useful detail: why it skipped or how it failed.
	switch rec.Status {
	case db.RunSkipped:
		if rec.SkipReason != "" {
			parts += st.Dim(" — " + rec.SkipReason)
		}
	case db.RunFailed:
		if rec.Error != "" {
			parts += st.Red(" — " + rec.Error)
		}
	}
	return parts
}

// relTime formats an RFC3339 timestamp as a relative "2 hours ago", or "" if it
// cannot be parsed.
func relTime(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return humanize.Time(t)
}
