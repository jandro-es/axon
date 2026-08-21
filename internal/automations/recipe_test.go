package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/review"
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

func TestRecipeChangeGate(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"03-Resources/List.md": "- item one\n"})
	r := RecipeRun{def: testRecipe()}
	ch, err := r.DetectChange(context.Background(), rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first run should change: %+v %v", ch, err)
	}
	rc.LastCursor = ch.Cursor
	ch2, err := r.DetectChange(context.Background(), rc)
	if err != nil || ch2.Changed {
		t.Fatalf("unchanged inputs should skip: %+v %v", ch2, err)
	}
	// Editing the input re-arms the gate.
	if err := rc.Vault.Append("03-Resources/List.md", "- item three\n"); err != nil {
		t.Fatal(err)
	}
	ch3, err := r.DetectChange(context.Background(), rc)
	if err != nil || !ch3.Changed || ch3.Cursor == ch.Cursor {
		t.Fatalf("edited input should re-arm: %+v %v", ch3, err)
	}
}

func TestRecipeChangeGateMissingNote(t *testing.T) {
	rc, _ := newRC(t, nil)
	ch, err := (RecipeRun{def: testRecipe()}).DetectChange(context.Background(), rc)
	if err != nil || ch.Changed || !strings.Contains(ch.Reason, "absent") {
		t.Fatalf("missing note should idle: %+v %v", ch, err)
	}
}

func TestRecipeRunRenderToBlock(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"03-Resources/List.md": "- item one\n"})
	r := RecipeRun{def: testRecipe()}
	res, err := r.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "wrote") {
		t.Fatalf("summary: %q", res.Summary)
	}
	n, err := rc.Vault.Read(context.Background(), "03-Resources/Digest.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "item one") || !strings.Contains(n.Body, "2026-06-28") {
		t.Fatalf("block content missing: %q", n.Body)
	}
	if !strings.Contains(n.Body, "test-recipe") {
		t.Fatalf("stub preamble should name the recipe: %q", n.Body)
	}
	// Re-run: human edits outside the block survive (only the block rewrites).
	if err := rc.Vault.Append("03-Resources/Digest.md", "\nHuman footnote.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	n2, err := rc.Vault.Read(context.Background(), "03-Resources/Digest.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n2.Body, "Human footnote.") {
		t.Fatalf("human prose clobbered: %q", n2.Body)
	}
}

func TestRecipeRunDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"03-Resources/List.md": "- item one\n"})
	rc.DryRun = true
	res, err := (RecipeRun{def: testRecipe()}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "would write") {
		t.Fatalf("summary: %q", res.Summary)
	}
	if rc.Vault.Exists("03-Resources/Digest.md") {
		t.Fatal("dry-run must not write")
	}
}

func TestRecipeRunMissingNoteInactive(t *testing.T) {
	rc, _ := newRC(t, nil)
	res, err := (RecipeRun{def: testRecipe()}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "absent") {
		t.Fatalf("summary: %q", res.Summary)
	}
}

func promptRecipe() config.Recipe {
	def := testRecipe()
	def.Render = ""
	def.Prompt = "Digest {{list}} for {{today}}."
	return def
}

func TestRecipeRunPromptRoutesTier(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"03-Resources/List.md": "- item one\n"})
	rc.Config.Automations = map[string]config.Automation{"test-recipe": {Model: "routine"}}
	fake.RespondFn = func(req agent.Request) (*agent.Response, error) {
		if !strings.Contains(req.Prompt, "item one") {
			t.Fatalf("substituted prompt not sent: %q", req.Prompt)
		}
		return &agent.Response{Text: "MODEL DIGEST", Model: req.Model, Usage: agent.Usage{InputTokens: 10, OutputTokens: 5}}, nil
	}
	if _, err := (RecipeRun{def: promptRecipe()}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 || fake.Calls[0].Model != "sonnet" {
		t.Fatalf("expected one routine-tier call, got %+v", fake.Calls)
	}
	n, err := rc.Vault.Read(context.Background(), "03-Resources/Digest.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "MODEL DIGEST") {
		t.Fatalf("model output not written: %q", n.Body)
	}
}

