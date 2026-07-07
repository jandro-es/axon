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
