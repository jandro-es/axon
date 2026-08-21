package config

import (
	"fmt"
	"regexp"
	"strings"
)

// This file is the ADR-039 recipe vocabulary: user-defined declarative
// automations as config data (FR-199). Recipes deliberately live in
// config.yaml — outside every model write path — so a model call can never
// author or alter an automation.

// Recipe is one user-defined declarative automation (FR-199, ADR-039):
// named zero-Claude inputs, exactly one of prompt (one one-shot chokepoint
// call) or render (zero-model), and exactly one wikilink-safe sink.
type Recipe struct {
	Name    string        `yaml:"name"`
	Purpose string        `yaml:"purpose"`
	Inputs  []RecipeInput `yaml:"inputs"`
	Prompt  string        `yaml:"prompt,omitempty"`
	Render  string        `yaml:"render,omitempty"`
	Output  RecipeOutput  `yaml:"output"`
}

// RecipeInput names one reader; exactly one of the reader fields is set.
type RecipeInput struct {
	Name        string             `yaml:"name"`
	Note        *RecipeNoteInput   `yaml:"note,omitempty"`
	Search      *RecipeSearchInput `yaml:"search,omitempty"`
	RecentNotes *RecipeRecentInput `yaml:"recent_notes,omitempty"`
}

// RecipeNoteInput reads one note's body.
type RecipeNoteInput struct {
	Path string `yaml:"path"`
}

// RecipeSearchInput renders hybrid-search hits as "[[path]]: excerpt" lines.
type RecipeSearchInput struct {
	Query string `yaml:"query"`
	TopK  int    `yaml:"top_k,omitempty"`
}

// RecipeRecentInput renders recently-updated notes as "[[path]] (updated D)".
type RecipeRecentInput struct {
	LookbackDays int `yaml:"lookback_days,omitempty"`
	Limit        int `yaml:"limit,omitempty"`
}

// RecipeOutput is the sink; exactly one field is set.
type RecipeOutput struct {
	Block  *RecipeBlockSink  `yaml:"block,omitempty"`
	Review *RecipeReviewSink `yaml:"review,omitempty"`
}

// RecipeBlockSink rebuilds one axon:<block> managed block in one note.
type RecipeBlockSink struct {
	Note  string `yaml:"note"`
	Block string `yaml:"block"`
}

// RecipeReviewSink proposes output lines to the review queue.
type RecipeReviewSink struct{}

const maxRecipeInputs = 8

var (
	recipeNameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)
	recipeInputNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)
	recipeBlockRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	placeholderRe     = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
)

// reservedRecipeBlocks are managed-block names owned by built-in automations;
// a recipe may never write them (ADR-039).
var reservedRecipeBlocks = map[string]bool{
	"heartbeat": true, "briefing": true, "actions": true, "answers": true,
	"memory": true, "pulse": true, "mentions": true, "merged": true,
	"tasks": true, "summary": true, "report": true, "deep": true, "links": true,
}

// validRecipePath enforces the vault-relative note-path rules shared by note
// inputs and block sinks.
func validRecipePath(p string) error {
	switch {
	case strings.TrimSpace(p) == "":
		return fmt.Errorf("path is required")
	case !strings.HasSuffix(p, ".md"):
		return fmt.Errorf("path %q must end in .md", p)
	case strings.HasPrefix(p, "/") || strings.Contains(p, ".."):
		return fmt.Errorf("path %q must be vault-relative without '..'", p)
	case strings.HasPrefix(p, ".axon/") || strings.HasPrefix(p, ".trash/"):
		return fmt.Errorf("path %q may not target .axon/ or .trash/", p)
	}
	return nil
}

