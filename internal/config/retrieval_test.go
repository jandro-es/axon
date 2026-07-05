package config

import "testing"

func TestIndexModeDefaultsBrute(t *testing.T) {
	var r RetrievalConfig
	if got := r.IndexMode(); got != "brute" {
		t.Fatalf("IndexMode() = %q, want brute", got)
	}
	r.Index = "ann"
	if got := r.IndexMode(); got != "ann" {
		t.Fatalf("IndexMode() = %q, want ann", got)
	}
}

func TestANNDefaults(t *testing.T) {
	var a ANNConfig
	if got := a.ThresholdOr(); got != 10000 {
		t.Fatalf("ThresholdOr() = %d, want 10000", got)
	}
	if got := a.NProbeOr(); got != 8 {
		t.Fatalf("NProbeOr() = %d, want 8", got)
	}
	a = ANNConfig{Threshold: 500, NProbe: 3}
	if a.ThresholdOr() != 500 || a.NProbeOr() != 3 {
		t.Fatalf("explicit values not honoured: %+v", a)
	}
}