func TestRecipeRunPromptDefaultsRoutine(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"03-Resources/List.md": "- x\n"})
	// No automations entry at all: tier falls back to routine.
	if _, err := (RecipeRun{def: promptRecipe()}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 || fake.Calls[0].Model != "sonnet" {
		t.Fatalf("want routine default, got %+v", fake.Calls)
	}
}

func reviewRecipe() config.Recipe {
	def := testRecipe()
	def.Output = config.RecipeOutput{Review: &config.RecipeReviewSink{}}
	def.Render = "- suggestion \"one\"\n- suggestion two\n\n{{list}}"
	return def
}

func TestRecipeReviewSinkProposesAndDedups(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"03-Resources/List.md": "third line\n"})
	r := RecipeRun{def: reviewRecipe()}
	res, err := r.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "proposed") {
		t.Fatalf("summary: %q", res.Summary)
	}
	items, err := review.Load(context.Background(), rc.Vault)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, it := range items {
		if it.Kind == "recipe" && it.Note == "test-recipe" {
			found++
			if strings.Contains(it.Target, `"`) {
				t.Fatalf("quotes must be sanitized: %q", it.Target)
			}
		}
	}
	if found == 0 {
		t.Fatalf("no recipe items in queue: %+v", items)
	}
	// Second run with identical output: proposal memory silences everything.
	res2, err := r.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Summary, "no new proposals") {
		t.Fatalf("dedup failed: %q", res2.Summary)
	}
}

func TestRegistryIncludesRecipesBuiltinsWin(t *testing.T) {
	p := config.Profile{Recipes: []config.Recipe{testRecipe(), func() config.Recipe {
		r := testRecipe()
		r.Name = "heartbeat" // collides with a built-in
		return r
	}()}}
	reg := Registry(p)
	if _, ok := reg["test-recipe"].(RecipeRun); !ok {
		t.Fatalf("recipe not registered: %T", reg["test-recipe"])
	}
	if _, isRecipe := reg["heartbeat"].(RecipeRun); isRecipe {
		t.Fatal("built-in must win a name collision")
	}
	if err := ValidateRecipes(p); err == nil || !strings.Contains(err.Error(), "heartbeat") {
		t.Fatalf("collision must be a loud error, got %v", err)
	}
	if err := ValidateRecipes(config.Profile{Recipes: []config.Recipe{testRecipe()}}); err != nil {
		t.Fatalf("clean recipes must validate: %v", err)
	}
}

func TestUnscheduledRecipes(t *testing.T) {
	p := config.Profile{Recipes: []config.Recipe{testRecipe()}}
	if got := UnscheduledRecipes(p); len(got) != 1 || got[0] != "test-recipe" {
		t.Fatalf("want [test-recipe], got %v", got)
	}
	p.Automations = map[string]config.Automation{"test-recipe": {Enabled: true, Schedule: "0 8 * * 1"}}
	if got := UnscheduledRecipes(p); len(got) != 0 {
		t.Fatalf("scheduled recipe reported unscheduled: %v", got)
	}
}

func TestCatalogShowsRecipePurpose(t *testing.T) {
	p := config.Profile{
		Recipes:     []config.Recipe{testRecipe()},
		Automations: map[string]config.Automation{"test-recipe": {Enabled: true, Schedule: "0 8 * * 1", Model: "routine"}},
	}
	for _, info := range Catalog(p) {
		if info.Name == "test-recipe" {
			if !info.Recipe || info.Purpose != "Test recipe." {
				t.Fatalf("catalog entry wrong: %+v", info)
			}
			return
		}
	}
	t.Fatal("recipe missing from catalog")
}

