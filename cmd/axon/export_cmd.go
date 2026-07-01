package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/ui"
)

func newExportCmd(gf *globalFlags) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write a portable, self-describing snapshot bundle (manifest + Markdown + JSON)",
		Long: "Export a plain-format snapshot of the profile's derived state: a manifest\n" +
			"(JSON), a core-context note (Markdown) and the recent activity log (JSON).\n" +
			"The vault itself is already portable Markdown and is referenced, not copied.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()
			ctx := cmd.Context()

			stamp := time.Now().UTC().Format("20060102-150405")
			dir := out
			if dir == "" {
				dir = filepath.Join(deps.paths.DataDir, "exports", stamp)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}

			stats, err := db.Stats(ctx, deps.db)
			if err != nil {
				return err
			}
			mgr := tokens.New(deps.db, nil, nil, nil, managerConfig(deps.name, deps.profile, deps.cfg))
			budget, _ := mgr.Status(ctx, deps.name)
			version, _ := db.SchemaVersion(deps.db)

			manifest := map[string]any{
				"axon_export_version": 1,
				"generated_at":        time.Now().UTC().Format(time.RFC3339),
				"profile":             deps.name,
				"auth_mode":           deps.profile.Claude.AuthMode,
				"vault_path":          deps.paths.VaultPath,
				"schema_version":      version,
				"stats": map[string]any{
					"notes": stats.Notes, "links": stats.Links, "words": stats.Words,
					"sources": stats.Sources, "inbox_backlog": stats.InboxBacklog,
				},
				"budget": map[string]any{
					"day_used": budget.Day.Used, "day_limit": budget.Day.Limit,
					"week_used": budget.Week.Used, "week_limit": budget.Week.Limit,
					"guard_paused": budget.GuardPaused,
				},
			}
			if err := writeJSONFile(filepath.Join(dir, "manifest.json"), manifest); err != nil {
				return err
			}

			context := fmt.Sprintf("# AXON context export — %s\n\nProfile: **%s** (auth: %s)\nVault: `%s`\n\n"+
				"## Snapshot\n- Notes: %d\n- Links: %d\n- Words: %d\n- Ingested sources: %d\n- Inbox backlog: %d\n\n"+
				"## Budget\n- Day: %d / %d tokens\n- Week: %d / %d tokens\n\n"+
				"The vault is the source of truth (plain Markdown at the path above); this\n"+
				"bundle is a derived, disposable snapshot.\n",
				stamp, deps.name, deps.profile.Claude.AuthMode, deps.paths.VaultPath,
				stats.Notes, stats.Links, stats.Words, stats.Sources, stats.InboxBacklog,
				budget.Day.Used, budget.Day.Limit, budget.Week.Used, budget.Week.Limit)
			if err := os.WriteFile(filepath.Join(dir, "core-context.md"), []byte(context), 0o644); err != nil {
				return err
			}

			events, _ := db.RecentEvents(ctx, deps.db, 500)
			if err := writeJSONFile(filepath.Join(dir, "activity.json"), events); err != nil {
				return err
			}

			eout := cmd.OutOrStdout()
			est := ui.For(eout)
			fmt.Fprintf(eout, "%s exported snapshot to %s\n  %s\n",
				est.Green(ui.IconOK), est.Cyan(dir),
				est.Dim("manifest.json · core-context.md · activity.json"))
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "output directory (default: <data_dir>/exports/<timestamp>)")
	return cmd
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
