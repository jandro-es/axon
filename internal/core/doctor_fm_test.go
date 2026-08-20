package core

import (
	"strings"
	"testing"
)

func TestFMCheckMapping(t *testing.T) {
	for _, tc := range []struct {
		name       string
		st         FMStatus
		wantStatus CheckStatus
		wantFix    string
		wantIn     string
	}{
		{"os too old is informational", FMStatus{State: FMStateOSTooOld, Detail: "fm CLI requires macOS 27 (this is 26.6)"}, StatusOK, "", "26.6"},
		{"absent is informational", FMStatus{State: FMStateAbsent, Detail: "fm CLI not found on PATH"}, StatusOK, "", "not found"},
		{"license pending warns with fix", FMStatus{State: FMStateLicensePending, Detail: "terms have not been agreed"}, StatusWarn, "sudo fm license", "agreed"},
		{"ready is ok", FMStatus{State: FMStateReady, Detail: "Model availability; on-device: available"}, StatusOK, "", "on-device"},
		{"unresponsive is informational", FMStatus{State: FMStateUnresponsive, Detail: "fm did not answer: timeout"}, StatusOK, "", "did not answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fmCheckFrom(tc.st)
			if c.Name != "apple-fm" {
				t.Errorf("name = %q", c.Name)
			}
			if c.Status != tc.wantStatus {
				t.Errorf("status = %v, want %v", c.Status, tc.wantStatus)
			}
			if c.Fix != tc.wantFix {
				t.Errorf("fix = %q, want %q", c.Fix, tc.wantFix)
			}
			if !strings.Contains(c.Detail, tc.wantIn) {
				t.Errorf("detail %q should contain %q", c.Detail, tc.wantIn)
			}
		})
	}
}

func TestVisionCheckApple(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		st         FMStatus
		wantStatus CheckStatus
		wantFix    string
		wantIn     string
	}{
		{"ready on-device", "apple", FMStatus{State: FMStateReady}, StatusOK, "", "vision ready: apple"},
		{"ready pcc mentions gating", "apple:pcc", FMStatus{State: FMStateReady}, StatusOK, "", "context-gated"},
		{"licence pending warns with fix", "apple", FMStatus{State: FMStateLicensePending}, StatusWarn, "sudo fm license", "licence"},
		{"absent warns with fallback", "apple", FMStatus{State: FMStateAbsent, Detail: "fm CLI not found on PATH"}, StatusWarn, "", "OCR-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := visionCheckApple(tc.mode, tc.st)
			if c.Status != tc.wantStatus || c.Fix != tc.wantFix || !strings.Contains(c.Detail, tc.wantIn) {
				t.Fatalf("check = %+v", c)
			}
		})
	}
}