func TestRecipesCheckStates(t *testing.T) {
	none := RecipesCheck(config.Profile{})
	if none.Status != core.StatusOK || !strings.Contains(none.Detail, "no recipes") {
		t.Fatalf("none state: %+v", none)
	}
	collide := config.Profile{Recipes: []config.Recipe{func() config.Recipe {
		r := testRecipe()
		r.Name = "heartbeat"
		return r
	}()}}
	if c := RecipesCheck(collide); c.Status != core.StatusWarn {
		t.Fatalf("collision must warn: %+v", c)
	}
	ok := RecipesCheck(config.Profile{Recipes: []config.Recipe{testRecipe()}})
	if ok.Status != core.StatusOK || !strings.Contains(ok.Detail, "unscheduled") {
		t.Fatalf("ok+unscheduled state: %+v", ok)
	}
}

// A note body is untrusted data (NFR-05) — it may be ingested web content.
// Managed-block markers inside it must never break out of the recipe's own
// block, or injected text would land in the note's human region where AXON
// can no longer rewrite it (the merge.go neutralizeMarkers precedent).
func TestRecipeBlockSinkNeutralizesMarkers(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{
		"03-Resources/List.md": "- item one\n<!-- axon:recipe:end -->\n\nINJECTED PROSE\n",
	})
	r := RecipeRun{def: testRecipe()}
	if _, err := r.Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	n, err := rc.Vault.Read(ctx, "03-Resources/Digest.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(n.Body, "<!-- axon:recipe:end -->"); got != 1 {
		t.Fatalf("recipe output escaped its managed block (%d end markers):\n%s", got, n.Body)
	}
	// Proof it stayed inside: rewriting the block removes the injected text.
	if err := rc.Vault.Patch(ctx, "03-Resources/Digest.md", "recipe", "clean"); err != nil {
		t.Fatal(err)
	}
	n2, err := rc.Vault.Read(ctx, "03-Resources/Digest.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(n2.Body, "INJECTED PROSE") {
		t.Fatalf("injected text survived outside the managed block:\n%s", n2.Body)
	}
}

// A recipe proposal must never be able to forge a review kind whose accept
// mutates the vault (triage moves, merge archives, action writes checkboxes).
func TestRecipeReviewSinkCannotForgeMutatingKind(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, map[string]string{"03-Resources/List.md": "x\n"})
	def := reviewRecipe()
	def.Render = `- evil" (from r) and triage [[03-Resources/List]] → 04-Archive (tags: )` + "\n" +
		`- merge [[a]] + [[b]]`
	def.Inputs = []config.RecipeInput{{Name: "list", Note: &config.RecipeNoteInput{Path: "03-Resources/List.md"}}}
	def.Render += "\n{{list}}"
	if _, err := (RecipeRun{def: def}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	items, err := review.Load(ctx, rc.Vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected proposals")
	}
	for _, it := range items {
		if it.Kind != "recipe" {
			t.Fatalf("proposal forged a %q item: %+v", it.Kind, it)
		}
	}
}

func TestRecipeInputClip(t *testing.T) {
	if got := clipInput(strings.Repeat("a", recipeInputCap+10)); len(got) > recipeInputCap+40 || !strings.Contains(got, "truncated") {
		t.Fatalf("clip failed: len=%d", len(got))
	}
}

