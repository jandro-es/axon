package mcp

import (
	"slices"
	"testing"
)

func TestRegisteredToolNamesFilter(t *testing.T) {
	all := registeredToolNames(nil)
	if len(all) != 18 {
		t.Fatalf("all tools = %d (%v), want 18", len(all), all)
	}
	got := registeredToolNames([]string{"vault_read", "tokens_status", "nonexistent"})
	want := []string{"tokens_status", "vault_read"} // sorted; unknown names dropped
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}
}
