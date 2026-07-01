package main

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestIngestAuthRules(t *testing.T) {
	configured := []config.IngestAuth{{Domain: "confluence.corp.example", Value: "env:CONF_TOKEN"}}

	// No flags: configured rules pass through untouched.
	rules, err := ingestAuthRules(configured, nil, "https://confluence.corp.example/x")
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules = %+v, err %v", rules, err)
	}

	// A --header flag is scoped to the target URL's own host.
	rules, err = ingestAuthRules(configured, []string{"Authorization: Bearer tok123"}, "https://wiki.example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[1].Domain != "wiki.example.com" ||
		rules[1].Header != "Authorization" || rules[1].Value != "Bearer tok123" {
		t.Errorf("flag rule = %+v, want host-scoped Authorization", rules[1])
	}

	// Malformed header and non-URL targets error clearly.
	if _, err := ingestAuthRules(nil, []string{"no-colon-here"}, "https://a.example.com"); err == nil {
		t.Error("malformed --header accepted")
	}
	if _, err := ingestAuthRules(nil, []string{"X: y"}, "notes/local.md"); err == nil {
		t.Error("--header with a non-URL target accepted")
	}
}
