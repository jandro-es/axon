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

	"github.com/jandro-es/axon/internal/config"
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
	runFM        func(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error)
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

// detectFM is the package indirection doctor uses, stubbable in tests so
// machine-dependent fm state never decides a test outcome.
var detectFM = DetectFM

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
	stdout, stderr, runErr := d.runFM(probeCtx, bin, "available")
	outText := strings.TrimSpace(stripANSI(string(stdout)))
	errText := strings.TrimSpace(stripANSI(string(stderr)))

	// fm's exit codes are untrustworthy (observed on macOS 27.0: the licence
	// refusal exits 0; a real "System model available" answer exits 1 when a
	// PCC context error rides along on stderr). So: licence markers first,
	// then any non-empty stdout answer wins over the exit code, and only a
	// silent stdout with an error is unresponsive.
	if fmLicensePending(outText) || fmLicensePending(errText) {
		st.State = FMStateLicensePending
		st.Detail = "fm is installed but the Foundation Models CLI terms have not been agreed on this machine"
		return st
	}
	if outText != "" {
		st.State = FMStateReady
		st.Detail = capString(fmSummary(outText), fmDetailCap)
		return st
	}
	if runErr != nil {
		st.State = FMStateUnresponsive
		st.Detail = capString("fm errored: "+runErr.Error()+fmOutputSuffix(errText), fmDetailCap)
		return st
	}
	st.State = FMStateReady
	st.Detail = "fm answered with no output"
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

// fmCheck reports the fm CLI posture in doctor (FR-191). Advisory: only the
// license-pending state warns, because it is the one thing a user can act on.
// AXON never agrees to the terms itself — `fm license` is a privileged,
// machine-wide legal agreement. With PCC opted in (FR-195) a ready machine
// also carries the quota posture from `fm quota-usage`.
func fmCheck(p config.Profile) Check {
	st := DetectFM(context.Background())
	c := fmCheckFrom(st)
	if p.Models.PCCEnabled && st.State == FMStateReady {
		if q, ok := FMQuota(context.Background()); ok {
			c.Detail += " · quota: " + q
		}
	}
	return c
}

// FMQuota reports `fm quota-usage` (ANSI-stripped, capped) — advisory input
// for the PCC rung (FR-195). ok=false when fm is absent or silent.
func FMQuota(ctx context.Context) (string, bool) {
	d := defaultFMDetector()
	if d.goos != "darwin" {
		return "", false
	}
	bin, err := d.lookPath("fm")
	if err != nil {
		return "", false
	}
	qctx, cancel := context.WithTimeout(ctx, fmDetectTimeout)
	defer cancel()
	stdout, stderr, _ := d.runFM(qctx, bin, "quota-usage")
	text := strings.TrimSpace(stripANSI(string(stdout)))
	if text == "" {
		text = strings.TrimSpace(stripANSI(string(stderr)))
	}
	if text == "" {
		return "", false
	}
	return capString(fmSummary(text), fmDetailCap), true
}

// visionCheckApple maps the fm posture onto the vision check for the apple
// vision modes (FR-196/197). Advisory: warns keep the OCR-only fallback.
func visionCheckApple(mode string, st FMStatus) Check {
	const name = "vision"
	switch st.State {
	case FMStateReady:
		detail := "vision ready: " + mode + " (fm answering)"
		if mode == "apple:pcc" {
			detail += " — PCC is context-gated and quota-limited; a failed describe falls back to OCR-only"
		}
		return Check{Name: name, Status: StatusOK, Detail: detail}
	case FMStateLicensePending:
		return Check{Name: name, Status: StatusWarn, Detail: "vision " + mode + ": fm licence not agreed (images fall back to OCR-only)", Fix: "sudo fm license"}
	default:
		return Check{Name: name, Status: StatusWarn, Detail: "vision " + mode + " unavailable: " + st.Detail + " (images fall back to OCR-only)"}
	}
}

// fmCheckFrom is the pure status→check mapping (table-tested per state).
func fmCheckFrom(st FMStatus) Check {
	c := Check{Name: "apple-fm", Status: StatusOK, Detail: st.Detail}
	switch st.State {
	case FMStateLicensePending:
		c.Status = StatusWarn
		c.Fix = "sudo fm license"
	case FMStateReady:
		c.Detail = "fm ready: " + st.Detail
	}
	return c
}

// MacOSProductVersion reports the OS version (e.g. "27.0") — the drift key
// for fm-backed model tiers (FR-194): no digest exists, so an OS update is
// the re-eval event. ok=false off macOS or when sw_vers fails.
func MacOSProductVersion(ctx context.Context) (string, bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	v, err := swVersProductVersion(ctx)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(v), true
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
// subprocess probe. Streams stay separate: stdout carries the answer, stderr
// carries contextual errors (and either may carry the licence refusal).
func runFMCommand(ctx context.Context, bin string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