// validateRecipes is the FR-199 cross-field pass (the validateVision
// pattern), run per profile from Config.Validate.
func validateRecipes(p Profile) error {
	seen := map[string]bool{}
	for i, r := range p.Recipes {
		where := fmt.Sprintf("recipes[%d]", i)
		if r.Name != "" {
			where = fmt.Sprintf("recipe %q", r.Name)
		}
		if !recipeNameRe.MatchString(r.Name) {
			return fmt.Errorf("%s: name must match ^[a-z0-9][a-z0-9-]{1,40}$", where)
		}
		if seen[r.Name] {
			return fmt.Errorf("%s: duplicate recipe name", where)
		}
		seen[r.Name] = true
		if strings.TrimSpace(r.Purpose) == "" {
			return fmt.Errorf("%s: purpose is required", where)
		}
		if len(r.Inputs) == 0 || len(r.Inputs) > maxRecipeInputs {
			return fmt.Errorf("%s: between 1 and %d inputs required", where, maxRecipeInputs)
		}
		inNames := map[string]bool{}
		for _, in := range r.Inputs {
			if !recipeInputNameRe.MatchString(in.Name) {
				return fmt.Errorf("%s: input name %q must match ^[a-z0-9][a-z0-9_-]{0,40}$", where, in.Name)
			}
			if inNames[in.Name] {
				return fmt.Errorf("%s: duplicate input name %q", where, in.Name)
			}
			inNames[in.Name] = true
			readers := 0
			if in.Note != nil {
				readers++
				if err := validRecipePath(in.Note.Path); err != nil {
					return fmt.Errorf("%s input %q: %w", where, in.Name, err)
				}
			}
			if in.Search != nil {
				readers++
				if strings.TrimSpace(in.Search.Query) == "" {
					return fmt.Errorf("%s input %q: search query is required", where, in.Name)
				}
				if in.Search.TopK < 0 || in.Search.TopK > 20 {
					return fmt.Errorf("%s input %q: top_k must be 0–20", where, in.Name)
				}
			}
			if in.RecentNotes != nil {
				readers++
				if in.RecentNotes.LookbackDays < 0 || in.RecentNotes.LookbackDays > 90 {
					return fmt.Errorf("%s input %q: lookback_days must be 0–90", where, in.Name)
				}
				if in.RecentNotes.Limit < 0 || in.RecentNotes.Limit > 100 {
					return fmt.Errorf("%s input %q: limit must be 0–100", where, in.Name)
				}
			}
			if readers != 1 {
				return fmt.Errorf("%s input %q: exactly one of note, search, recent_notes required", where, in.Name)
			}
		}
		hasPrompt := strings.TrimSpace(r.Prompt) != ""
		hasRender := strings.TrimSpace(r.Render) != ""
		if hasPrompt == hasRender {
			return fmt.Errorf("%s: exactly one of prompt or render required", where)
		}
		tmpl := r.Prompt
		if hasRender {
			tmpl = r.Render
		}
		used := map[string]bool{}
		for _, m := range placeholderRe.FindAllStringSubmatch(tmpl, -1) {
			ph := strings.TrimSpace(m[1])
			if ph == "today" {
				continue
			}
			if !inNames[ph] {
				return fmt.Errorf("%s: unknown placeholder {{%s}}", where, ph)
			}
			used[ph] = true
		}
		for n := range inNames {
			if !used[n] {
				return fmt.Errorf("%s: input %q is never referenced in the template", where, n)
			}
		}
		sinks := 0
		if b := r.Output.Block; b != nil {
			sinks++
			if err := validRecipePath(b.Note); err != nil {
				return fmt.Errorf("%s output: %w", where, err)
			}
			if !recipeBlockRe.MatchString(b.Block) {
				return fmt.Errorf("%s output: block name %q must match ^[a-z0-9][a-z0-9-]{0,40}$", where, b.Block)
			}
			if reservedRecipeBlocks[b.Block] {
				return fmt.Errorf("%s output: block %q is reserved for built-in automations", where, b.Block)
			}
		}
		if r.Output.Review != nil {
			sinks++
		}
		if sinks != 1 {
			return fmt.Errorf("%s: exactly one sink (output.block or output.review) required", where)
		}
	}
	return nil
}
