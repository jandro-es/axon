package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func testRecipe() config.Recipe {
	return config.Recipe{
		Name: "test-recipe", Purpose: "Test recipe.",
		Inputs: []config.RecipeInput{
			{Name: "list", Note: &config.RecipeNoteInput{Path: "03-Resources/List.md"}},
		},
		Render: "On {{today}}:\n{{list}}",
		Output: config.RecipeOutput{Block: &config.RecipeBlockSink{Note: "03-Resources/Digest.md", Block: "recipe"}},
	}
}

// seedNote inserts a derived notes row directly, for recent_notes tests.
func seedNote(t *testing.T, rc RunCtx, path, title, updated string) {
	t.Helper()
	if _, err := db.InsertNote(context.Background(), rc.DB, db.NoteRow{
		Path: path, Title: title, Updated: updated,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeResolveInputsNoteAndSubstitute(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"03-Resources/List.md": "- item one\n- item two\n"})
	r := RecipeRun{def: testRecipe()}
	vals, reason, err := r.resolveInputs(context.Background(), rc)
	if err != nil || reason != "" {
		t.Fatalf("resolve: vals=%v reason=%q err=%v", vals, reason, err)
	}
	if !strings.Contains(vals["list"], "item one") {
		t.Fatalf("note body not resolved: %q", vals["list"])
	}
	got := r.substitute(r.def.Render, vals, rc)
	if !strings.Contains(got, "2026-06-28") || !strings.Contains(got, "item two") {
		t.Fatalf("substitution wrong: %q", got)
	}
	if strings.Contains(got, "{{") {
		t.Fatalf("unresolved placeholder survived: %q", got)
	}
}

func TestRecipeResolveInputsMissingNoteIdles(t *testing.T) {
	rc, _ := newRC(t, nil)
	r := RecipeRun{def: testRecipe()}
	_, reason, err := r.resolveInputs(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reason, "absent") {
		t.Fatalf("want absent reason, got %q", reason)
	}
}

func TestRecipeResolveInputsRecentNotes(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "01-Projects/Alpha.md", "Alpha", "2026-06-27")
	def := testRecipe()
	def.Inputs = []config.RecipeInput{{Name: "recent", RecentNotes: &config.RecipeRecentInput{LookbackDays: 7, Limit: 10}}}
	def.Render = "{{recent}}"
	r := RecipeRun{def: def}
	vals, reason, err := r.resolveInputs(context.Background(), rc)
	if err != nil || reason != "" {
		t.Fatalf("resolve: %q %v", reason, err)
	}
	if !strings.Contains(vals["recent"], "[[01-Projects/Alpha]]") {
		t.Fatalf("recent notes not rendered as wikilinks: %q", vals["recent"])
	}
}

func TestRecipeInputClip(t *testing.T) {
	if got := clipInput(strings.Repeat("a", recipeInputCap+10)); len(got) > recipeInputCap+40 || !strings.Contains(got, "truncated") {
		t.Fatalf("clip failed: len=%d", len(got))
	}
}
