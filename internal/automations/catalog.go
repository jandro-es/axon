package automations

import (
	"sort"

	"github.com/jandro-es/axon/internal/config"
)

// purposes gives each built-in automation a short, human-facing description of
// what it does and when. Kept beside the registry so the copy lives in one place
// and `axon automations` can explain the system without the user reading code.
var purposes = map[string]string{
	"budget-guard":        "Watches token-budget pressure and pauses non-essential automations when usage crosses the guard threshold.",
	"heartbeat":           "Periodic situational awareness: inbox backlog, budget headroom and guard state. No model call.",
	"knowledge-reindex":   "Rebuilds the derived DB (notes mirror, link graph, embeddings) from the vault when it changes. No model call.",
	"context-export":      "Writes a portable snapshot bundle (manifest + core context) under .axon/exports/. No model call.",
	"link-suggester":      "Proposes Zettelkasten links from vector similarity and queues them for review. No model call.",
	"daily-log":           "Synthesises the day's activity into today's daily-note summary block.",
	"inbox-triage":        "Classifies new Inbox items and proposes a triage into the review queue.",
	"compaction":          "Distills oversized notes into summary blocks, preserving the original prose.",
	"knowledge-digest":    "Weekly synthesis of newly ingested sources: surfaces connections and proposes MOC additions.",
	"memory-distill":      "Maintains the durable personal-memory note: distils new entries and compacts old ones.",
	"capture":             "Ingests own-line URLs from Inbox notes and files dropped into 00-Inbox, archiving originals. The FR-26 capture funnel; no model call (enrichment optional via capture.enrich).",
	"briefing":            "Writes the morning axon:briefing block into the daily note: what changed, review queue, budget — plus a short routine-tier narrative. Facts are free; the narrative degrades on budget pressure.",
	"resurfacer":          "Weekly spaced-repetition resurfacing (R9): schedules recent↔dormant connections into the review queue at lengthening intervals; opt-in routine-tier contradiction detection when budget_tokens > 0.",
	"subscriptions":       "Polls configured RSS/Atom feeds hourly and ingests new items through the pipeline (subscribe-from-now, per-tick caps). Enrichment optional via subscriptions.enrich.",
	"session-distill":     "Distills finished vault sessions into durable MEMORY entries (decisions, lessons, preferences) — one classify-tier call per session, once ever. Gated by memory.capture_sessions.",
	"research-questions":  "Weekly: answers standing questions in 03-Resources/Research Questions.md from the vault, grounded, into an axon:answers block. Disabled by default.",
	"entity-pages":        "Extracts named people and projects from new notes into auto-maintained Entities/ index pages with wikilink-safe mention lists. Disabled by default.",
	"project-pulse":       "Weekly: reads 01-Projects + USER goals into an axon:pulse block (progress, stalls, next actions) and nudges stale projects to the review queue. Narrative degrades to facts-only under budget. Disabled by default.",
	"eval-drift":          "On a schedule: when a gated local model's version (Ollama digest) changes, re-runs `axon eval` for that tier and refreshes eval_runs so promotion stays evidence-based (FR-143). No digest change → no work. Disabled by default.",
	"merge-proposals":     "Weekly near-duplicate sweep (R7): proposes note merges to the review queue by mean-vector cosine (zero-model). Accepting merges wikilink-safely — survivor keeps prose + gains the loser's content, inbound links retarget, the loser is archived to .trash/, never deleted. Disabled by default.",
	"actions-consolidate": "Daily (zero-model, T2): renders every checkbox task across the vault into a GTD-ordered axon:actions block in 01-Projects/Actions.md (Overdue/Today/This week/Next/Waiting/Someday/Done-this-week) as [[source]] references — never duplicate checkboxes. Change-gated on the rendered projection. Enabled by default.",
}

// Purpose returns the human description for an automation, or a generic fallback.
func Purpose(name string) string {
	if p, ok := purposes[name]; ok {
		return p
	}
	return "(no description)"
}

// Info is the static + config-derived metadata for one automation, as surfaced
// by `axon automations`.
type Info struct {
	Name          string `json:"name"`
	Purpose       string `json:"purpose"`
	Essential     bool   `json:"essential"`
	Enabled       bool   `json:"enabled"`        // effective: config-enabled AND policy-allowed
	ConfigEnabled bool   `json:"config_enabled"` // raw enabled flag in config
	Allowed       bool   `json:"allowed"`        // permitted by policy.allowed_automations
	Schedule      string `json:"schedule,omitempty"`
	Model         string `json:"model,omitempty"` // configured model tier, or "none"
}

// Catalog returns metadata for every built-in automation (sorted by name),
// combining the registry with this profile's config and policy. It lists ALL
// automations — enabled or not — so the user sees the full menu and its state.
func Catalog(profile config.Profile) []Info {
	reg := Registry(profile)
	out := make([]Info, 0, len(reg))
	for name, a := range reg {
		cfg, hasCfg := profile.Automations[name]
		allowed := AllowedByPolicy(profile, name)
		info := Info{
			Name:          name,
			Purpose:       Purpose(name),
			Essential:     a.Essential(),
			ConfigEnabled: hasCfg && cfg.Enabled,
			Allowed:       allowed,
			Enabled:       hasCfg && cfg.Enabled && allowed,
			Schedule:      cfg.Schedule,
			Model:         cfg.Model,
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
