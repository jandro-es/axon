package automations

import (
	"os"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

// Schedulables only iterates automations present in the profile config, so a
// registered automation with no seed can never run on a default install (the
// eval-drift bug, docs/ISSUES.md #1). Pin the invariant: every automation in
// the registry has an entry in the starter config and in the example config.
func TestEveryRegisteredAutomationIsSeeded(t *testing.T) {
	sources := map[string]config.Profile{}

	raw, err := config.Starter("personal", "~/Notes/Vault", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, starter, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	sources["starter"] = starter

	exampleRaw, err := os.ReadFile("../../axon.config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	exampleCfg, err := config.Parse(exampleRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, example, err := exampleCfg.ResolveProfile("personal")
	if err != nil {
		t.Fatal(err)
	}
	sources["example (personal)"] = example

	for src, profile := range sources {
		for name := range Registry(profile) {
			if _, ok := profile.Automations[name]; !ok {
				t.Errorf("%s config has no %q entry — Schedulables() can never run it", src, name)
			}
		}
	}
}
