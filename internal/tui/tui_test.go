package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestInteractiveFalseForBuffers(t *testing.T) {
	if Interactive(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer must never be interactive")
	}
}

func TestInteractiveFalseUnderCI(t *testing.T) {
	t.Setenv("CI", "true")
	if Interactive(&bytes.Buffer{}) {
		t.Error("CI must force non-interactive")
	}
}

func TestStepsPlainFallback(t *testing.T) {
	var out bytes.Buffer
	var got []string
	s := NewSteps(&out, "provisioning", func(name, detail string, st StepStatus) {
		got = append(got, name+"/"+string(st)+"/"+detail)
	})
	s.Start()
	s.Set("db", "creating", StatusRunning)
	s.Set("db", "created", StatusDone)
	if err := s.Finish("all good"); err != nil {
		t.Fatal(err)
	}
	// Plain mode forwards only terminal states to the fallback printer, so
	// non-TTY output stays exactly what commands print today.
	if len(got) != 1 || got[0] != "db/done/created" {
		t.Errorf("plain fallback rows = %v, want [db/done/created]", got)
	}
	if !strings.Contains(out.String(), "all good") {
		t.Errorf("summary missing: %q", out.String())
	}
}

func TestStepsPlainNilPrinterIsSafe(t *testing.T) {
	var out bytes.Buffer
	s := NewSteps(&out, "t", nil)
	s.Start()
	s.Set("x", "d", StatusWarn)
	if err := s.Finish("done"); err != nil {
		t.Fatal(err)
	}
}

func TestSpinPlainRunsFn(t *testing.T) {
	var out bytes.Buffer
	ran := false
	err := Spin(&out, "reindexing", func() (string, error) { ran = true; return "14 notes", nil })
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if !strings.Contains(out.String(), "14 notes") {
		t.Errorf("summary missing: %q", out.String())
	}
}

func TestSpinPlainPropagatesError(t *testing.T) {
	var out bytes.Buffer
	err := Spin(&out, "working", func() (string, error) { return "", errors.New("boom") })
	if err == nil || err.Error() != "boom" {
		t.Errorf("Spin must return fn's error verbatim, got %v", err)
	}
}

func TestTablePlain(t *testing.T) {
	var out bytes.Buffer
	Table(&out, []string{"NAME", "STATUS"}, [][]string{{"heartbeat", "ok"}, {"daily-log", "skipped"}})
	s := out.String()
	for _, want := range []string{"NAME", "heartbeat", "skipped"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q:\n%s", want, s)
		}
	}
}

func TestConfirmNonInteractiveReturnsDefault(t *testing.T) {
	var out bytes.Buffer
	if !Confirm(&out, strings.NewReader(""), "proceed?", true) {
		t.Error("non-interactive Confirm must return defaultYes=true")
	}
	if Confirm(&out, strings.NewReader(""), "proceed?", false) {
		t.Error("non-interactive Confirm must return defaultYes=false")
	}
	if TypedConfirm(&out, strings.NewReader(""), "delete everything?", "delete") {
		t.Error("non-interactive TypedConfirm must refuse")
	}
}

func TestSelectAndInputNonInteractive(t *testing.T) {
	var out bytes.Buffer
	if _, err := Select(&out, strings.NewReader(""), "pick", []Option{{Label: "A", Value: "a"}}); err == nil {
		t.Error("non-interactive Select must error, never hang")
	}
	got, err := Input(&out, strings.NewReader(""), "model?", "nomic-embed-text", "nomic-embed-text")
	if err != nil || got != "nomic-embed-text" {
		t.Errorf("non-interactive Input must return the default; got %q err=%v", got, err)
	}
}
