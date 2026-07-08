package config

import (
	"reflect"
	"testing"
)

func TestResurfacingDefaults(t *testing.T) {
	var r ResurfacingConfig
	if got := r.IntervalsWeeksOr(); !reflect.DeepEqual(got, []int{1, 2, 4, 8, 16}) {
		t.Fatalf("default intervals = %v", got)
	}
	if got := r.ContradictionMaxChecksOr(); got != 3 {
		t.Fatalf("default max checks = %d, want 3", got)
	}
}

func TestResurfacingOverrides(t *testing.T) {
	r := ResurfacingConfig{IntervalsWeeks: []int{2, 6}, ContradictionMaxChecks: 1}
	if got := r.IntervalsWeeksOr(); !reflect.DeepEqual(got, []int{2, 6}) {
		t.Fatalf("intervals = %v", got)
	}
	if got := r.ContradictionMaxChecksOr(); got != 1 {
		t.Fatalf("max checks = %d", got)
	}
}
