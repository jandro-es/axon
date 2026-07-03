package agent

import "testing"

func TestRouterResolve(t *testing.T) {
	fake := NewFake()
	r := Router{Claude: fake}

	tests := []struct {
		provider string
		wantErr  bool
	}{
		{"claude", false},
		{"", false}, // empty = claude, defensive default
		{"ollama", true},
		{"apple", true},
		{"gemini", true},
	}
	for _, tt := range tests {
		got, err := r.Resolve(tt.provider)
		if (err != nil) != tt.wantErr {
			t.Errorf("Resolve(%q): err=%v, wantErr=%v", tt.provider, err, tt.wantErr)
		}
		if !tt.wantErr && got != Agent(fake) {
			t.Errorf("Resolve(%q) returned wrong adapter", tt.provider)
		}
	}

	r.Ollama = fake
	if _, err := r.Resolve("ollama"); err != nil {
		t.Fatalf("Resolve(ollama) with adapter set: %v", err)
	}
}
