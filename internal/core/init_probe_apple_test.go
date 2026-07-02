package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestProbeAppleEmbedding(t *testing.T) {
	e := config.EmbeddingsConfig{Provider: "apple", Model: "apple-nlcontextual-v1", Dim: 512}
	ok := appleProbeDeps{
		goos:    "darwin",
		swiftOK: func() bool { return true },
		ensure:  func(ctx context.Context, path string) (bool, error) { return true, nil },
		probe:   func(ctx context.Context, helper string, e config.EmbeddingsConfig) error { return nil },
	}
	for _, tc := range []struct {
		name   string
		mutate func(*appleProbeDeps)
		status StepStatus
		detail string
	}{
		{"compiled and verified", func(*appleProbeDeps) {}, StepDone, "compiled"},
		{"non-darwin warns", func(d *appleProbeDeps) { d.goos = "linux" }, StepWarn, "macOS"},
		{"no swiftc warns", func(d *appleProbeDeps) { d.swiftOK = func() bool { return false } }, StepWarn, "xcode-select --install"},
		{"compile failure warns", func(d *appleProbeDeps) {
			d.ensure = func(context.Context, string) (bool, error) { return false, fmt.Errorf("boom") }
		}, StepWarn, "boom"},
		{"probe failure warns", func(d *appleProbeDeps) {
			d.probe = func(context.Context, string, config.EmbeddingsConfig) error {
				return fmt.Errorf("dim 512 != configured 768")
			}
		}, StepWarn, "dim"},
		{"already current", func(d *appleProbeDeps) {
			d.ensure = func(context.Context, string) (bool, error) { return false, nil }
		}, StepDone, "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := ok
			tc.mutate(&d)
			res := probeAppleEmbedding(context.Background(), e, d)
			if res.Status != tc.status || !strings.Contains(res.Detail, tc.detail) {
				t.Errorf("got %s %q; want %s containing %q", res.Status, res.Detail, tc.status, tc.detail)
			}
		})
	}
}

func TestProbeEmbeddingModelDispatchesApple(t *testing.T) {
	// The dispatcher must not return the generic "not checked" warning for
	// apple; it must run the apple probe.
	res := probeEmbeddingModel(context.Background(), config.EmbeddingsConfig{Provider: "apple", Model: "m", Dim: 512})
	if strings.Contains(res.Detail, "not checked") {
		t.Errorf("apple provider fell through to the unknown-provider branch: %q", res.Detail)
	}
}
