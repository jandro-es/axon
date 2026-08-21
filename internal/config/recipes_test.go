package config

import (
	"strings"
	"testing"
)

func validRecipe() Recipe {
	return Recipe{
		Name: "reading-digest", Purpose: "Weekly digest.",
		Inputs: []RecipeInput{
			{Name: "recent", RecentNotes: &RecipeRecentInput{LookbackDays: 7, Limit: 20}},
			{Name: "list", Note: &RecipeNoteInput{Path: "03-Resources/Reading List.md"}},
		},
		Prompt: "Summarise {{recent}} and {{list}} on {{today}}.",
		Output: RecipeOutput{Block: &RecipeBlockSink{Note: "03-Resources/Reading Digest.md", Block: "recipe"}},
	}
}

func TestValidateRecipesAcceptsValid(t *testing.T) {
	if err := validateRecipes(Profile{Recipes: []Recipe{validRecipe()}}); err != nil {
		t.Fatalf("valid recipe rejected: %v", err)
	}
	r := validRecipe() // render + review + search variant
	r.Prompt, r.Render = "", "Recent: {{recent}}\nList: {{list}}"
	r.Output = RecipeOutput{Review: &RecipeReviewSink{}}
	r.Inputs = append(r.Inputs, RecipeInput{Name: "hits", Search: &RecipeSearchInput{Query: "reading", TopK: 5}})
	r.Render += " {{hits}}"
	if err := validateRecipes(Profile{Recipes: []Recipe{r}}); err != nil {
		t.Fatalf("valid render/review recipe rejected: %v", err)
	}
}

func TestValidateRecipesRejects(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Recipe)
		want string
	}{
		{"bad name", func(r *Recipe) { r.Name = "Bad_Name" }, "name"},
		{"no purpose", func(r *Recipe) { r.Purpose = " " }, "purpose"},
		{"no inputs", func(r *Recipe) { r.Inputs = nil }, "input"},
		{"two readers", func(r *Recipe) { r.Inputs[0].Note = &RecipeNoteInput{Path: "a.md"} }, "exactly one"},
		{"no reader", func(r *Recipe) { r.Inputs[0].RecentNotes = nil }, "exactly one"},
		{"dup input name", func(r *Recipe) { r.Inputs[1].Name = "recent"; r.Inputs[1].Note.Path = "b.md" }, "duplicate"},
		{"empty query", func(r *Recipe) {
			r.Inputs[0] = RecipeInput{Name: "recent", Search: &RecipeSearchInput{Query: " "}}
		}, "query"},
		{"top_k too big", func(r *Recipe) {
			r.Inputs[0] = RecipeInput{Name: "recent", Search: &RecipeSearchInput{Query: "x", TopK: 21}}
		}, "top_k"},
		{"lookback too big", func(r *Recipe) { r.Inputs[0].RecentNotes.LookbackDays = 91 }, "lookback"},
		{"note path traversal", func(r *Recipe) { r.Inputs[1].Note.Path = "../etc/passwd.md" }, "path"},
		{"note path .axon", func(r *Recipe) { r.Inputs[1].Note.Path = ".axon/review-queue.md" }, "path"},
		{"both prompt+render", func(r *Recipe) { r.Render = "x {{recent}} {{list}}" }, "exactly one of"},
		{"neither prompt/render", func(r *Recipe) { r.Prompt = "" }, "exactly one of"},
		{"unknown placeholder", func(r *Recipe) { r.Prompt = "{{recent}} {{list}} {{nope}}" }, "nope"},
		{"unused input", func(r *Recipe) { r.Prompt = "only {{recent}}" }, "never referenced"},
		{"two sinks", func(r *Recipe) { r.Output.Review = &RecipeReviewSink{} }, "exactly one sink"},
		{"no sink", func(r *Recipe) { r.Output.Block = nil }, "exactly one sink"},
		{"reserved block", func(r *Recipe) { r.Output.Block.Block = "briefing" }, "reserved"},
		{"block target .trash", func(r *Recipe) { r.Output.Block.Note = ".trash/x.md" }, "path"},
		{"block target not md", func(r *Recipe) { r.Output.Block.Note = "x.txt" }, ".md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRecipe()
			tc.mut(&r)
			err := validateRecipes(Profile{Recipes: []Recipe{r}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateRecipesRejectsDuplicateNames(t *testing.T) {
	err := validateRecipes(Profile{Recipes: []Recipe{validRecipe(), validRecipe()}})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate-name error, got %v", err)
	}
}
