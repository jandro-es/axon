package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ui"
)

func newConfigCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect, get/set and validate configuration",
	}
	cmd.AddCommand(newConfigValidateCmd(gf), newConfigGetCmd(gf), newConfigSetCmd(gf))
	return cmd
}

// jsonPathFor maps a user dotted key to a goccy YAML JSONPath. Top-level keys
// (version, project_name, active_profile, profiles, prices) address the document
// root; any other key is resolved relative to the active profile, so the
// ergonomic `limits.daily_tokens` means `profiles.<active>.limits.daily_tokens`.
func jsonPathFor(activeProfile, key string) string {
	key = strings.TrimSpace(key)
	head := key
	if i := strings.IndexByte(key, '.'); i >= 0 {
		head = key[:i]
	}
	topLevel := map[string]bool{
		"version": true, "project_name": true, "active_profile": true,
		"profiles": true, "prices": true,
	}
	if topLevel[head] {
		return "$." + key
	}
	return "$.profiles." + activeProfile + "." + key
}

func newConfigGetCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Read a config value by dotted key (profile-relative, e.g. limits.daily_tokens)",
		Long: "Read a value from the config by dotted key. Keys are resolved relative to\n" +
			"the active profile unless they start with a top-level key (version,\n" +
			"project_name, active_profile, profiles, prices). Examples:\n" +
			"  axon config get limits.daily_tokens\n" +
			"  axon config get models.synthesis\n" +
			"  axon config get active_profile",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(gf.configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Parse(raw)
			if err != nil {
				return err
			}
			name := cfg.ResolveProfileName(gf.profile)
			path, err := yaml.PathString(jsonPathFor(name, args[0]))
			if err != nil {
				return fmt.Errorf("invalid key %q: %w", args[0], err)
			}
			var v any
			if err := path.Read(bytes.NewReader(raw), &v); err != nil {
				return fmt.Errorf("key %q not found", args[0])
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"key": args[0], "value": v})
			}
			fmt.Fprintln(out, formatScalar(v))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the value as JSON")
	return cmd
}

func newConfigSetCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set an existing config value by dotted key (comment-preserving, then re-validated)",
		Long: "Set a value in the config by dotted key (resolved like `config get`). The\n" +
			"edit preserves the file's comments and formatting, and the result is\n" +
			"re-validated before it is written — an invalid change is refused. Only\n" +
			"existing keys may be set. Examples:\n" +
			"  axon config set limits.daily_tokens 2000000\n" +
			"  axon config set models.synthesis claude-opus-4-8",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			raw, err := os.ReadFile(gf.configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Parse(raw)
			if err != nil {
				return err
			}
			name := cfg.ResolveProfileName(gf.profile)

			file, err := parser.ParseBytes(raw, parser.ParseComments)
			if err != nil {
				return fmt.Errorf("parse config: %w", err)
			}
			path, err := yaml.PathString(jsonPathFor(name, key))
			if err != nil {
				return fmt.Errorf("invalid key %q: %w", key, err)
			}
			// Confirm the key exists (set only modifies existing keys).
			if _, rerr := path.ReadNode(bytes.NewReader(raw)); rerr != nil {
				return fmt.Errorf("key %q not found; `config set` only updates existing keys", key)
			}
			if err := path.ReplaceWithReader(file, strings.NewReader(yamlScalar(value))); err != nil {
				return fmt.Errorf("set %q: %w", key, err)
			}

			updated := []byte(file.String())
			if _, err := config.Parse(updated); err != nil {
				return fmt.Errorf("refusing to write: the change makes the config invalid: %w", err)
			}
			if err := writeFileAtomic(gf.configPath, updated); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"key": key, "value": value, "ok": true})
			}
			st := ui.For(out)
			fmt.Fprintf(out, "%s set %s = %s\n", st.Green(ui.IconOK), st.Bold(key), st.Cyan(value))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit a machine-readable result")
	return cmd
}

// yamlScalar renders a CLI string value as a YAML scalar source: bare for
// int/float/bool, quoted otherwise, so types round-trip correctly.
func yamlScalar(value string) string {
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return value
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return value
	}
	if value == "true" || value == "false" {
		return value
	}
	return strconv.Quote(value)
}

// formatScalar renders a read value for human output.
func formatScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		return string(b)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// writeFileAtomic writes data to path via a temp file + rename.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".axon-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func newConfigValidateCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the config file and the active profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			name, _, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			st := ui.For(out)
			fmt.Fprintf(out, "%s %s\n",
				st.Green(ui.IconOK),
				st.Green(fmt.Sprintf("OK: %s is valid (%d profile(s); active profile %q)", gf.configPath, len(cfg.Profiles), name)))
			return nil
		},
	}
}
