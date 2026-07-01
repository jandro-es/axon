package automations

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestCatalogReflectsConfigAndPolicy(t *testing.T) {
	profile := config.Profile{
		Automations: map[string]config.Automation{
			"daily-log":    {Enabled: true, Schedule: "30 21 * * *", Model: "routine"},
			"compaction":   {Enabled: true, Schedule: "0 3 * * 0", Model: "synthesis"},
			"inbox-triage": {Enabled: false},
		},
		// Policy allows daily-log but NOT compaction, even though it's enabled.
		Policy: config.PolicyConfig{AllowedAutomations: []string{"daily-log", "heartbeat"}},
	}

	cat := Catalog(profile)
	if len(cat) != len(Registry(profile)) {
		t.Fatalf("catalog should list every automation; got %d of %d", len(cat), len(Registry(profile)))
	}

	byName := map[string]Info{}
	for _, info := range cat {
		byName[info.Name] = info
		if info.Purpose == "" || info.Purpose == "(no description)" {
			t.Errorf("automation %q has no purpose", info.Name)
		}
	}

	// daily-log: enabled in config AND allowed → effective enabled.
	if !byName["daily-log"].Enabled {
		t.Error("daily-log should be enabled (config + policy allow)")
	}
	// compaction: enabled in config but blocked by policy → not effective.
	if c := byName["compaction"]; c.Enabled || !c.ConfigEnabled || c.Allowed {
		t.Errorf("compaction should be config-enabled but policy-blocked; got %+v", c)
	}
	// inbox-triage: disabled in config.
	if byName["inbox-triage"].Enabled {
		t.Error("inbox-triage should be disabled")
	}
	// budget-guard is essential.
	if !byName["budget-guard"].Essential {
		t.Error("budget-guard should be essential")
	}
}
