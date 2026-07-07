package automations

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestRegistryHasAllStandardAutomations(t *testing.T) {
	want := []string{
		"budget-guard", "heartbeat", "knowledge-reindex", "context-export",
		"link-suggester", "daily-log", "inbox-triage", "compaction", "knowledge-digest",
		"memory-distill", "capture", "briefing", "resurfacer", "subscriptions", "session-distill",
		"research-questions", "entity-pages", "project-pulse", "eval-drift",
	}
	reg := Registry(config.Profile{})
	if len(reg) != len(want) {
		t.Errorf("registry has %d automations, want %d", len(reg), len(want))
	}
	for _, n := range want {
		if _, ok := reg[n]; !ok {
			t.Errorf("missing automation %q", n)
		}
	}
}

func TestAllowedByPolicy(t *testing.T) {
	tests := []struct {
		name    string
		allow   []string
		auto    string
		allowed bool
	}{
		{"empty allows all", nil, "compaction", true},
		{"wildcard allows all", []string{"*"}, "compaction", true},
		{"explicit allow", []string{"heartbeat", "daily-log"}, "daily-log", true},
		{"not in allow-list", []string{"heartbeat"}, "compaction", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := config.Profile{Policy: config.PolicyConfig{AllowedAutomations: tt.allow}}
			if got := AllowedByPolicy(p, tt.auto); got != tt.allowed {
				t.Errorf("AllowedByPolicy = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestSchedulablesRespectEnabledAndPolicy(t *testing.T) {
	p := config.Profile{
		Policy: config.PolicyConfig{AllowedAutomations: []string{"heartbeat", "daily-log"}},
		Automations: map[string]config.Automation{
			"heartbeat":  {Enabled: true, Schedule: "0 9 * * *"},
			"daily-log":  {Enabled: false, Schedule: "30 21 * * *"}, // disabled
			"compaction": {Enabled: true, Schedule: "0 3 * * 0"},    // not allowed by policy
		},
	}
	sch := Schedulables(p)
	if len(sch) != 1 || sch[0].Automation.Name() != "heartbeat" {
		t.Errorf("schedulables = %+v, want only heartbeat (enabled + allowed)", sch)
	}
}
