package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/vault"
)

func newReindexCmd(gf *globalFlags) *cobra.Command {
	var embeddings bool
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the notes mirror + link graph from the vault",
		Long: "Reconstruct AXON's derived database state from the Markdown vault\n" +
			"(ADR-006): the vault is the source of truth, so deleting db.sqlite and\n" +
			"re-running reindex fully rebuilds the operational index.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			name, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			paths := profile.Paths()

			out := cmd.OutOrStdout()
			if embeddings {
				fmt.Fprintln(out, "note: --embeddings re-embed is not available until Phase 2; rebuilding link graph only")
			}

			sqlDB, err := db.Open(paths.DBPath)
			if err != nil {
				return err
			}
			defer sqlDB.Close()
			if _, err := db.Migrate(sqlDB); err != nil {
				return err
			}

			vfs := vault.NewFS(paths.VaultPath)
			res, err := core.Reindex(cmd.Context(), vfs, sqlDB)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "reindex (profile %q): %d notes, %d links, %d unresolved wikilinks\n",
				name, res.Notes, res.Links, res.BrokenWikilink)
			return nil
		},
	}
	cmd.Flags().BoolVar(&embeddings, "embeddings", false, "force a full re-embed (Phase 2; currently a no-op with a notice)")
	return cmd
}
