package config

import "testing"

func TestMergeConfigDefaults(t *testing.T) {
	var m MergeConfig // zero value
	if got := m.ThresholdOr(); got != 0.92 {
		t.Fatalf("ThresholdOr default = %v, want 0.92", got)
	}
	if got := m.MaxProposalsOr(); got != 5 {
		t.Fatalf("MaxProposalsOr default = %v, want 5", got)
	}
	m = MergeConfig{Threshold: 0.8, MaxProposals: 3}
	if m.ThresholdOr() != 0.8 || m.MaxProposalsOr() != 3 {
		t.Fatalf("explicit values not returned: %+v", m)
	}
}
