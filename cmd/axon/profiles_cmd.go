package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
)

// profileView is the isolation-relevant, secret-free summary of a profile.
type profileView struct {
	Name               string   `json:"name"`
	Active             bool     `json:"active"`
	AuthMode           string   `json:"auth_mode"`
	VaultPath          string   `json:"vault_path"`
	DataDir            string   `json:"data_dir"`
	DBPath             string   `json:"db_path"`
	ConfigDir          string   `json:"config_dir"`
	OAuthTokenRef      string   `json:"oauth_token_ref"` // the env:NAME reference, NEVER the secret
	AllowedAutomations []string `json:"allowed_automations"`
}

func newProfilesCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "List configured profiles and their isolated resolved paths (no secrets)",
		Long: "Show each profile's isolation surface — vault, data dir, db, CLAUDE_CONFIG_DIR,\n" +
			"OAuth-token reference and policy — so you can verify by inspection that personal\n" +
			"and work share no data, secrets or Claude account (S7/NFR-04).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			active := cfg.ResolveProfileName(gf.profile)

			names := make([]string, 0, len(cfg.Profiles))
			for n := range cfg.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)

			views := make([]profileView, 0, len(names))
			for _, n := range names {
				p := cfg.Profiles[n]
				paths := p.Paths()
				views = append(views, profileView{
					Name: n, Active: n == active, AuthMode: p.Claude.AuthMode,
					VaultPath: paths.VaultPath, DataDir: paths.DataDir, DBPath: paths.DBPath,
					ConfigDir: paths.ConfigDir, OAuthTokenRef: p.Claude.OAuthToken,
					AllowedAutomations: p.Policy.AllowedAutomations,
				})
			}

			w := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(views)
			}
			for _, v := range views {
				marker := "  "
				if v.Active {
					marker = "▸ "
				}
				fmt.Fprintf(w, "%s%s (auth: %s)\n", marker, v.Name, v.AuthMode)
				fmt.Fprintf(w, "    vault:      %s\n", v.VaultPath)
				fmt.Fprintf(w, "    data dir:   %s\n", v.DataDir)
				fmt.Fprintf(w, "    config dir: %s\n", v.ConfigDir)
				fmt.Fprintf(w, "    oauth:      %s\n", orNone(v.OAuthTokenRef))
				fmt.Fprintf(w, "    automations:%s\n", fmtAllow(v.AllowedAutomations))
			}
			fmt.Fprintln(w, "\nProfiles are separate installations; one is active per machine. No data,")
			fmt.Fprintln(w, "secrets or Claude account is shared across them (NFR-04).")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit profiles as JSON")
	return cmd
}

func orNone(s string) string {
	if s == "" {
		return "(none — interactive login only)"
	}
	return s
}

func fmtAllow(a []string) string {
	if len(a) == 0 {
		return " (all)"
	}
	out := ""
	for _, x := range a {
		out += " " + x
	}
	return out
}
