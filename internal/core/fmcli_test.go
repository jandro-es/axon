package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The real machine's license refusal: 24-bit ANSI colour, and lying exit
// codes (`fm available` exits 0 while refusing).
const fmLicenseOutput = "\x1b[38;2;255;107;128mYOU HAVE NOT AGREED TO THE APPLE FOUNDATION MODELS CLI LEGAL NOTICE & TERMS.\n" +
	"Agreeing to the Apple Foundation Models CLI Legal Notice & Terms applies to every user on the machine, so it must be run as a privileged user (e.g. 'sudo fm license').\n\x1b[0m"

func fmDet(goos, goarch string, mut func(*fmDetector)) fmDetector {
	d := fmDetector{
		goos:      goos,
		goarch:    goarch,
		lookPath:  func(string) (string, error) { return "/usr/bin/fm", nil },
		osVersion: func(context.Context) (string, error) { return "27.0", nil },
		runFM: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Model availability:\n  on-device: available\n"), nil
		},
	}
	if mut != nil {
		mut(&d)
	}
	return d
}

func TestDetectFMStates(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		det  fmDetector
		want FMState
	}{
		{"non-mac", fmDet("linux", "amd64", nil), FMStateNotMac},
		{"os too old", fmDet("darwin", "arm64", func(d *fmDetector) {
			d.osVersion = func(context.Context) (string, error) { return "26.6", nil }
		}), FMStateOSTooOld},
		{"binary absent", fmDet("darwin", "arm64", func(d *fmDetector) {
			d.lookPath = func(string) (string, error) { return "", errors.New("not found") }
		}), FMStateAbsent},
		{"license pending, exit 0", fmDet("darwin", "arm64", func(d *fmDetector) {
			d.runFM = func(context.Context, string, ...string) ([]byte, error) { return []byte(fmLicenseOutput), nil }
		}), FMStateLicensePending},
		// Marker beats error: fm's exit codes are inconsistent.
		{"license pending, nonzero exit", fmDet("darwin", "arm64", func(d *fmDetector) {
			d.runFM = func(context.Context, string, ...string) ([]byte, error) {
				return []byte(fmLicenseOutput), errors.New("exit status 1")
			}
		}), FMStateLicensePending},
		{"ready", fmDet("darwin", "arm64", nil), FMStateReady},
		{"unresponsive", fmDet("darwin", "arm64", func(d *fmDetector) {
			d.runFM = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("timeout") }
		}), FMStateUnresponsive},
		// sw_vers failing must not block detection — probe anyway.
		{"version unknown still probes", fmDet("darwin", "arm64", func(d *fmDetector) {
			d.osVersion = func(context.Context) (string, error) { return "", errors.New("boom") }
		}), FMStateReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.det.detect(ctx)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q (detail: %s)", got.State, tc.want, got.Detail)
			}
			if strings.Contains(got.Detail, "\x1b") {
				t.Errorf("detail carries raw ANSI: %q", got.Detail)
			}
		})
	}
}

func TestDetectFMFields(t *testing.T) {
	ctx := context.Background()

	st := fmDet("darwin", "arm64", nil).detect(ctx)
	if !st.AppleSilicon {
		t.Error("arm64 must report AppleSilicon")
	}
	if st.OSVersion != "27.0" {
		t.Errorf("OSVersion = %q", st.OSVersion)
	}
	if !strings.Contains(st.Detail, "Model availability") || !strings.Contains(st.Detail, "on-device: available") {
		t.Errorf("ready detail should summarise fm available output, got %q", st.Detail)
	}

	st = fmDet("darwin", "amd64", func(d *fmDetector) {
		d.osVersion = func(context.Context) (string, error) { return "26.6", nil }
	}).detect(ctx)
	if st.AppleSilicon {
		t.Error("amd64 must not report AppleSilicon")
	}
	if !strings.Contains(st.Detail, "26.6") {
		t.Errorf("os-too-old detail should name the version, got %q", st.Detail)
	}

	// Persisted output is capped.
	huge := strings.Repeat("x", 5000)
	st = fmDet("darwin", "arm64", func(d *fmDetector) {
		d.runFM = func(context.Context, string, ...string) ([]byte, error) { return []byte(huge), nil }
	}).detect(ctx)
	if len(st.Detail) > 400 {
		t.Errorf("detail not capped: %d bytes", len(st.Detail))
	}
}

func TestStripANSI(t *testing.T) {
	got := stripANSI(fmLicenseOutput)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("ANSI survived: %q", got)
	}
	if !strings.Contains(got, "YOU HAVE NOT AGREED") {
		t.Fatalf("text mangled: %q", got)
	}
}
