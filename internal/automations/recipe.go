package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

// Recipe caps are code, not config (ADR-039): recipes are deliberately
// narrow, and anything needing more is a Go automation.
const (
	recipeInputCap       = 32_000 // bytes per rendered input
	recipeMaxProposals   = 10     // review-sink proposals per run
	recipeSearchTopKDef  = 5
	recipeRecentDaysDef  = 7
	recipeRecentLimitDef = 20
)

// RecipeRun is the one generic Automation behind every config-defined recipe
// (FR-200, ADR-039). The engine treats it exactly like a built-in; recipes
// are never essential, so budget-guard may pause them.
type RecipeRun struct {
	def config.Recipe
}

func (r RecipeRun) Name() string    { return r.def.Name }
func (r RecipeRun) Essential() bool { return false }

// clipInput bounds one rendered input so even the zero-model render path
// cannot paste unbounded text into a block.
func clipInput(s string) string {
	if len(s) <= recipeInputCap {
		return s
	}
	return s[:recipeInputCap] + "\n…[truncated]"
}

// resolveInputs renders every reader to text. A missing note input is an
// idle reason (second return), not an error — a recipe targeting a
// not-yet-created note waits for free.
func (r RecipeRun) resolveInputs(ctx context.Context, rc RunCtx) (map[string]string, string, error) {
	vals := map[string]string{}
	for _, in := range r.def.Inputs {
		switch {
		case in.Note != nil:
			if !rc.Vault.Exists(in.Note.Path) {
				return nil, "input note absent: " + in.Note.Path, nil
			}
			n, err := rc.Vault.Read(ctx, in.Note.Path)
			if err != nil {
				return nil, "", err
			}
			vals[in.Name] = clipInput(n.Body)
		case in.Search != nil:
			topK := in.Search.TopK
			if topK <= 0 {
				topK = recipeSearchTopKDef
			}
			hits, err := rc.Searcher.Search(ctx, in.Search.Query, topK)
			if err != nil {
				return nil, "", err
			}
			var b strings.Builder
			for _, h := range hits {
				fmt.Fprintf(&b, "[[%s]]: %s\n", stripExt(h.Path), h.Snippet)
			}
			vals[in.Name] = clipInput(strings.TrimSpace(b.String()))
		case in.RecentNotes != nil:
			days := in.RecentNotes.LookbackDays
			if days <= 0 {
				days = recipeRecentDaysDef
			}
			limit := in.RecentNotes.Limit
			if limit <= 0 {
				limit = recipeRecentLimitDef
			}
			since := rc.now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
			stamps, err := db.NotesUpdatedSince(ctx, rc.DB, since, limit)
			if err != nil {
				return nil, "", err
			}
			var b strings.Builder
			for _, s := range stamps {
				fmt.Fprintf(&b, "[[%s]] (updated %s)\n", stripExt(s.Path), s.Updated)
			}
			vals[in.Name] = clipInput(strings.TrimSpace(b.String()))
		}
	}
	return vals, "", nil
}

// substitute performs the plain placeholder substitution — no template
// logic, by design (ADR-039).
func (r RecipeRun) substitute(tmpl string, vals map[string]string, rc RunCtx) string {
	pairs := []string{"{{today}}", today(rc)}
	for name, v := range vals {
		pairs = append(pairs, "{{"+name+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// canonicalInputs is the deterministic form hashed by the automatic
// change-gate: sorted name/value pairs.
func canonicalInputs(vals map[string]string) string {
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(0)
		b.WriteString(vals[n])
		b.WriteByte('\n')
	}
	return b.String()
}
