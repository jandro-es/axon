package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestMergeCheck(t *testing.T) {
	off := mergeCheck(config.Profile{})
	if off.Status != StatusOK || !strings.Contains(off.Detail, "off") {
		t.Fatalf("disabled: %+v", off)
	}
	on := mergeCheck(config.Profile{
		Automations: map[string]config.Automation{"merge-proposals": {Enabled: true}},
	})
	if on.Status != StatusOK || !strings.Contains(on.Detail, "0.92") {
		t.Fatalf("enabled: %+v", on)
	}
}
