package config

import "testing"

func TestStaleAfterDaysOr(t *testing.T) {
	if (ActionsConfig{}).StaleAfterDaysOr() != 30 {
		t.Error("default stale_after_days must be 30")
	}
	if (ActionsConfig{StaleAfterDays: 14}).StaleAfterDaysOr() != 14 {
		t.Error("override not honored")
	}
}
