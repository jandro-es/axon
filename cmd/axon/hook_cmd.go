package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/hooks"
	"github.com/jandro-es/axon/internal/tokens"
)

func newHookCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "hook <event>",
		Short: "Handle a Claude Code hook event (internal; invoked from settings.json)",
		Long: "Thin handler for Claude Code hooks (SessionStart, PreToolUse, PostToolUse,\n" +
			"Stop, SessionEnd). Reads the hook JSON on stdin and emits the decision/context on stdout.\n" +
			"Hooks tighten behaviour only and never make a model call.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			event := args[0]
			stdin, _ := io.ReadAll(cmd.InOrStdin())

			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				// A hook must never hard-fail the session; degrade quietly.
				return nil
			}
			defer deps.close()

			// Read-only manager (budget status); hooks make no model call.
			mgr := tokens.New(deps.db, nil, nil, nil, managerConfig(deps.name, deps.profile, deps.cfg))
			res, err := hooks.Handle(cmd.Context(), event, stdin, hooks.Deps{
				Profile: deps.name, DB: deps.db, Vault: deps.vault, Manager: mgr,
				Memory: deps.profile.Memory, Redaction: deps.profile.Policy.RedactionRules,
			})
			if err != nil {
				return nil // never break the session on a hook error
			}
			if len(res.Stdout) > 0 {
				_, _ = cmd.OutOrStdout().Write(res.Stdout)
			}
			if res.ExitCode != 0 {
				os.Exit(res.ExitCode)
			}
			return nil
		},
	}
}
