package config

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
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
		{"note path .axon non-allowlisted", func(r *Recipe) { r.Inputs[1].Note.Path = ".axon/logs/run.md" }, "path"},
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

func TestRecipePathReadWriteSplit(t *testing.T) {
	cases := []struct {
		path            string
		readOK, writeOK bool
	}{
		{"03-Resources/Notes.md", true, true},
		{".axon/review-queue.md", true, false},         // FR-202: reading is not writing
		{".axon/review-queue-archive.md", true, false}, // the second allow-listed file
		{".axon/logs/run.md", false, false},            // everything else under .axon/ stays closed
		{".axon/exports/2026/bundle.md", false, false},
		{".axon/snapshots/s.md", false, false},
		{".trash/gone.md", false, false},
		{"../etc/passwd.md", false, false},
		{"/abs/path.md", false, false},
		{"notes.txt", false, false}, // non-.md
		{"   ", false, false},       // empty
	}
	for _, c := range cases {
		if got := validRecipeReadPath(c.path) == nil; got != c.readOK {
			t.Errorf("read %q: allowed=%v want %v", c.path, got, c.readOK)
		}
		if got := validRecipeWritePath(c.path) == nil; got != c.writeOK {
			t.Errorf("write %q: allowed=%v want %v", c.path, got, c.writeOK)
		}
	}
}

func TestValidateRecipesAllowsReviewQueueRead(t *testing.T) {
	r := validRecipe()
	r.Inputs[1].Note.Path = ".axon/review-queue.md"
	if err := validateRecipes(Profile{Recipes: []Recipe{r}}); err != nil {
		t.Fatalf("block-sink recipe reading the review queue must be legal: %v", err)
	}
}

