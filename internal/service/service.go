// Package service generates OS service units that supervise the AXON daemon
// (`axon start`) per profile, without the core depending on any specific OS
// scheduler (ADR-008, FR-06). Units are profile-scoped and carry the profile's
// isolated config (CLAUDE_CONFIG_DIR, AXON_HOME), so personal and work installs
// never cross. The unit text is generated deterministically and testably; the
// CLI writes it to the platform's conventional location.
package service

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Params describe a profile's supervised daemon.
type Params struct {
	Profile    string
	Binary     string // absolute path to the axon binary
	ConfigPath string // absolute path to axon.config.yaml
	ConfigDir  string // CLAUDE_CONFIG_DIR (profile-isolated auth)
	AxonHome   string // AXON_HOME
	LogDir     string // where to write stdout/stderr
	HomeDir    string // user home, for resolving install paths
}

// Unit is a generated service unit and where/how to install it.
type Unit struct {
	Kind      string // launchd | systemd | windows
	Label     string // service/job name
	Path      string // install path for the unit file
	Content   string // the unit file contents
	EnableCmd string // command to load/enable the unit
	StartCmd  string // command to start it now
	StopCmd   string // command to stop it
}

// ForOS returns the service unit appropriate for goos (defaults to the host OS
// when goos is "").
func ForOS(goos string, p Params) (Unit, error) {
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "darwin":
		return LaunchdUnit(p), nil
	case "linux":
		return SystemdUnit(p), nil
	case "windows":
		return WindowsTask(p), nil
	default:
		return Unit{}, fmt.Errorf("service units are not supported on %q", goos)
	}
}

// label is the profile-scoped service identifier.
func (p Params) label() string { return "axon-" + p.Profile }

// args are the daemon start arguments shared by every unit.
func (p Params) startArgs() []string {
	return []string{"start", "--config", p.ConfigPath, "--profile", p.Profile}
}

// LaunchdUnit generates a macOS launchd LaunchAgent plist.
func LaunchdUnit(p Params) Unit {
	label := "com.axon." + p.Profile
	path := filepath.Join(p.HomeDir, "Library", "LaunchAgents", label+".plist")
	args := append([]string{p.Binary}, p.startArgs()...)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	fmt.Fprintf(&b, "  <key>Label</key>\n  <string>%s</string>\n", label)
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, a := range args {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	for _, kv := range p.env() {
		fmt.Fprintf(&b, "    <key>%s</key>\n    <string>%s</string>\n", kv[0], xmlEscape(kv[1]))
	}
	b.WriteString("  </dict>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	fmt.Fprintf(&b, "  <key>StandardOutPath</key>\n  <string>%s</string>\n", xmlEscape(filepath.Join(p.LogDir, "daemon.out.log")))
	fmt.Fprintf(&b, "  <key>StandardErrorPath</key>\n  <string>%s</string>\n", xmlEscape(filepath.Join(p.LogDir, "daemon.err.log")))
	b.WriteString("</dict>\n</plist>\n")

	return Unit{
		Kind:      "launchd",
		Label:     label,
		Path:      path,
		Content:   b.String(),
		EnableCmd: "launchctl load " + path,
		StartCmd:  "launchctl start " + label,
		StopCmd:   "launchctl unload " + path,
	}
}

// SystemdUnit generates a Linux systemd user service.
func SystemdUnit(p Params) Unit {
	label := p.label() + ".service"
	path := filepath.Join(p.HomeDir, ".config", "systemd", "user", label)
	exec := p.Binary + " " + strings.Join(p.startArgs(), " ")

	var b strings.Builder
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=AXON daemon (profile %s)\n", p.Profile)
	b.WriteString("After=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", exec)
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	for _, kv := range p.env() {
		fmt.Fprintf(&b, "Environment=%s=%s\n", kv[0], kv[1])
	}
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")

	return Unit{
		Kind:      "systemd",
		Label:     label,
		Path:      path,
		Content:   b.String(),
		EnableCmd: "systemctl --user enable " + label,
		StartCmd:  "systemctl --user start " + label,
		StopCmd:   "systemctl --user stop " + label,
	}
}

// WindowsTask generates a Windows Task Scheduler XML definition.
func WindowsTask(p Params) Unit {
	label := p.label()
	path := filepath.Join(p.HomeDir, label+".xml")
	args := strings.Join(p.startArgs(), " ")

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <Triggers>\n    <LogonTrigger>\n      <Enabled>true</Enabled>\n    </LogonTrigger>\n  </Triggers>\n")
	b.WriteString("  <Settings>\n    <RestartOnFailure>\n      <Interval>PT5M</Interval>\n      <Count>3</Count>\n    </RestartOnFailure>\n    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n  </Settings>\n")
	b.WriteString("  <Actions>\n    <Exec>\n")
	fmt.Fprintf(&b, "      <Command>%s</Command>\n      <Arguments>%s</Arguments>\n", xmlEscape(p.Binary), xmlEscape(args))
	b.WriteString("    </Exec>\n  </Actions>\n</Task>\n")

	return Unit{
		Kind:      "windows",
		Label:     label,
		Path:      path,
		Content:   b.String(),
		EnableCmd: fmt.Sprintf("schtasks /Create /TN %s /XML %s", label, path),
		StartCmd:  "schtasks /Run /TN " + label,
		StopCmd:   "schtasks /End /TN " + label,
	}
}

// env returns the profile-isolating environment for the unit, in a stable order
// so generated unit files are deterministic.
func (p Params) env() [][2]string {
	var env [][2]string
	if p.AxonHome != "" {
		env = append(env, [2]string{"AXON_HOME", p.AxonHome})
	}
	if p.ConfigDir != "" {
		env = append(env, [2]string{"CLAUDE_CONFIG_DIR", p.ConfigDir})
	}
	return env
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
