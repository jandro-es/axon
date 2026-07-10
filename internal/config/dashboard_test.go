package config

import "testing"

func TestAskAllowedDefaultsOn(t *testing.T) {
	var d DashboardConfig // AskEnabled nil
	if !d.AskAllowed() {
		t.Fatal("AskAllowed() = false with nil pointer, want true (default-ON)")
	}
	f := false
	d.AskEnabled = &f
	if d.AskAllowed() {
		t.Fatal("AskAllowed() = true with *false, want false")
	}
}

func TestActionsAllowedDefaultsOn(t *testing.T) {
	var d DashboardConfig // ActionsEnabled nil
	if !d.ActionsAllowed() {
		t.Fatal("ActionsAllowed() = false with nil pointer, want true (default-ON)")
	}
	f := false
	d.ActionsEnabled = &f
	if d.ActionsAllowed() {
		t.Fatal("ActionsAllowed() = true with *false, want false")
	}
}

func TestCaptureAllowedDefaultsOn(t *testing.T) {
	var d DashboardConfig // CaptureEnabled nil
	if !d.CaptureAllowed() {
		t.Fatal("CaptureAllowed() = false with nil pointer, want true (default-ON)")
	}
	f := false
	d.CaptureEnabled = &f
	if d.CaptureAllowed() {
		t.Fatal("CaptureAllowed() = true with *false, want false")
	}
}
