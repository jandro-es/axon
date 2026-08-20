package core

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// FMState classifies what the macOS 27 `fm` CLI probe found (FR-191). The
// matrix is advisory — nothing in AXON consumes `fm` before docs/21 M2 — but
// doctor reports it so a 27-ready machine, a license-pending one, and a
// too-old one are distinguishable at a glance.
type FMState string

const (
	FMStateNotMac         FMState = "not-macos"
	FMStateOSTooOld       FMState = "os-too-old"
	FMStateAbsent         FMState = "absent"
	FMStateLicensePending FMState = "license-pending"
	FMStateReady          FMState = "ready"
	FMStateUnresponsive   FMState = "unresponsive"
)

// FMStatus is the probe result. Detail is human-ready, ANSI-free, and capped —
// it may be persisted (doctor --json, dashboards).
type FMStatus struct {
	State        FMState
	Detail       string
	OSVersion    string
	AppleSilicon bool
}

// fmMinMacOSMajor is the first macOS release that ships the fm CLI.
const fmMinMacOSMajor = 27

// fmDetectTimeout bounds the probe: fm answers instantly when healthy, and
// doctor must never hang on a wedged binary (the dashboard-port probe rule).
const fmDetectTimeout = 3 * time.Second

// fmDetailCap bounds any raw fm output that lands in Detail.
const fmDetailCap = 300

// fmDetector carries the probe's seams; tests inject all three.
type fmDetector struct {
	goos, goarch string
	lookPath     func(file string) (string, error)
	osVersion    func(ctx context.Context) (string, error)
	runFM        func(ctx context.Context, bin string, args ...string) ([]byte, error)
}

func defaultFMDetector() fmDetector {
	return fmDetector{
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
		lookPath:  exec.LookPath,
		osVersion: swVersProductVersion,
		runFM:     runFMCommand,
	}
}

// DetectFM probes the machine for the macOS 27 Foundation Models CLI.
func DetectFM(ctx context.Context) FMStatus {
	return defaultFMDetector().detect(ctx)
}

func (d fmDetector) detect(ctx context.Context) FMStatus {
	st := FMStatus{AppleSilicon: d.goarch == "arm64"}
	if d.goos != "darwin" {
		st.State = FMStateNotMac
		st.Detail = "fm CLI is macOS-only (running on " + d.goos + ")"
		return st
	}

	// Best-effort version: a failed or unparseable sw_vers never blocks the
	// probe — the binary itself is the real signal.
	if v, err := d.osVersion(ctx); err == nil {
		st.OSVersion = strings.TrimSpace(v)
		if major, ok := macOSMajor(st.OSVersion); ok && major < fmMinMacOSMajor {
			st.State = FMStateOSTooOld
			st.Detail = "fm CLI requires macOS 27 (this is " + st.OSVersion + "); the on-device `apple` tier is unaffected"
			return st
		}
	}

	bin, err := d.lookPath("fm")
	if err != nil {
		st.State = FMStateAbsent
		st.Detail = "fm CLI not found on PATH"
		return st
	}

	probeCtx, cancel := context.WithTimeout(ctx, fmDetectTimeout)
	defer cancel()
	out, runErr := d.runFM(probeCtx, bin, "available")
	text := strings.TrimSpace(stripANSI(string(out)))

	// License markers are checked BEFORE the exec error: fm's exit codes are
	// inconsistent (observed on macOS 27.0: `fm available` exits 0 while
	// refusing; `fm --help` exits 1 with the same refusal).
	if fmLicensePending(text) {
		st.State = FMStateLicensePending
		st.Detail = "fm is installed but the Foundation Models CLI terms have not been agreed on this machine"
		return st
	}
	if runErr != nil {
		st.State = FMStateUnresponsive
		st.Detail = capString("fm did not answer: "+runErr.Error()+fmOutputSuffix(text), fmDetailCap)
		return st
	}
	st.State = FMStateReady
	st.Detail = capString(fmSummary(text), fmDetailCap)
	return st
}

// fmLicensePending reports whether stripped fm output is the machine-wide
// license refusal. Matched loosely: the format is beta and may drift.
func fmLicensePending(text string) bool {
	up := strings.ToUpper(text)
	return strings.Contains(up, "NOT AGREED") || strings.Contains(text, "fm license")
}

// fmSummary condenses `fm available` output to its first two non-empty lines —
// the format is undocumented beta, so no structure is assumed.
func fmSummary(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
		if len(lines) == 2 {
			break
		}
	}
	if len(lines) == 0 {
		return "fm answered with no output"
	}
	return strings.Join(lines, "; ")
}

func fmOutputSuffix(text string) string {
	if text == "" {
		return ""
	}
	return " — " + text
}

// macOSMajor parses "27.0" → 27. Tolerant: anything unparseable → !ok.
func macOSMajor(v string) (int, bool) {
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ansiRe matches CSI escape sequences, including the 24-bit colour forms fm
// emits (e.g. \x1b[38;2;255;107;128m).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func capString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
}

// swVersProductVersion asks the OS for its version (e.g. "27.0").
func swVersProductVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fmDetectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sw_vers", "-productVersion")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// runFMCommand executes fm with the WaitDelay guard used by every other
// subprocess probe. Combined output: fm styles its refusals on stdout.
func runFMCommand(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 2 * time.Second
	return cmd.CombinedOutput()
}
