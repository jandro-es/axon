package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestActionExtractCheck(t *testing.T) {
	off := actionExtractCheck(config.Profile{})
	if off.Status != StatusOK || off.Name != "action-extract" {
		t.Fatalf("off check = %+v", off)
	}
	on := actionExtractCheck(config.Profile{
		Automations: map[string]config.Automation{"action-extract": {Enabled: true}},
	})
	if !strings.Contains(on.Detail, "routine") {
		t.Errorf("enabled detail should mention the tier: %q", on.Detail)
	}
}
