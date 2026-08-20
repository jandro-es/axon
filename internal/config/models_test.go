package config

import "testing"

func TestParseModelRef(t *testing.T) {
	tests := []struct {
		in       string
		provider string
		model    string
	}{
		{"claude-haiku-4-5", ProviderClaude, "claude-haiku-4-5"},
		{"ollama:qwen3:8b", ProviderOllama, "qwen3:8b"},
		{"apple", ProviderApple, AppleFoundationModel},
		{"apple:system", ProviderAppleFM, "system"},
		{"apple:pcc", ProviderAppleFM, "pcc"},
		{"", ProviderClaude, ""},
	}
	for _, tt := range tests {
		got := ParseModelRef(tt.in)
		if got.Provider != tt.provider || got.Model != tt.model {
			t.Errorf("ParseModelRef(%q) = %+v, want {%s %s}", tt.in, got, tt.provider, tt.model)
		}
	}
}

func TestValidateLocalRouting(t *testing.T) {
	base := ModelsConfig{Classify: "claude-haiku-4-5", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8"}
	tests := []struct {
		name    string
		mutate  func(*ModelsConfig)
		wantErr bool
	}{
		{"all claude", func(m *ModelsConfig) {}, false},
		{"ollama classify", func(m *ModelsConfig) { m.Classify = "ollama:qwen3:8b" }, false},
		{"apple classify", func(m *ModelsConfig) { m.Classify = "apple" }, false},
		{"apple routine rejected", func(m *ModelsConfig) { m.Routine = "apple" }, true},
		{"local synthesis rejected", func(m *ModelsConfig) { m.Synthesis = "ollama:qwen3:8b" }, true},
		{"empty ollama model rejected", func(m *ModelsConfig) { m.Classify = "ollama:" }, true},
		{"bad fallback rejected", func(m *ModelsConfig) { m.LocalFallback = "retry" }, true},
		{"eval_min_pass in range ok", func(m *ModelsConfig) { m.EvalMinPass = 80 }, false},
		{"eval_min_pass over 100 rejected", func(m *ModelsConfig) { m.EvalMinPass = 150 }, true},
		{"verify ollama ok", func(m *ModelsConfig) { m.Verify = "ollama:judge" }, false},
		{"verify off ok", func(m *ModelsConfig) { m.Verify = "off" }, false},
		{"verify claude rejected", func(m *ModelsConfig) { m.Verify = "claude-haiku-4-5" }, true},
		{"verify apple rejected", func(m *ModelsConfig) { m.Verify = "apple" }, true},
		{"verify empty-model rejected", func(m *ModelsConfig) { m.Verify = "ollama:" }, true},
		{"verify_min_score over range rejected", func(m *ModelsConfig) { m.VerifyMinScore = 11 }, true},
		// FR-192 (narrowed by FR-194): apple-fm:* stays reserved; unknown
		// apple:<x> variants are rejected rather than misrouted to claude -p.
		{"apple-fm: reserved", func(m *ModelsConfig) { m.Classify = "apple-fm:foo" }, true},
		{"bare apple-fm reserved", func(m *ModelsConfig) { m.Synthesis = "apple-fm" }, true},
		{"unknown apple variant rejected", func(m *ModelsConfig) { m.Routine = "apple:on-device" }, true},
		// FR-194: the two fm-backed variants.
		{"apple:system classify ok", func(m *ModelsConfig) { m.Classify = "apple:system" }, false},
		{"apple:system routine rejected (on-device cap)", func(m *ModelsConfig) { m.Routine = "apple:system" }, true},
		{"apple:system synthesis rejected", func(m *ModelsConfig) { m.Synthesis = "apple:system" }, true},
		// FR-195: apple:pcc needs the explicit opt-in.
		{"apple:pcc without opt-in rejected", func(m *ModelsConfig) { m.Classify = "apple:pcc" }, true},
		{"apple:pcc classify with opt-in ok", func(m *ModelsConfig) { m.Classify = "apple:pcc"; m.PCCEnabled = true }, false},
		{"apple:pcc routine with opt-in ok", func(m *ModelsConfig) { m.Routine = "apple:pcc"; m.PCCEnabled = true }, false},
		{"apple:pcc synthesis rejected even with opt-in", func(m *ModelsConfig) { m.Synthesis = "apple:pcc"; m.PCCEnabled = true }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			tt.mutate(&m)
			err := validateLocalRouting(m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateLocalRouting: err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestModelsFallbackDefault(t *testing.T) {
	if got := (ModelsConfig{}).Fallback(); got != "claude" {
		t.Fatalf("default fallback = %q, want claude", got)
	}
	if got := (ModelsConfig{LocalFallback: "fail"}).Fallback(); got != "fail" {
		t.Fatalf("fallback = %q, want fail", got)
	}
}

// FR-197: apple:pcc vision requires the same PCC opt-in as the tier.
func TestValidateVision(t *testing.T) {
	for _, tt := range []struct {
		name    string
		p       Profile
		wantErr bool
	}{
		{"off ok", Profile{}, false},
		{"ollama ok", Profile{Ingestion: IngestionConfig{Vision: "ollama:qwen2.5vl"}}, false},
		{"apple ok without opt-in", Profile{Ingestion: IngestionConfig{Vision: "apple"}}, false},
		{"apple:pcc without opt-in rejected", Profile{Ingestion: IngestionConfig{Vision: "apple:pcc"}}, true},
		{"apple:pcc with opt-in ok", Profile{Ingestion: IngestionConfig{Vision: "apple:pcc"}, Models: ModelsConfig{PCCEnabled: true}}, false},
		{"unknown apple variant rejected", Profile{Ingestion: IngestionConfig{Vision: "apple:weird"}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateVision(tt.p); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
