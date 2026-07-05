package config

import "testing"

func TestResearchQuestionsConfigDisabledByDefault(t *testing.T) {
	cfg, err := Load(exampleConfigPath(t))
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	rq, ok := cfg.Profiles["personal"].Automations["research-questions"]
	if !ok {
		t.Fatal("research-questions missing from example personal profile")
	}
	if rq.Enabled {
		t.Fatal("research-questions must default disabled")
	}
	if rq.Schedule == "" {
		t.Fatal("research-questions needs a schedule")
	}
}
