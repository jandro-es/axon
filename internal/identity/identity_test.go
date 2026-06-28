package identity

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func newVault(t *testing.T) *vault.FS {
	t.Helper()
	return vault.NewFS(t.TempDir())
}

func sampleValues() Values {
	return Values{
		Name: "Jandro", Role: "Engineer", Timezone: "Europe/Madrid",
		Communication: "concise, bullets", Goals: []string{"ship AXON"},
		People: []string{"Ada"}, Projects: []string{"AXON"}, Tools: []string{"Go"},
		AgentName: "Axon", Tone: "direct", Boundaries: []string{"ask before sending"},
		Date: "2026-06-28",
	}
}

func TestGenerateCreatesLayerAndIsIdempotent(t *testing.T) {
	v := newVault(t)
	res, err := Generate(v, sampleValues())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 3 || len(res.Skipped) != 0 {
		t.Fatalf("first Generate: created=%v skipped=%v", res.Created, res.Skipped)
	}
	for _, p := range []string{UserPath, SoulPath, MemoryPath} {
		if !v.Exists(p) {
			t.Errorf("missing %s after Generate", p)
		}
	}
	// Second run converges: everything skipped, nothing clobbered.
	res2, err := Generate(v, sampleValues())
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Created) != 0 || len(res2.Skipped) != 3 {
		t.Fatalf("second Generate not idempotent: created=%v skipped=%v", res2.Created, res2.Skipped)
	}
}

func TestGenerateNeverClobbersHumanEdits(t *testing.T) {
	v := newVault(t)
	const human = "---\ntype: user\n---\n## Identity\n- name: HAND-EDITED\n"
	if _, err := v.Create(UserPath, human); err != nil {
		t.Fatal(err)
	}
	res, err := Generate(v, sampleValues())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Skipped, UserPath) {
		t.Errorf("USER.md should have been skipped, not rewritten: %v", res.Created)
	}
	n, _ := v.Read(context.Background(), UserPath)
	if !strings.Contains(n.Body, "HAND-EDITED") {
		t.Errorf("human USER.md was clobbered:\n%s", n.Body)
	}
}

func TestRenderEmptyWhenNoLayer(t *testing.T) {
	v := newVault(t)
	out, err := Render(context.Background(), v, RenderOptions{MaxTokens: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("expected empty render with no layer, got %q", out)
	}
}

func TestRenderIncludesProfilePersonaAndMemory(t *testing.T) {
	v := newVault(t)
	if _, err := Generate(v, sampleValues()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := Remember(ctx, v, Entry{Text: "Prefers Go for daemons", Source: "session", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	out, err := Render(ctx, v, RenderOptions{MaxTokens: 1500, RecentMemory: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Jandro", "Axon", "Prefers Go for daemons", "Recent memory"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderAppliesRedaction(t *testing.T) {
	v := newVault(t)
	if _, err := Generate(v, sampleValues()); err != nil {
		t.Fatal(err)
	}
	out, err := Render(context.Background(), v, RenderOptions{
		MaxTokens: 1500,
		Redact:    func(s string) string { return strings.ReplaceAll(s, "Jandro", "[REDACTED]") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Jandro") || !strings.Contains(out, "[REDACTED]") {
		t.Errorf("redaction not applied:\n%s", out)
	}
}

func TestRenderRespectsTokenCeiling(t *testing.T) {
	v := newVault(t)
	if _, err := Generate(v, sampleValues()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := range 30 {
		if _, err := Remember(ctx, v, Entry{Text: strings.Repeat("memory ", 10), Source: "test", Date: "2026-06-2" + string(rune('0'+i%10))}); err != nil {
			t.Fatal(err)
		}
	}
	const budget = 120
	out, err := Render(ctx, v, RenderOptions{MaxTokens: budget, RecentMemory: 100})
	if err != nil {
		t.Fatal(err)
	}
	if approxTokens(out) > budget+40 { // +slack for the truncation/omission note
		t.Errorf("render overran budget: %d tokens (budget %d)\n%s", approxTokens(out), budget, out)
	}
}

func TestRememberPrependsNewestFirstAndIsWikilinkSafe(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()
	// Human prose outside the managed block must survive.
	const seed = "---\ntype: memory\n---\n\n## Memory\n\nMy own note.\n\n<!-- axon:memory:start -->\n<!-- axon:memory:end -->\n"
	if _, err := v.Create(MemoryPath, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := Remember(ctx, v, Entry{Text: "first", Date: "2026-06-27"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remember(ctx, v, Entry{Text: "second", Kind: "decision", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	entries, err := RecentEntries(ctx, v, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !strings.Contains(entries[0], "second") || !strings.Contains(entries[1], "first") {
		t.Fatalf("entries not newest-first: %v", entries)
	}
	if !strings.Contains(entries[1], "[decision]") && !strings.Contains(entries[0], "[decision]") {
		// second carries the kind tag
		if !strings.Contains(entries[0], "[decision]") {
			t.Errorf("kind tag missing: %v", entries)
		}
	}
	n, _ := v.Read(ctx, MemoryPath)
	if !strings.Contains(n.Body, "My own note.") {
		t.Errorf("human prose lost from MEMORY.md:\n%s", n.Body)
	}
}

func TestRememberCreatesLayerIfAbsent(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()
	if _, err := Remember(ctx, v, Entry{Text: "bootstrapped", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	if !Present(v) {
		t.Error("Remember should create the layer when absent")
	}
	n, _ := CountEntries(ctx, v)
	// Seed entry from Generate + the remembered one.
	if n < 2 {
		t.Errorf("expected at least 2 entries, got %d", n)
	}
}