func TestValidateRecipesNewReaders(t *testing.T) {
	base := func() Recipe {
		r := validRecipe()
		r.Inputs = []RecipeInput{
			{Name: "dormant", StaleNotes: &RecipeStaleInput{OlderThanDays: 365, Limit: 20}},
			{Name: "old", Sources: &RecipeSourcesInput{OlderThanDays: 180, Limit: 30}},
		}
		r.Prompt = "Stale: {{dormant}} Sources: {{old}}"
		return r
	}
	if err := validateRecipes(Profile{Recipes: []Recipe{base()}}); err != nil {
		t.Fatalf("valid new readers rejected: %v", err)
	}
	// Zero means "apply the default" for both, so an all-zero reader is legal.
	z := base()
	z.Inputs[0].StaleNotes = &RecipeStaleInput{}
	z.Inputs[1].Sources = &RecipeSourcesInput{}
	if err := validateRecipes(Profile{Recipes: []Recipe{z}}); err != nil {
		t.Fatalf("zero-valued new readers rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Recipe)
		want string
	}{
		{"stale days too big", func(r *Recipe) { r.Inputs[0].StaleNotes.OlderThanDays = 3651 }, "older_than_days"},
		{"stale days negative", func(r *Recipe) { r.Inputs[0].StaleNotes.OlderThanDays = -1 }, "older_than_days"},
		{"stale limit too big", func(r *Recipe) { r.Inputs[0].StaleNotes.Limit = 101 }, "limit"},
		{"sources days too big", func(r *Recipe) { r.Inputs[1].Sources.OlderThanDays = 3651 }, "older_than_days"},
		{"sources limit negative", func(r *Recipe) { r.Inputs[1].Sources.Limit = -1 }, "limit"},
		{"stale plus note is two readers", func(r *Recipe) {
			r.Inputs[0].Note = &RecipeNoteInput{Path: "a.md"}
		}, "exactly one"},
		{"sources plus search is two readers", func(r *Recipe) {
			r.Inputs[1].Search = &RecipeSearchInput{Query: "x"}
		}, "exactly one"},
	}
	for _, c := range cases {
		r := base()
		c.mut(&r)
		err := validateRecipes(Profile{Recipes: []Recipe{r}})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestValidateRecipesRefusesSelfFeedingReviewRecipe(t *testing.T) {
	// review sink + reads its own queue => the change-gate can never skip.
	bad := validRecipe()
	bad.Inputs[1].Note.Path = ".axon/review-queue.md"
	bad.Output = RecipeOutput{Review: &RecipeReviewSink{}}
	err := validateRecipes(Profile{Recipes: []Recipe{bad}})
	if err == nil || !strings.Contains(err.Error(), "own output") {
		t.Fatalf("self-feeding review recipe must be refused, got %v", err)
	}

	// Legal neighbour 1: review sink reading the ARCHIVE (a human must act
	// before anything lands there).
	okArchive := validRecipe()
	okArchive.Inputs[1].Note.Path = ".axon/review-queue-archive.md"
	okArchive.Output = RecipeOutput{Review: &RecipeReviewSink{}}
	if err := validateRecipes(Profile{Recipes: []Recipe{okArchive}}); err != nil {
		t.Fatalf("review sink reading the archive must be legal: %v", err)
	}

	// Legal neighbour 2: BLOCK sink reading the queue — D3's actual case.
	okBlock := validRecipe()
	okBlock.Inputs[1].Note.Path = ".axon/review-queue.md"
	if err := validateRecipes(Profile{Recipes: []Recipe{okBlock}}); err != nil {
		t.Fatalf("block sink reading the queue must be legal: %v", err)
	}
}

// The example config ships its recipes commented out, so nothing validates
// them — a renamed field would rot every example silently and greet the next
// user who copies one with a validation error. This uncomments the personal
// profile's `recipes:` block and runs it through the real validator.
func TestExampleConfigRecipesValidate(t *testing.T) {
	raw, err := os.ReadFile(exampleConfigPath(t))
	if err != nil {
		t.Fatal(err)
	}
	block, n := uncommentRecipes(string(raw))
	if n == 0 {
		t.Fatal("no commented recipe example found in axon.config.example.yaml — did the block move?")
	}
	var doc struct {
		Recipes []Recipe `yaml:"recipes"`
	}
	if err := yaml.Unmarshal([]byte(block), &doc); err != nil {
		t.Fatalf("the commented recipe example is not valid YAML once uncommented:\n%s\nerror: %v", block, err)
	}
	if len(doc.Recipes) == 0 {
		t.Fatalf("uncommented block parsed to zero recipes:\n%s", block)
	}
	if err := validateRecipes(Profile{Recipes: doc.Recipes}); err != nil {
		t.Fatalf("a shipped recipe example does not validate: %v\n%s", err, block)
	}
	t.Logf("validated %d shipped recipe example(s)", len(doc.Recipes))
}

// uncommentRecipes lifts every commented recipe list-item out of the example
// config and returns them as one YAML document plus the count of recipes
// found. It collects each `- name:` item and its more-indented continuation
// lines, skipping the prose lines that separate the examples — so all shipped
// examples are covered, not just the first.
func uncommentRecipes(src string) (string, int) {
	out := []string{"recipes:"}
	found := 0
	collecting := false
	baseIndent := 0
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			collecting = false
			continue
		}
		body := strings.TrimPrefix(trimmed, "#")
		if strings.HasPrefix(body, " ") {
			body = body[1:]
		}
		inner := strings.TrimSpace(body)
		// Indent of the content itself, measured inside the comment.
		indent := len(body) - len(strings.TrimLeft(body, " "))
		switch {
		case strings.HasPrefix(inner, "- name:") && (!collecting || indent <= baseIndent):
			// A new recipe item. The indent guard matters: `inputs:` also
			// contains `- name:` entries, and those are continuations, not
			// new recipes.
			collecting, baseIndent = true, indent
			found++
			out = append(out, "  "+inner)
		case collecting && inner == "":
			// A blank line inside a block scalar (the `render: |` templates
			// contain them). Keep it — dropping it truncates the template and
			// its placeholders go unreferenced.
			out = append(out, "")
		case collecting && indent > baseIndent && !strings.HasPrefix(inner, "#"):
			out = append(out, "  "+strings.Repeat(" ", indent-baseIndent)+inner)
		default:
			collecting = false
		}
	}
	return strings.Join(out, "\n"), found
}
