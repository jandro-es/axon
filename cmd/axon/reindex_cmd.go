package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/ui"
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
			st := ui.For(out)

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
			fmt.Fprintf(out, "%s reindex (profile %q): %s notes, %s links, %s\n",
				st.Green(ui.IconOK), name,
				st.Bold(fmt.Sprintf("%d", res.Notes)),
				st.Bold(fmt.Sprintf("%d", res.Links)),
				st.Dim(fmt.Sprintf("%d unresolved wikilinks", res.BrokenWikilink)))

			if embeddings {
				embedder := embeddingsProvider(profile)
				re, err := core.ReembedPending(cmd.Context(), sqlDB, embedder, true)
				if err != nil {
					return fmt.Errorf("re-embed: %w (is Ollama running?)", err)
				}
				fmt.Fprintf(out, "%s re-embedded %d/%d chunks via %s\n", st.Green(ui.IconOK), re.Embedded, re.Total, st.Cyan(profile.Embeddings.Model))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&embeddings, "embeddings", false, "force a full re-embed (Phase 2; currently a no-op with a notice)")
	return cmd
}
