package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestResurfaceCheckContradictionOff(t *testing.T) {
	p := config.Profile{
		Automations: map[string]config.Automation{"resurfacer": {Enabled: true, BudgetTokens: 0}},
	}
	c := resurfaceCheck(p)
	if c.Status != StatusOK || !strings.Contains(c.Detail, "contradiction path off") {
		t.Fatalf("got %+v", c)
	}
}

func TestResurfaceCheckContradictionActive(t *testing.T) {
	p := config.Profile{
		Automations: map[string]config.Automation{"resurfacer": {Enabled: true, BudgetTokens: 4000}},
		Resurfacing: config.ResurfacingConfig{ContradictionMaxChecks: 2},
	}
	c := resurfaceCheck(p)
	if c.Status != StatusOK || !strings.Contains(c.Detail, "contradiction path active") {
		t.Fatalf("got %+v", c)
	}
}
