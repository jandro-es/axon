// Package scaffold creates AXON's vault layout — the PARA + Inbox folders,
// Daily/MOCs/Templates, the .axon system dir, folder READMEs and note templates
// — idempotently and without ever clobbering existing user content (FR-10,
// FR-11, init step 6). All writes go through the vault's wikilink-safe creation
// helpers; creating new files cannot break links, and existing files are never
// overwritten.
package scaffold

import (
	"embed"
	"fmt"

	"github.com/jandro-es/axon/internal/vault"
)

//go:embed assets
var assets embed.FS

// dirs are the vault directories AXON ensures exist. Order is shallow-first so
// parents precede children. The .axon system subdirs hold runtime state.
var dirs = []string{
	"00-Inbox",
	"01-Projects",
	"02-Areas",
	"03-Resources",
	"03-Resources/Knowledge",
	"04-Archive",
	"Daily",
	"MOCs",
	"Templates",
	".axon",
	".axon/logs",
	".axon/exports",
	".axon/snapshots",
	".axon/dashboards",
}

// fileSpec maps an embedded asset to its destination path in the vault.
type fileSpec struct {
	dest  string // vault-relative destination
	asset string // path under assets/
}

// files are the README and template notes seeded into the vault. Non-system
// folders each get a README; Templates/ gets the five note templates.
var files = []fileSpec{
	{"00-Inbox/README.md", "assets/readmes/inbox.md"},
	{"01-Projects/README.md", "assets/readmes/projects.md"},
	{"02-Areas/README.md", "assets/readmes/areas.md"},
	{"03-Resources/README.md", "assets/readmes/resources.md"},
	{"03-Resources/Knowledge/README.md", "assets/readmes/knowledge.md"},
	{"04-Archive/README.md", "assets/readmes/archive.md"},
	{"Daily/README.md", "assets/readmes/daily.md"},
	{"MOCs/README.md", "assets/readmes/mocs.md"},
	{"Templates/README.md", "assets/readmes/templates.md"},
	{"Templates/Daily Note.md", "assets/templates/daily-note.md"},
	{"Templates/Atomic Note.md", "assets/templates/atomic-note.md"},
	{"Templates/Project.md", "assets/templates/project.md"},
	{"Templates/Knowledge Source.md", "assets/templates/knowledge-source.md"},
	{"Templates/MOC.md", "assets/templates/moc.md"},
}

// Result reports what the scaffold did, for the init summary and idempotency
// checks. CreatedDirs/CreatedFiles list newly made items; the Skipped* counts
// cover items that already existed.
type Result struct {
	CreatedDirs  []string
	CreatedFiles []string
	SkippedDirs  int
	SkippedFiles int
}

// Changed reports whether the scaffold created anything (false => "no changes").
func (r Result) Changed() bool {
	return len(r.CreatedDirs) > 0 || len(r.CreatedFiles) > 0
}

// dashboardFiles are the in-vault Dataview dashboards (init step 8), written to
// .axon/dashboards/.
var dashboardFiles = []fileSpec{
	{".axon/dashboards/Inbox & Triage.md", "assets/dashboards/inbox-triage.md"},
	{".axon/dashboards/Active Projects.md", "assets/dashboards/active-projects.md"},
	{".axon/dashboards/Recent Knowledge.md", "assets/dashboards/recent-knowledge.md"},
	{".axon/dashboards/Link Suggestions.md", "assets/dashboards/link-suggestions.md"},
}

// Dashboards writes the generated in-vault Dataview dashboard notes (init step
// 8), idempotently and without clobbering user edits.
func Dashboards(v *vault.FS) (Result, error) {
	var res Result
	for _, f := range dashboardFiles {
		content, err := assets.ReadFile(f.asset)
		if err != nil {
			return res, fmt.Errorf("read dashboard asset %q: %w", f.asset, err)
		}
		created, err := v.Create(f.dest, string(content))
		if err != nil {
			return res, fmt.Errorf("write dashboard %q: %w", f.dest, err)
		}
		if created {
			res.CreatedFiles = append(res.CreatedFiles, f.dest)
		} else {
			res.SkippedFiles++
		}
	}
	return res, nil
}

// Apply ensures the vault layout exists. It is safe to run repeatedly: existing
// directories and files are left exactly as they are. Returns a Result
// describing what changed.
func Apply(v *vault.FS) (Result, error) {
	var res Result

	for _, d := range dirs {
		created, err := v.EnsureDir(d)
		if err != nil {
			return res, fmt.Errorf("scaffold dir %q: %w", d, err)
		}
		if created {
			res.CreatedDirs = append(res.CreatedDirs, d)
		} else {
			res.SkippedDirs++
		}
	}

	for _, f := range files {
		content, err := assets.ReadFile(f.asset)
		if err != nil {
			return res, fmt.Errorf("read embedded asset %q: %w", f.asset, err)
		}
		created, err := v.Create(f.dest, string(content))
		if err != nil {
			return res, fmt.Errorf("scaffold file %q: %w", f.dest, err)
		}
		if created {
			res.CreatedFiles = append(res.CreatedFiles, f.dest)
		} else {
			res.SkippedFiles++
		}
	}

	return res, nil
}
