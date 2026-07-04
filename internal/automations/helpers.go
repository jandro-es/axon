package automations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// inboxItems returns the vault-relative paths of capture items in 00-Inbox,
// excluding the folder README.
func inboxItems(ctx context.Context, rc RunCtx) []string {
	paths, err := rc.Vault.List(ctx)
	if err != nil {
		return nil
	}
	var items []string
	for _, p := range paths {
		if !strings.HasPrefix(p, "00-Inbox/") {
			continue
		}
		if strings.EqualFold(base(p), "README") {
			continue
		}
		items = append(items, p)
	}
	return items
}

// countInbox counts inbox capture items.
func countInbox(ctx context.Context, rc RunCtx) int {
	return len(inboxItems(ctx, rc))
}

// guardSuffix annotates a status line when budget-guard is active.
func guardSuffix(st tokens.BudgetStatus) string {
	if st.GuardPaused {
		return " ⚠ guard active"
	}
	return ""
}

// dailyStub is the minimal daily note created when an automation needs today's
// note and the user hasn't made one yet.
func dailyStub(date string) string {
	return "---\ntitle: \"" + date + "\"\ntype: daily\ntags: [daily]\n---\n\n## Log\n\n"
}

// hashShort returns a short stable hash of s, for compact change-gate cursors.
func hashShort(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// firstWords returns the first n whitespace-separated words of s, used to form a
// representative search query from a note body.
func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

// linkTargets returns the set of wikilink targets already present in a body,
// keyed by both their path form and basename form, so a suggester can avoid
// re-proposing an existing link.
func linkTargets(body string) map[string]bool {
	out := map[string]bool{}
	for _, l := range vault.ParseLinks(body) {
		if l.Kind == vault.KindTag {
			continue
		}
		key, _ := vault.TargetKey(l.Target)
		out[key] = true
	}
	return out
}

// stripExt is the vault-relative path without its ".md" extension.
func stripExt(p string) string { return vault.RelNoExt(p) }

// base is the basename of a vault path without ".md".
func base(p string) string { return vault.BaseNoExt(p) }

// ---- proposal memory (shared by resurfacer + link-suggester, FR-90/FR-102) --

// proposalMemoryCap bounds each automation's persistent proposal memory.
const proposalMemoryCap = 500

// loadProposalMemory reads an automation's proposed-pair memory from its
// automation_state row (empty on any problem — worst case a pair is
// proposed twice).
func loadProposalMemory(ctx context.Context, rc RunCtx, stateKey string) map[string]bool {
	out := map[string]bool{}
	raw, err := db.GetCursor(ctx, rc.DB, stateKey)
	if err != nil || raw == "" {
		return out
	}
	var keys []string
	_ = json.Unmarshal([]byte(raw), &keys)
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// saveProposalMemory persists proposal memory beside the engine cursor,
// capped at the newest entries.
func saveProposalMemory(ctx context.Context, rc RunCtx, stateKey string, proposed map[string]bool) {
	keys := make([]string, 0, len(proposed))
	for k := range proposed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > proposalMemoryCap {
		keys = keys[len(keys)-proposalMemoryCap:]
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, stateKey, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("proposal memory: persist", "key", stateKey, "err", err)
	}
}
