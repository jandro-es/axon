package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	askpkg "github.com/jandro-es/axon/internal/ask"
	"github.com/jandro-es/axon/internal/ui"
)

func newAskCmd(gf *globalFlags) *cobra.Command {
	var topK int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Answer a question from your vault — grounded or silent, with [[wikilink]] citations",
		Long: "Retrieval-augmented answer over the vault + knowledge base. AXON answers ONLY\n" +
			"from retrieved notes and cites them as wikilinks; when nothing relevant is\n" +
			"retrieved it refuses without spending a single token (FR-108…FR-110).",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()
			svc := deps.buildServices(nil)

			question := strings.Join(args, " ")
			a, err := askpkg.Ask(cmd.Context(), askpkg.Deps{
				Searcher: svc.searcher, Manager: svc.manager, Config: deps.profile,
			}, question, topK)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(a)
			}
			sty := ui.For(out)
			if a.Refused {
				fmt.Fprintf(out, "%s %s%s\n", sty.Yellow("✗"), sty.Bold("no answer: "), a.Reason)
				if a.Reason == "budget" {
					fmt.Fprintf(out, "  %s\n", sty.Dim("check `axon status` — the day/week window is exhausted"))
				}
				if len(a.Sources) > 0 {
					fmt.Fprintf(out, "%s\n", sty.Dim("Retrieved (uncited):"))
					for _, s := range a.Sources {
						fmt.Fprintf(out, "  - %s\n", s)
					}
				}
				return nil
			}
			fmt.Fprintln(out, a.Text)
			fmt.Fprintf(out, "\n%s\n", sty.Dim("Sources:"))
			for _, c := range a.Citations {
				fmt.Fprintf(out, "  - %s\n", sty.Cyan(c))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 0, "retrieval depth (default: retrieval.top_k)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the answer as JSON")
	return cmd
}
