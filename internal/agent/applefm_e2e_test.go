package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAppleFMEndToEnd compiles the real Swift helper and runs a tiny
// generation on the on-device model. Skipped unless darwin + swiftc + the
// model is actually available (CI runners and managed Macs often can't) —
// the same gating convention as the Apple embeddings e2e.
func TestAppleFMEndToEnd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("apple foundation models: darwin only")
	}
	if !SwiftAvailable() {
		t.Skip("swiftc not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	helper := filepath.Join(t.TempDir(), "axon-apple-lm")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := EnsureAppleLMHelper(ctx, helper); err != nil {
		// An SDK too old to have FoundationModels is an environment gap, not
		// a code failure — surface it as a skip with the compiler output.
		if strings.Contains(err.Error(), "FoundationModels") {
			t.Skipf("FoundationModels SDK unavailable: %v", err)
		}
		t.Fatalf("compile helper: %v", err)
	}
	// Availability probe first: skip (not fail) on machines without Apple
	// Intelligence — mirrors the embeddings e2e's asset gating.
	if out, err := exec.CommandContext(ctx, helper, "--check-availability").CombinedOutput(); err != nil {
		t.Skipf("on-device model unavailable: %s", strings.TrimSpace(string(out)))
	}

	a := NewAppleFM(helper)
	resp, err := a.Run(ctx, Request{
		Model:  "apple-foundation-v1",
		System: "Reply with a single word.",
		Prompt: "Say ok.",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(resp.Text) == "" {
		t.Fatal("empty generation")
	}

	// Guided generation (FR-80): a schema-constrained call must return JSON
	// with the requested keys.
	resp, err = a.Run(ctx, Request{
		Model:        "apple-foundation-v1",
		System:       "You label short notes.",
		Prompt:       "Give a title and tags for a note about morning coffee brewing.",
		OutputSchema: json.RawMessage(`{"properties":{"title":{"type":"string"},"tags":{"type":"array"}}}`),
	})
	if err != nil {
		t.Fatalf("guided run: %v", err)
	}
	var out struct {
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
	}
	if uerr := json.Unmarshal([]byte(resp.Text), &out); uerr != nil {
		t.Fatalf("guided output not valid JSON: %v\n%s", uerr, resp.Text)
	}
	if out.Title == "" {
		t.Fatalf("guided output missing title: %s", resp.Text)
	}
}
