package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestActionsReviewCheck(t *testing.T) {
	off := actionsReviewCheck(config.Profile{})
	if off.Status != StatusOK || off.Name != "actions-review" {
		t.Fatalf("off check = %+v", off)
	}
	on := actionsReviewCheck(config.Profile{
		Automations: map[string]config.Automation{"actions-review": {Enabled: true}},
		Actions:     config.ActionsConfig{StaleAfterDays: 21},
	})
	if !strings.Contains(on.Detail, "21") {
		t.Errorf("enabled detail should mention the threshold: %q", on.Detail)
	}
}
