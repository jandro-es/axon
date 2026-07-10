package automations

import "testing"

// TestActionToolsAgenticDisposition pins ADR-034's guarantee: actions_list is an
// agentic READ tool; action_complete is in NEITHER agentic map (interactive-only).
func TestActionToolsAgenticDisposition(t *testing.T) {
	if !agenticReadTools["actions_list"] {
		t.Error("actions_list must be an agentic READ tool")
	}
	if agenticReadTools["action_complete"] || agenticWriteTools["action_complete"] {
		t.Error("action_complete must be excluded from BOTH agentic maps (ADR-034)")
	}
}
