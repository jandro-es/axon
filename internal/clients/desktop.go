// Package clients wires the AXON MCP server into external Claude clients beyond
// Claude Code — currently Claude Desktop (Component 13, FR-74…FR-76). AXON ships
// one standard stdio MCP server (`axon mcp`); every client launches the same
// server, so wiring a client is just registering an identical `mcpServers` entry
// in that client's config. This package owns the OS-specific config-path
// resolution and a NON-DESTRUCTIVE merge: it adds/updates only AXON's own entry
// and never disturbs other servers or unknown keys.
//
// It deliberately knows nothing about the vault or the token manager — it only
// writes a launch spec. Vault safety still lives in the server (every AXON tool
// is wikilink-safe), so it holds regardless of which client connects.
package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ServerName is the key AXON registers under in a client's mcpServers map. It
// matches the key `axon init` writes into Claude Code's project .mcp.json, so the
// two clients reference the same logical server.
const ServerName = "axon"

// Params describe how a client should launch the AXON MCP server for a profile.
// They mirror the Claude Code wiring (claudeassets.Params) so both clients get
// an identical, profile-isolated launch spec (NFR-04).
type Params struct {
	Profile    string // active profile name (becomes --profile)
	Binary     string // absolute path to the axon binary
	ConfigPath string // absolute path to the config file (config.yaml)
	ConfigDir  string // CLAUDE_CONFIG_DIR (profile-isolated auth)
	AxonHome   string // AXON_HOME
}

func (p Params) env() map[string]string {
	env := map[string]string{}
	if p.ConfigDir != "" {
		env["CLAUDE_CONFIG_DIR"] = p.ConfigDir
	}
	if p.AxonHome != "" {
		env["AXON_HOME"] = p.AxonHome
	}
	return env
}

// entry is the mcpServers["axon"] value: command + args + (optional) env.
func (p Params) entry() map[string]any {
	e := map[string]any{
		"command": p.Binary,
		"args":    []any{"mcp", "--config", p.ConfigPath, "--profile", p.Profile},
	}
	if env := p.env(); len(env) > 0 {
		e["env"] = env
	}
	return e
}

// AxonEntry is the AXON server's mcpServers value (exported for callers that
// build a combined registration, e.g. AXON + an interop backend).
func (p Params) AxonEntry() map[string]any { return p.entry() }

// PrintJSON renders the `{ "mcpServers": { "axon": … } }` block for manual
// pasting (the `--print` path). It writes nothing.
func (p Params) PrintJSON() string {
	return PrintJSON(map[string]any{ServerName: p.entry()})
}

// PrintJSON renders a `{ "mcpServers": { … } }` block for manual pasting.
func PrintJSON(servers map[string]any) string {
	doc := map[string]any{"mcpServers": servers}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b) + "\n"
}

// DesktopConfigPath returns the OS-specific Claude Desktop config file:
//   - macOS:   ~/Library/Application Support/Claude/claude_desktop_config.json
//   - Windows: %APPDATA%/Claude/claude_desktop_config.json
//   - Linux:   ~/.config/Claude/claude_desktop_config.json
//
// All three are `os.UserConfigDir()/Claude/claude_desktop_config.json`, so the
// path logic lives here in one place (as with the hook/`claude -p` schemas).
func DesktopConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "Claude", "claude_desktop_config.json"), nil
}

// InstallResult reports what the merge did, for the CLI summary.
type InstallResult struct {
	Path   string
	Action string // created | added | updated | unchanged
}

// Entry builds a generic mcpServers entry (command + args + optional env), used
// for interop servers such as a community Obsidian MCP backend (FR-54).
func Entry(command string, args []string, env map[string]string) map[string]any {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	e := map[string]any{"command": command, "args": anyArgs}
	if len(env) > 0 {
		e["env"] = env
	}
	return e
}

// InstallDesktop merges AXON's server entry into the Claude Desktop config at
// configPath. Convenience wrapper over InstallServer.
func InstallDesktop(configPath string, p Params) (InstallResult, error) {
	return InstallServer(configPath, ServerName, p.entry())
}

// InstallServer merges a single named server entry into a Claude client config
// (claude_desktop_config.json OR a project .mcp.json — same `mcpServers` shape),
// NON-DESTRUCTIVELY: it preserves every other server and unknown top-level key,
// touching only mcpServers[name]. A missing file is created; an unparseable
// existing file is refused (so a hand-edited config is never clobbered — the
// caller should fall back to `--print`).
func InstallServer(configPath, name string, entry map[string]any) (InstallResult, error) {
	res := InstallResult{Path: configPath}

	root := map[string]any{}
	existed := false
	switch data, err := os.ReadFile(configPath); {
	case err == nil:
		existed = true
		if len(bytes.TrimSpace(data)) > 0 {
			if uerr := json.Unmarshal(data, &root); uerr != nil {
				return res, fmt.Errorf("existing %s is not valid JSON; refusing to overwrite — use `--print` and merge by hand: %w", configPath, uerr)
			}
		}
	case os.IsNotExist(err):
		// new file
	default:
		return res, fmt.Errorf("read %s: %w", configPath, err)
	}

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	switch existing, ok := servers[name]; {
	case ok && jsonEqual(existing, entry):
		res.Action = "unchanged"
		return res, nil
	case ok:
		res.Action = "updated"
	case existed:
		res.Action = "added"
	default:
		res.Action = "created"
	}

	servers[name] = entry
	root["mcpServers"] = servers
	if err := writeJSONAtomic(configPath, root); err != nil {
		return res, err
	}
	return res, nil
}

// ClientStatus is the detection result for `axon doctor`.
type ClientStatus struct {
	ConfigPath string
	Present    bool   // the config file exists
	Registered bool   // AXON's entry is present
	Profile    string // the --profile the registered entry launches, if any
}

// DetectDesktop inspects the Claude Desktop config without modifying it.
func DetectDesktop(configPath string) (ClientStatus, error) {
	st := ClientStatus{ConfigPath: configPath}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	st.Present = true
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		return st, nil // unparseable → treat as not registered, don't error
	}
	servers, _ := root["mcpServers"].(map[string]any)
	entry, ok := servers[ServerName].(map[string]any)
	if !ok {
		return st, nil
	}
	st.Registered = true
	st.Profile = profileFromArgs(entry["args"])
	return st, nil
}

// profileFromArgs extracts the value following "--profile" in an args array.
func profileFromArgs(args any) string {
	list, ok := args.([]any)
	if !ok {
		return ""
	}
	for i, a := range list {
		if s, _ := a.(string); s == "--profile" && i+1 < len(list) {
			if v, _ := list[i+1].(string); v != "" {
				return v
			}
		}
	}
	return ""
}

// jsonEqual compares two values by their canonical JSON encoding (map keys are
// sorted by encoding/json), so an unchanged re-install is detected as a no-op.
func jsonEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

// writeJSONAtomic writes pretty-printed JSON via a temp file + rename, so a
// reader never sees a half-written config.
func writeJSONAtomic(path string, doc any) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir for %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".axon-client-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file for %q: %w", path, err)
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
		return fmt.Errorf("write %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit %q: %w", path, err)
	}
	cleanup = false
	return nil
}