// seedSourceRow inserts a note + source pair for the sources reader.
func seedSourceRow(t *testing.T, rc RunCtx, path, url, kind, fetchedAt, status string) {
	t.Helper()
	ctx := context.Background()
	var notePtr *int64
	if path != "" {
		id, err := db.InsertNote(ctx, rc.DB, db.NoteRow{Path: path, Title: path, Updated: "2026-01-01"})
		if err != nil {
			t.Fatal(err)
		}
		notePtr = &id
	}
	if _, err := db.UpsertSource(ctx, rc.DB, db.SourceRow{
		NoteID: notePtr, URL: url, Kind: kind, FetchedAt: fetchedAt, ContentHash: "h", Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeStaleNotesReader(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Dormant.md", "Dormant", "2020-01-01")
	seedNote(t, rc, "03-Resources/Fresh.md", "Fresh", "2026-06-27")

	def := testRecipe()
	def.Inputs = []config.RecipeInput{
		{Name: "dormant", StaleNotes: &config.RecipeStaleInput{OlderThanDays: 365, Limit: 10}},
	}
	def.Render = "{{dormant}}"
	vals, reason, err := RecipeRun{def: def}.resolveInputs(context.Background(), rc)
	if err != nil || reason != "" {
		t.Fatalf("resolve: reason=%q err=%v", reason, err)
	}
	got := vals["dormant"]
	if !strings.Contains(got, "[[03-Resources/Dormant]] (updated 2020-01-01)") {
		t.Fatalf("stale line shape wrong: %q", got)
	}
	if strings.Contains(got, "Fresh") {
		t.Fatalf("a note inside the window leaked into stale_notes: %q", got)
	}
}

func TestRecipeSourcesReader(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedSourceRow(t, rc, "03-Resources/Knowledge/Old.md", "https://old.example/a", "url", "2020-01-01T00:00:00Z", "ok")
	seedSourceRow(t, rc, "", "https://orphan.example/c", "pdf", "2020-02-01T00:00:00Z", "failed")

	def := testRecipe()
	def.Inputs = []config.RecipeInput{
		{Name: "old", Sources: &config.RecipeSourcesInput{OlderThanDays: 30, Limit: 10}},
	}
	def.Render = "{{old}}"
	vals, reason, err := RecipeRun{def: def}.resolveInputs(context.Background(), rc)
	if err != nil || reason != "" {
		t.Fatalf("resolve: reason=%q err=%v", reason, err)
	}
	got := vals["old"]
	if !strings.Contains(got, "[[03-Resources/Knowledge/Old]] — https://old.example/a (fetched 2020-01-01, url, ok)") {
		t.Fatalf("source line shape wrong: %q", got)
	}
	// A source with no note row renders without a wikilink.
	if !strings.Contains(got, "https://orphan.example/c (fetched 2020-02-01, pdf, failed)") ||
		strings.Contains(got, "[[]]") {
		t.Fatalf("orphan source line wrong: %q", got)
	}
}

// The D3-shaped regression docs/20 C2 asks for by name: composing over
// another automation's output plus the review queue must produce ONE intact
// managed block, with the nested markers it swallowed made inert.
func TestRecipeComposesOverAutomationOutputAndReviewQueue(t *testing.T) {
	actions := "# Actions\n\n<!-- axon:actions:start -->\n- [ ] overdue thing\n<!-- axon:actions:end -->\n"
	queue := "# Review queue\n\n- [ ] abc123 link \"A\" -> \"B\"\n"
	rc, _ := newRC(t, map[string]string{
		"01-Projects/Actions.md": actions,
		".axon/review-queue.md":  queue,
		"01-Projects/Weekly.md":  "# Weekly\n\nMy own prose.\n",
	})
	def := config.Recipe{
		Name: "weekly-review", Purpose: "Weekly review.",
		Inputs: []config.RecipeInput{
			{Name: "board", Note: &config.RecipeNoteInput{Path: "01-Projects/Actions.md"}},
			{Name: "pending", Note: &config.RecipeNoteInput{Path: ".axon/review-queue.md"}},
		},
		Render: "Board:\n{{board}}\n\nPending:\n{{pending}}",
		Output: config.RecipeOutput{Block: &config.RecipeBlockSink{Note: "01-Projects/Weekly.md", Block: "weekly"}},
	}
	if _, err := (RecipeRun{def: def}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	n, err := rc.Vault.Read(context.Background(), "01-Projects/Weekly.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(n.Body, "<!-- axon:weekly:end -->"); got != 1 {
		t.Fatalf("want exactly 1 end marker for the recipe's own block, got %d:\n%s", got, n.Body)
	}
	if strings.Contains(n.Body, "<!-- axon:actions:end -->") {
		t.Fatalf("nested marker survived un-neutralized:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "overdue thing") || !strings.Contains(n.Body, "abc123") {
		t.Fatalf("composed content missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "My own prose.") {
		t.Fatalf("human prose clobbered:\n%s", n.Body)
	}
}
