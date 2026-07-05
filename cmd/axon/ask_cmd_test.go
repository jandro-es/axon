package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAskCLIRefusesEmptyVault: with nothing relevant ingested the
// deterministic gate refuses before any model call — no claude binary
// needed, exit code 0.
func TestAskCLIRefusesEmptyVault(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, err := run(t, "ask", "zzqx flumadiddle brontosaurus recipe", "--json", "--config", cfgPath)
	if err != nil {
		t.Fatalf("ask must exit 0 on a grounded refusal: %v\n%s", err, out)
	}
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no JSON in output:\n%s", out)
	}
	var a struct {
		Refused bool   `json:"refused"`
		Reason  string `json:"reason"`
	}
	if jerr := json.Unmarshal([]byte(out[i:]), &a); jerr != nil {
		t.Fatalf("bad JSON: %v\n%s", jerr, out)
	}
	if !a.Refused || a.Reason == "" {
		t.Fatalf("expected a refusal with a reason, got %+v\n%s", a, out)
	}
}
