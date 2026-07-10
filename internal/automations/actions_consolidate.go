package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
)

const (
	actionsNotePath = "01-Projects/Actions.md"
	actionsBlock    = "actions"
)

func actionFromRow(r db.Action) actions.Action {
	return actions.Action{
		SourcePath: r.SourcePath, LineNo: r.LineNo, Section: r.Section, Text: r.Text, Raw: r.Raw,
		State: actions.State(r.State), Checkbox: r.Checkbox, Priority: r.Priority,
		Due: r.Due, Scheduled: r.Scheduled, Start: r.Start, DoneDate: r.DoneDate,
		Project: r.Project, Contexts: r.Contexts, Tags: r.Tags, Archived: r.Archived,
	}
}

func priorityGlyph(p string) string {
	switch p {
	case "highest":
		return "🔺"
	case "high":
		return "⏫"
	case "medium":
		return "🔼"
	case "low":
		return "🔽"
	case "lowest":
		return "⏬"
	}
	return ""
}

// actionRefLine renders one action as a plain-list REFERENCE (never a checkbox).
func actionRefLine(a actions.Action) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(a.Text)
	b.WriteString(" — [[")
	b.WriteString(stripExt(a.SourcePath))
	b.WriteString("]]")
	if a.Section != "" {
		b.WriteString(" · ")
		b.WriteString(a.Section)
	}
	if a.Due != "" {
		b.WriteString(" · 📅 ")
		b.WriteString(a.Due)
	}
	if g := priorityGlyph(a.Priority); g != "" {
		b.WriteString(" ")
		b.WriteString(g)
	}
	return b.String()
}

func sortByDueThenText(as []actions.Action) {
	sort.SliceStable(as, func(i, j int) bool {
		di, dj := as[i].Due, as[j].Due
		if (di == "") != (dj == "") {
			return di != "" // dated first, empty last
		}
		if di != dj {
			return di < dj
		}
		return as[i].Text < as[j].Text
	})
}

