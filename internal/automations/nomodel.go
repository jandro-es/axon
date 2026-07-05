package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
)

// ---- budget-guard (essential, no model) ------------------------------------

// BudgetGuard surfaces budget pressure and is the lever the engine consults to
// pause non-essential automations (FR-43). It is essential (never paused) and
// makes no model call.
type BudgetGuard struct{}

func (BudgetGuard) Name() string    { return "budget-guard" }
func (BudgetGuard) Essential() bool { return true }

func (BudgetGuard) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	// Always run: it is a frequent, cheap watchdog.
	return Change{Changed: true, Reason: "budget watch"}, nil
}

func (BudgetGuard) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	st, err := rc.Manager.Status(ctx, rc.Profile)
	if err != nil {
		return RunResult{}, err
	}
	if st.GuardPaused {
		return RunResult{
			Summary: fmt.Sprintf("guard ACTIVE: day %.0f%%, week %.0f%% (≥ %d%%) — non-essential automations paused",
				st.Day.Pct, st.Week.Pct, st.GuardPct),
			Changes: []string{"paused non-essential automations for the rest of the window"},
		}, nil
	}
	return RunResult{Summary: fmt.Sprintf("budget ok: day %.0f%%, week %.0f%%", st.Day.Pct, st.Week.Pct)}, nil
}

// ---- knowledge-reindex (no model) ------------------------------------------

// KnowledgeReindex keeps the derived DB consistent with the vault (ADR-006):
// rebuild the notes mirror + link graph and re-embed pending chunks. Gated on a
// vault content change; no Claude call.
type KnowledgeReindex struct{}

func (KnowledgeReindex) Name() string    { return "knowledge-reindex" }
func (KnowledgeReindex) Essential() bool { return false }

func (KnowledgeReindex) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	fp, err := vaultFingerprint(ctx, rc.Vault, "")
	if err != nil {
		return Change{}, err
	}
	if fp == rc.LastCursor {
		return Change{Changed: false, Reason: "vault unchanged since last index"}, nil
	}
	return Change{Changed: true, Reason: "vault content changed", Cursor: fp}, nil
}

func (KnowledgeReindex) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	if rc.DryRun {
		return RunResult{
			Summary: "would rebuild notes/links and re-embed pending chunks",
			Changes: []string{"reindex (dry-run): no DB writes performed"},
		}, nil
	}
	res, err := core.Reindex(ctx, rc.Vault, rc.DB)
	if err != nil {
		return RunResult{}, err
	}
	changes := []string{fmt.Sprintf("reindex: %d notes, %d links, %d unresolved", res.Notes, res.Links, res.BrokenWikilink)}
	if rc.Embedder != nil {
		if re, eerr := core.ReembedPending(ctx, rc.DB, rc.Embedder, false); eerr == nil && re.Embedded > 0 {
			changes = append(changes, fmt.Sprintf("re-embedded %d pending chunks", re.Embedded))
		}
	}
	// Best-effort ANN index maintenance (ADR-025): never fail the reindex over it.
	_ = core.RefreshVectorIndex(ctx, rc.DB, rc.Config.Retrieval)
	return RunResult{Summary: fmt.Sprintf("indexed %d notes, %d links", res.Notes, res.Links), Changes: changes}, nil
}

// ---- context-export (no model) ---------------------------------------------

// ContextExport assembles a portable snapshot bundle under .axon/exports/<ts>/
// (manifest + core-context Markdown). Pure data assembly, zero Claude cost.
type ContextExport struct{}

func (ContextExport) Name() string    { return "context-export" }
func (ContextExport) Essential() bool { return false }

func (ContextExport) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	fp, err := vaultFingerprint(ctx, rc.Vault, "")
	if err != nil {
		return Change{}, err
	}
	if fp == rc.LastCursor {
		return Change{Changed: false, Reason: "no vault change since last export"}, nil
	}
	return Change{Changed: true, Reason: "vault changed", Cursor: fp}, nil
}

