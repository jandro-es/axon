package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// bucketOrder is the GTD engage order for display + the `week` aggregate slot.
var bucketOrder = map[string]int{
	"overdue": 0, "today": 1, "week": 2, "next": 3,
	"waiting": 4, "someday": 5, "scheduled": 6, "done": 7, "cancelled": 8,
}

// actionItem is one CLI row: the parsed action plus its read-time bucket.
// It embeds actions.Action so --json carries the json-tagged fields.
type actionItem struct {
	actions.Action
	Bucket string `json:"bucket"`
}

func newActionsCmd(gf *globalFlags) *cobra.Command {
	var status, project, contextFilter string
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List and filter actions (tasks) across the vault — no model call (FR-159)",
		Long: "Lists checkbox tasks parsed from the whole vault, grouped by GTD bucket\n" +
			"(overdue/today/next/waiting/someday/…). Read-only, zero tokens.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			rows, err := db.ListActions(cmd.Context(), deps.db, db.ListActionsOpts{IncludeAll: all})
			if err != nil {
				return err
			}
			today := time.Now()
			items := make([]actionItem, 0, len(rows))
			for _, r := range rows {
				av := toActionValue(r)
				b := actions.Bucket(av, today)
				if !all && (b == "done" || b == "cancelled") {
					continue
				}
				items = append(items, actionItem{av, b})
			}
			items, err = filterActions(items, status, project, contextFilter, today)
			if err != nil {
				return err
			}
			sort.SliceStable(items, func(i, j int) bool {
				if bi, bj := bucketOrder[items[i].Bucket], bucketOrder[items[j].Bucket]; bi != bj {
					return bi < bj
				}
				return items[i].Due < items[j].Due
			})

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			renderActions(out, rows, items, today)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter: a bucket (overdue|today|scheduled|next|waiting|someday|done|cancelled) or open|week")
	cmd.Flags().StringVar(&project, "project", "", "filter by project (wikilink target or source-path substring)")
	cmd.Flags().StringVar(&contextFilter, "context", "", "filter by @context (without the @)")
	cmd.Flags().BoolVar(&all, "all", false, "include done, cancelled and archived actions")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit results as JSON")
	return cmd
}

func toActionValue(r db.Action) actions.Action {
	return actions.Action{
		SourcePath: r.SourcePath, LineNo: r.LineNo, Section: r.Section, Text: r.Text, Raw: r.Raw,
		State: actions.State(r.State), Checkbox: r.Checkbox, Priority: r.Priority,
		Due: r.Due, Scheduled: r.Scheduled, Start: r.Start, DoneDate: r.DoneDate,
		Project: r.Project, Contexts: r.Contexts, Tags: r.Tags, Archived: r.Archived,
	}
}

func filterActions(items []actionItem, status, project, ctx string, today time.Time) ([]actionItem, error) {
	valid := map[string]bool{"overdue": true, "today": true, "scheduled": true,
		"next": true, "waiting": true, "someday": true, "done": true,
		"cancelled": true, "open": true, "week": true}
	if status != "" && !valid[status] {
		return nil, fmt.Errorf("unknown --status %q (want one of: overdue today scheduled next waiting someday done cancelled open week)", status)
	}
	weekEnd := today.AddDate(0, 0, 7).Format("2006-01-02")
	tStr := today.Format("2006-01-02")
	out := items[:0]
	for _, it := range items {
		if status != "" {
			switch status {
			case "open": // already open-only unless --all; keep
			case "week":
				if !(it.Due != "" && it.Due >= tStr && it.Due <= weekEnd) {
					continue
				}
			default:
				if it.Bucket != status {
					continue
				}
			}
		}
		if project != "" && it.Project != project && !strings.Contains(it.SourcePath, project) {
			continue
		}
		if ctx != "" && !hasCtx(it.Contexts, ctx) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func hasCtx(cs []string, want string) bool {
	for _, c := range cs {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}

func renderActions(out io.Writer, all []db.Action, items []actionItem, today time.Time) {
	sty := ui.For(out)
	c := summarizeActions(all, today)
	fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf(
		"Open %d · Overdue %d · Today %d · Waiting %d · Someday %d · Done(7d) %d",
		c["open"], c["overdue"], c["today"], c["waiting"], c["someday"], c["done7"])))
	if len(items) == 0 {
		fmt.Fprintf(out, "%s\n", sty.Dim("no matching actions"))
		return
	}
	if tui.Interactive(out) {
		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{it.Bucket, it.Due, it.Text, it.SourcePath})
		}
		tui.Table(out, []string{"BUCKET", "DUE", "TASK", "SOURCE"}, rows)
		return
	}
	for _, it := range items {
		due := it.Due
		if due == "" {
			due = "—"
		}
		fmt.Fprintf(out, "%s  %s  %s %s\n",
			sty.Bold(fmt.Sprintf("%-9s", it.Bucket)), sty.Dim(due),
			it.Text, sty.Dim("("+it.SourcePath+")"))
	}
}

func summarizeActions(rows []db.Action, today time.Time) map[string]int {
	c := map[string]int{}
	weekAgo := today.AddDate(0, 0, -7).Format("2006-01-02")
	for _, r := range rows {
		switch b := actions.Bucket(toActionValue(r), today); b {
		case "done":
			if r.DoneDate >= weekAgo {
				c["done7"]++
			}
		case "cancelled":
			// not counted
		default:
			c["open"]++
			c[b]++
		}
	}
	return c
}
