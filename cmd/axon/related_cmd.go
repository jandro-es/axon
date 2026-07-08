package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newRelatedCmd(gf *globalFlags) *cobra.Command {
	var topK int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "related <note-path>",
		Short: "Notes most similar to a given note — pure vector math, no model call (FR-148)",
		Long: "Surfaces the notes most related to <note-path> using the embeddings AXON\n" +
			"already has. Zero tokens, no Claude/Ollama call. <note-path> is a\n" +
			"vault-relative path, exactly like `axon read`/vault_read.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			s := deps.buildSearcher()
			related, err := s.Related(cmd.Context(), args[0], topK)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(related)
			}
			sty := ui.For(out)
			if len(related) == 0 {
				fmt.Fprintf(out, "%s %s\n", sty.Yellow(ui.IconSearch), sty.Dim(fmt.Sprintf("no related notes for %q", args[0])))
				return nil
			}
			if tui.Interactive(out) {
				rows := make([][]string, 0, len(related))
				for i, r := range related {
					rows = append(rows, []string{fmt.Sprintf("%d", i+1), r.Path, fmt.Sprintf("%.3f", r.Similarity)})
				}
				fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf("%d note(s) related to %q", len(related), args[0])))
				tui.Table(out, []string{"#", "PATH", "SIMILARITY"}, rows)
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf("%d note(s) related to %q", len(related), args[0])))
			for i, r := range related {
				fmt.Fprintf(out, "%s %s  %s\n",
					sty.Dim(fmt.Sprintf("%d.", i+1)),
					sty.Bold(sty.Cyan(r.Path)),
					sty.Dim(fmt.Sprintf("(sim %.3f)", r.Similarity)))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 0, "number of related notes (default 10)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit results as JSON")
	return cmd
}
