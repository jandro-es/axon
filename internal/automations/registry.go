package automations

import (
	"fmt"
	"sort"

	"github.com/jandro-es/axon/internal/config"
)

// Registry returns all standard automations keyed by name. Per-automation config
// (thresholds etc.) is applied where relevant.
func Registry(profile config.Profile) map[string]Automation {
	reg := map[string]Automation{
		BudgetGuard{}.Name():       BudgetGuard{},
		Heartbeat{}.Name():         Heartbeat{},
		KnowledgeReindex{}.Name():  KnowledgeReindex{},
		ContextExport{}.Name():     ContextExport{},
		LinkSuggester{}.Name():     LinkSuggester{},
		DailyLog{}.Name():          DailyLog{},
		InboxTriage{}.Name():       InboxTriage{},
		Compaction{}.Name():        Compaction{},
		KnowledgeDigest{}.Name():   KnowledgeDigest{},
		MemoryDistill{}.Name():     MemoryDistill{},
		Capture{}.Name():           Capture{},
		Briefing{}.Name():          Briefing{},
		Resurfacer{}.Name():        Resurfacer{},
		Subscriptions{}.Name():     Subscriptions{},
		SessionDistill{}.Name():    SessionDistill{},
		ResearchQuestions{}.Name(): ResearchQuestions{},
		EntityPages{}.Name():       EntityPages{},
		ProjectPulse{}.Name():      ProjectPulse{},
		EvalDrift{}.Name():         EvalDrift{},
	}
	return reg
}

// Names returns the sorted list of all automation names.
func Names(profile config.Profile) []string {
	reg := Registry(profile)
	names := make([]string, 0, len(reg))
	for n := range reg {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Get returns the named automation, or ErrUnknownAutomation.
func Get(profile config.Profile, name string) (Automation, error) {
	a, ok := Registry(profile)[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAutomation, name)
	}
	return a, nil
}

// AllowedByPolicy reports whether an automation is permitted by
// policy.allowed_automations. "*" (or an empty/unset list) permits all; a
// non-empty list is a strict allow-list (FR-30, work profile constraint).
func AllowedByPolicy(profile config.Profile, name string) bool {
	allow := profile.Policy.AllowedAutomations
	if len(allow) == 0 {
		return true
	}
	for _, a := range allow {
		if a == "*" || a == name {
			return true
		}
	}
	return false
}

// Schedulable is an automation that is enabled in config AND allowed by policy.
type Schedulable struct {
	Automation Automation
	Schedule   string
	CatchUp    string
}

// Schedulable returns the automations the scheduler should register: enabled in
// config, present in the registry, and permitted by policy.
func Schedulables(profile config.Profile) []Schedulable {
	var out []Schedulable
	reg := Registry(profile)
	for name, cfg := range profile.Automations {
		if !cfg.Enabled || !AllowedByPolicy(profile, name) {
			continue
		}
		a, ok := reg[name]
		if !ok {
			continue
		}
		out = append(out, Schedulable{Automation: a, Schedule: cfg.Schedule, CatchUp: cfg.CatchUp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Automation.Name() < out[j].Automation.Name() })
	return out
}
