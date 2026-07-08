package tokens

import (
	"strings"
	"testing"
)

func TestParseVerifyScore(t *testing.T) {
	cases := []struct {
		in    string
		score int
		ok    bool
	}{
		{"8", 8, true},
		{"score: 3", 3, true},
		{"10", 10, true},
		{"12", 10, true}, // clamp
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseVerifyScore(c.in)
		if ok != c.ok || (ok && got != c.score) {
			t.Errorf("parseVerifyScore(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.score, c.ok)
		}
	}
}

func TestBuildVerifyPromptIncludesTaskAndAnswer(t *testing.T) {
	sys, prompt := buildVerifyPrompt("be terse", []Message{{Role: "user", Content: "capital of France?"}}, "Paris")
	if sys == "" {
		t.Fatal("empty judge system prompt")
	}
	if !strings.Contains(prompt, "capital of France?") || !strings.Contains(prompt, "Paris") {
		t.Fatalf("prompt missing task or answer: %q", prompt)
	}
}
