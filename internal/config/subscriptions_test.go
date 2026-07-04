package config

import "testing"

func TestSubscriptionsDefaults(t *testing.T) {
	var c SubscriptionsConfig
	if c.EnrichMode() != "heuristic" || c.PerTick() != 5 {
		t.Fatalf("defaults = %q/%d, want heuristic/5", c.EnrichMode(), c.PerTick())
	}
	c = SubscriptionsConfig{Enrich: "claude", MaxPerTick: 2}
	if c.EnrichMode() != "claude" || c.PerTick() != 2 {
		t.Fatalf("overrides not honored: %+v", c)
	}
}

func TestValidateSubscriptions(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SubscriptionsConfig
		wantErr bool
	}{
		{"zero ok", SubscriptionsConfig{}, false},
		{"good feed", SubscriptionsConfig{Feeds: []Feed{{URL: "https://example.com/feed.xml"}}}, false},
		{"bad scheme", SubscriptionsConfig{Feeds: []Feed{{URL: "ftp://x/feed"}}}, true},
		{"empty url", SubscriptionsConfig{Feeds: []Feed{{URL: ""}}}, true},
		{"bad enrich", SubscriptionsConfig{Enrich: "gpt"}, true},
		{"negative cap", SubscriptionsConfig{MaxPerTick: -1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateSubscriptions(tt.cfg); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