func (ContextExport) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	stamp := rc.now().UTC().Format("20060102-150405")
	dir := ".axon/exports/" + stamp
	paths, err := rc.Vault.List(ctx)
	if err != nil {
		return RunResult{}, err
	}
	var projects, mocs int
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, "01-Projects/"):
			projects++
		case strings.HasPrefix(p, "MOCs/"):
			mocs++
		}
	}
	manifest := map[string]any{
		"created":  rc.now().UTC().Format(time.RFC3339),
		"profile":  rc.Profile,
		"notes":    len(paths),
		"projects": projects,
		"mocs":     mocs,
	}
	if rc.DryRun {
		return RunResult{
			Summary: fmt.Sprintf("would export snapshot to %s (%d notes)", dir, len(paths)),
			Changes: []string{dir + "/manifest.json", dir + "/core-context.md"},
		}, nil
	}
	mjson, _ := json.MarshalIndent(manifest, "", "  ")
	if _, err := rc.Vault.Create(dir+"/manifest.json", string(mjson)); err != nil {
		return RunResult{}, err
	}
	coreMD := fmt.Sprintf("# Core context — %s\n\nProfile: %s\nNotes: %d, Projects: %d, MOCs: %d\n",
		stamp, rc.Profile, len(paths), projects, mocs)
	if _, err := rc.Vault.Create(dir+"/core-context.md", coreMD); err != nil {
		return RunResult{}, err
	}
	return RunResult{
		Summary: fmt.Sprintf("exported snapshot to %s", dir),
		Changes: []string{dir + "/manifest.json", dir + "/core-context.md"},
	}, nil
}

// ---- link-suggester (no model for candidate generation) --------------------

// linkSuggesterProposedState is the automation_state row remembering every
// pair the link-suggester has ever queued (FR-102).
const linkSuggesterProposedState = "link-suggester:proposed"

// LinkSuggester proposes Zettelkasten links between semantically close notes
// that aren't yet linked, via a vector-similarity sweep (no model needed). It
// writes ranked suggestions to .axon/review-queue.md.
type LinkSuggester struct {
	MaxSuggestions int
}

func (LinkSuggester) Name() string    { return "link-suggester" }
func (LinkSuggester) Essential() bool { return false }

func (LinkSuggester) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	n, err := db.CountVectors(ctx, rc.DB)
	if err != nil {
		return Change{}, err
	}
	cursor := fmt.Sprintf("vectors:%d", n)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no new embeddings to compare"}, nil
	}
	if n == 0 {
		return Change{Changed: false, Reason: "no embeddings yet"}, nil
	}
	return Change{Changed: true, Reason: "embeddings changed", Cursor: cursor}, nil
}

func (l LinkSuggester) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	max := l.MaxSuggestions
	if max <= 0 {
		max = 10
	}
	paths, err := rc.Vault.List(ctx)
	if err != nil {
		return RunResult{}, err
	}
	sort.Strings(paths)

	// Proposal memory (FR-102): pairs already queued once — accepted or
	// dismissed — are never re-proposed. Unordered: direction is noise.
	proposed := loadProposalMemory(ctx, rc, linkSuggesterProposedState)

	type suggestion struct{ from, to string }
	var suggestions []suggestion
	seen := map[string]bool{}

	for _, p := range paths {
		if len(suggestions) >= max {
			break
		}
		n, err := rc.Vault.Read(ctx, p)
		if err != nil || strings.TrimSpace(n.Body) == "" {
			continue
		}
		hits, err := rc.Searcher.Search(ctx, firstWords(n.Body, 40), 5)
		if err != nil {
			continue
		}
		existing := linkTargets(n.Body)
		for _, h := range hits {
			if h.Path == "" || h.Path == p {
				continue
			}
			key := pairKey(p, h.Path)
			if seen[key] || proposed[key] || existing[stripExt(h.Path)] || existing[base(h.Path)] {
				continue
			}
			seen[key] = true
			suggestions = append(suggestions, suggestion{p, h.Path})
			if len(suggestions) >= max {
				break
			}
		}
	}

	if len(suggestions) == 0 {
		return RunResult{Summary: "no new link suggestions"}, nil
	}
	changes := make([]string, len(suggestions))
	for i, s := range suggestions {
		changes[i] = fmt.Sprintf("[[%s]] ↔ [[%s]]", stripExt(s.from), stripExt(s.to))
	}
	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would propose %d link(s)", len(suggestions)), Changes: changes}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n## Link suggestions (%s)\n", rc.now().UTC().Format("2006-01-02 15:04"))
	for _, c := range changes {
		fmt.Fprintf(&b, "- [ ] %s\n", c)
	}
	if err := rc.Vault.Append(".axon/review-queue.md", b.String()); err != nil {
		return RunResult{}, err
	}
	for _, s := range suggestions {
		proposed[pairKey(s.from, s.to)] = true
	}
	saveProposalMemory(ctx, rc, linkSuggesterProposedState, proposed)
	return RunResult{Summary: fmt.Sprintf("proposed %d link(s) in review queue", len(suggestions)), Changes: changes}, nil
}