func renderSection(sb *strings.Builder, heading string, as []actions.Action) {
	sb.WriteString("## ")
	sb.WriteString(heading)
	sb.WriteString("\n")
	if len(as) == 0 {
		sb.WriteString("_none_\n\n")
		return
	}
	sortByDueThenText(as)
	for _, a := range as {
		sb.WriteString(actionRefLine(a))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

func firstContext(a actions.Action) string {
	if len(a.Contexts) > 0 {
		return a.Contexts[0]
	}
	return "~" // no-context sorts last
}

// renderActionsSections renders the GTD block body (NO footer — the caller adds
// the timestamped footer so it never affects the change-gate hash). Returns the
// body and the count of OPEN actions (done/cancelled/archived excluded).
func renderActionsSections(rows []db.Action, today time.Time) (string, int) {
	tStr := today.Format("2006-01-02")
	weekEnd := today.AddDate(0, 0, 7).Format("2006-01-02")
	doneCut := today.AddDate(0, 0, -7).Format("2006-01-02")

	var overdue, todayA, thisWeek, waiting, someday, doneWeek []actions.Action
	nextByProject := map[string][]actions.Action{}
	var projOrder []string
	open := 0
	for _, r := range rows {
		if r.Archived {
			continue
		}
		a := actionFromRow(r)
		switch actions.Bucket(a, today) {
		case "cancelled":
			continue
		case "done":
			if r.DoneDate >= doneCut {
				doneWeek = append(doneWeek, a)
			}
			continue
		case "overdue":
			overdue = append(overdue, a)
		case "today":
			todayA = append(todayA, a)
		case "waiting":
			waiting = append(waiting, a)
		case "someday":
			someday = append(someday, a)
		default: // next | scheduled
			if a.Due != "" && a.Due > tStr && a.Due <= weekEnd {
				thisWeek = append(thisWeek, a)
			} else {
				proj := a.Project
				if proj == "" {
					proj = stripExt(a.SourcePath)
				}
				if _, seen := nextByProject[proj]; !seen {
					projOrder = append(projOrder, proj)
				}
				nextByProject[proj] = append(nextByProject[proj], a)
			}
		}
		open++
	}

	var sb strings.Builder
	renderSection(&sb, "🔴 Overdue", overdue)
	renderSection(&sb, "📅 Today", todayA)
	renderSection(&sb, "⏳ This week", thisWeek)

	// Next actions: grouped by project, then context, then text.
	sb.WriteString("## ▶ Next actions\n")
	if len(projOrder) == 0 {
		sb.WriteString("_none_\n\n")
	} else {
		sort.Strings(projOrder)
		for _, proj := range projOrder {
			group := nextByProject[proj]
			sort.SliceStable(group, func(i, j int) bool {
				ci, cj := firstContext(group[i]), firstContext(group[j])
				if ci != cj {
					return ci < cj
				}
				return group[i].Text < group[j].Text
			})
			sb.WriteString("**[[")
			sb.WriteString(proj)
			sb.WriteString("]]**\n")
			for _, a := range group {
				sb.WriteString(actionRefLine(a))
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	renderSection(&sb, "🕓 Waiting for", waiting)
	renderSection(&sb, "💭 Someday / Maybe", someday)

	// Done this week: an Obsidian collapsible callout (count + list).
	sb.WriteString("## ✅ Done this week\n")
	if len(doneWeek) == 0 {
		sb.WriteString("_none_\n")
	} else {
		sortByDueThenText(doneWeek)
		fmt.Fprintf(&sb, "> [!success]- %d completed this week\n", len(doneWeek))
		for _, a := range doneWeek {
			fmt.Fprintf(&sb, "> - %s — [[%s]]\n", a.Text, stripExt(a.SourcePath))
		}
	}
	return strings.TrimRight(sb.String(), "\n"), open
}

func actionsNoteStub() string {
	return "---\ntitle: \"Actions\"\ntype: actions\ntags: [actions]\n---\n\n" +
		"> AXON maintains your consolidated action list below inside the `axon:actions` block.\n" +
		"> These are references — tick tasks off in their source notes (linked), not here.\n" +
		"> Write your own notes above this line — AXON never overwrites them.\n\n"
}

// ActionsConsolidate renders the whole action index into 01-Projects/Actions.md.
// Zero-model, enabled by default. Change-gated on the rendered projection so a
// day with no visible change writes nothing.
type ActionsConsolidate struct{}

func (ActionsConsolidate) Name() string    { return "actions-consolidate" }
func (ActionsConsolidate) Essential() bool { return false }

// openTaskCounts reports open + overdue action counts from the derived index
// (FR-161). Best-effort: the essential heartbeat never fails on a DB hiccup.
func openTaskCounts(ctx context.Context, rc RunCtx) (open, overdue int) {
	rows, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})
	if err != nil {
		return 0, 0
	}
	today := rc.now()
	for _, r := range rows {
		open++
		if actions.Bucket(actionFromRow(r), today) == "overdue" {
			overdue++
		}
	}
	return open, overdue
}

// buildActionsBody reads the index and renders the block body (no footer).
func buildActionsBody(ctx context.Context, rc RunCtx) (string, int, error) {
	rows, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		return "", 0, err
	}
	body, total := renderActionsSections(rows, rc.now())
	return body, total, nil
}

func (a ActionsConsolidate) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	body, total, err := buildActionsBody(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	if total == 0 && !rc.Vault.Exists(actionsNotePath) {
		return Change{Changed: false, Reason: "no actions yet"}, nil
	}
	cursor := "actions:" + hashShort(body)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "actions unchanged"}, nil
	}
	return Change{Changed: true, Reason: fmt.Sprintf("%d open action(s)", total), Cursor: cursor}, nil
}

func (a ActionsConsolidate) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	body, total, err := buildActionsBody(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	if rc.DryRun {
		return RunResult{
			Summary: fmt.Sprintf("would consolidate %d open action(s) → %s", total, actionsNotePath),
			Changes: []string{actionsNotePath + ": axon:actions (dry-run)"},
		}, nil
	}
	footer := fmt.Sprintf("_generated %s UTC · %d open_", rc.now().UTC().Format("2006-01-02 15:04"), total)
	block := strings.TrimSpace(body + "\n\n" + footer)

	if !rc.Vault.Exists(actionsNotePath) {
		if _, cerr := rc.Vault.Create(actionsNotePath, actionsNoteStub()); cerr != nil {
			return RunResult{}, cerr
		}
	}
	if perr := rc.Vault.Patch(ctx, actionsNotePath, actionsBlock, block); perr != nil {
		return RunResult{}, perr
	}
	return RunResult{
		Summary: fmt.Sprintf("actions consolidated (%d open) → %s", total, actionsNotePath),
		Changes: []string{actionsNotePath + ": axon:actions updated"},
	}, nil
}
