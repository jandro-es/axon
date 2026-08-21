package automations

import (
	"context"
	"fmt"
	"strings"

	"github.com/jandro-es/axon/internal/db"
)

// Caps are code, not config (the recipe-caps precedent).
const (
	orphanDormantDays = 180
	orphanListMax     = 50
	orphanReportNote  = "03-Resources/Vault Health.md"
	orphanReportBlock = "orphans"
)

// OrphanReport renders the vault's disconnected and dormant notes into a
// managed block (FR-204). Zero model calls and zero proposals: it makes the
// graph's holes visible in the vault itself, where a human — or a recipe
// reading this note — can act on them. Dormant notes are reported but never
// proposed; proactive's resurfacer owns those on its own ladder.
type OrphanReport struct{}

func (OrphanReport) Name() string    { return "orphan-report" }
func (OrphanReport) Essential() bool { return false }

// render builds the report body. Both sections query one past the cap so
// truncation is detectable and never reads as a complete list.
func (OrphanReport) render(ctx context.Context, rc RunCtx) (string, int, int, error) {
	// The report links to every note it lists, so its own edges must be
	// invisible here — otherwise the report rescues its own subjects from
	// orphanhood and oscillates between full and empty on alternate runs.
	orphans, err := db.OrphanNotes(ctx, rc.DB, orphanListMax+1, orphanReportNote)
	if err != nil {
		return "", 0, 0, err
	}
	cutoff := rc.now().UTC().AddDate(0, 0, -orphanDormantDays).Format("2006-01-02")
	dormant, err := db.NotesUpdatedBefore(ctx, rc.DB, cutoff, orphanListMax+1)
	if err != nil {
		return "", 0, 0, err
	}

	var b strings.Builder
	section := func(title, blurb string, rows []db.NoteStamp) {
		shown := rows
		extra := 0
		if len(shown) > orphanListMax {
			extra = len(shown) - orphanListMax
			shown = shown[:orphanListMax]
		}
		fmt.Fprintf(&b, "## %s (%d)\n%s\n\n", title, len(rows), blurb)
		for _, n := range shown {
			fmt.Fprintf(&b, "- [[%s]] (updated %s)\n", stripExt(n.Path), n.Updated)
		}
		if len(shown) == 0 {
			b.WriteString("- none\n")
		}
		if extra > 0 {
			fmt.Fprintf(&b, "- …and %d more\n", extra)
		}
		b.WriteString("\n")
	}
	section("Orphans", "Notes with no links in or out — nothing points here, and this points nowhere.", orphans)
	section("Dormant", fmt.Sprintf("Not edited in %d days. The resurfacer proposes these on its own ladder; this list is for orientation.", orphanDormantDays), dormant)
	return strings.TrimSpace(b.String()), len(orphans), len(dormant), nil
}

// DetectChange hashes the rendered body, so a run where neither set moved
// skips with no work (the RecipeRun change-gate pattern).
func (o OrphanReport) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	body, orphans, dormant, err := o.render(ctx, rc)
	if err != nil {
		return Change{}, err
	}
	cursor := "orphans:" + hashShort(body)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no change in orphans or dormant notes"}, nil
	}
	return Change{
		Changed: true,
		Reason:  fmt.Sprintf("%d orphan(s), %d dormant", orphans, dormant),
		Cursor:  cursor,
	}, nil
}

func (o OrphanReport) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	body, orphans, dormant, err := o.render(ctx, rc)
	if err != nil {
		return RunResult{}, err
	}
	summary := fmt.Sprintf("%d orphan(s), %d dormant → %s", orphans, dormant, orphanReportNote)
	if rc.DryRun {
		return RunResult{Summary: "would write " + summary, Changes: []string{orphanReportNote}}, nil
	}
	if !rc.Vault.Exists(orphanReportNote) {
		stub := "# " + base(orphanReportNote) + "\n\nMaintained by the \"orphan-report\" automation. " +
			"The section below is rewritten on every run; anything outside it is yours.\n"
		if _, cerr := rc.Vault.Create(orphanReportNote, stub); cerr != nil {
			return RunResult{}, cerr
		}
	}
	if perr := rc.Vault.Patch(ctx, orphanReportNote, orphanReportBlock, body); perr != nil {
		return RunResult{}, perr
	}
	return RunResult{Summary: "wrote " + summary, Changes: []string{orphanReportNote}}, nil
}
