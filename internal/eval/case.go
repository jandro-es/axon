// Package eval is AXON's model-evaluation harness (R5.1, FR-140/141). It runs
// in-repo golden sets against any (provider, model) pair through the token
// chokepoint and grades them hybrid: deterministic for the classify family,
// must_include + a Claude judge for the routine family. It never imports
// internal/agent (cardinal rule 1) and never mutates the vault (cardinal rule 2).
package eval

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Family is the task family a case exercises (== the tier it would promote).
type Family string

const (
	FamilyClassify  Family = "classify"
	FamilyRoutine   Family = "routine"
	FamilySynthesis Family = "synthesis"
)

// Grade carries the pass/fail criteria. classify uses ExpectJSON/ExpectText
// (deterministic); routine uses MustInclude (+ optional Rubric for the judge).
type Grade struct {
	ExpectJSON  string   `yaml:"expect_json"` // raw JSON literal; graded by semantic equality
	ExpectText  string   `yaml:"expect_text"`
	MustInclude []string `yaml:"must_include"`
	Rubric      string   `yaml:"rubric"`
}

// Case is one self-contained golden example, loaded from an embedded YAML file.
type Case struct {
	Name   string `yaml:"name"`
	Family Family `yaml:"family"`
	System string `yaml:"system"`
	Prompt string `yaml:"prompt"`
	Grade  Grade  `yaml:"grade"`
}

//go:embed golden
var goldenFS embed.FS

// LoadCases parses every embedded golden/<family>/*.yaml. A family of "" or
// "all" loads every family; otherwise only that family's directory is read.
// Each case is validated so a malformed fixture fails loudly rather than
// silently scoring.
func LoadCases(family string) ([]Case, error) {
	return loadCasesFS(goldenFS, "golden", family)
}

// loadCasesFS is the fixture loader parameterised on the FS so tests can point
// it at a deliberately malformed set.
func loadCasesFS(fsys fs.FS, root, family string) ([]Case, error) {
	var out []Case
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		if family != "" && family != "all" && path.Base(path.Dir(p)) != family {
			return nil
		}
		raw, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", p, rerr)
		}
		var c Case
		if uerr := yaml.Unmarshal(raw, &c); uerr != nil {
			return fmt.Errorf("parse %s: %w", p, uerr)
		}
		if verr := c.validate(); verr != nil {
			return fmt.Errorf("invalid fixture %s: %w", p, verr)
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// validate enforces exactly one grading mode appropriate to the family so a
// malformed fixture cannot silently score.
func (c Case) validate() error {
	if c.Name == "" {
		return fmt.Errorf("missing name")
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("empty prompt")
	}
	switch c.Family {
	case FamilyClassify:
		if len(c.Grade.ExpectJSON) == 0 && c.Grade.ExpectText == "" {
			return fmt.Errorf("classify case needs expect_json or expect_text")
		}
		if len(c.Grade.ExpectJSON) > 0 && c.Grade.ExpectText != "" {
			return fmt.Errorf("classify case sets both expect_json and expect_text")
		}
	case FamilyRoutine:
		if len(c.Grade.MustInclude) == 0 {
			return fmt.Errorf("routine case needs at least one must_include anchor")
		}
	case FamilySynthesis:
		// Baseline-only (never promoted): must_include or a rubric is enough.
		if len(c.Grade.MustInclude) == 0 && c.Grade.Rubric == "" {
			return fmt.Errorf("synthesis case needs must_include or a rubric")
		}
	default:
		return fmt.Errorf("unknown family %q", c.Family)
	}
	return nil
}
