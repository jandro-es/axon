package actions

import (
	"strings"
	"testing"
)

func TestParseStates(t *testing.T) {
	cases := []struct {
		line  string
		ok    bool
		state State
		cbox  string
	}{
		{"- [ ] open task", true, StateOpen, " "},
		{"- [x] done task", true, StateDone, "x"},
		{"- [X] done upper", true, StateDone, "X"},
		{"- [-] cancelled", true, StateCancelled, "-"},
		{"- [/] in progress", true, StateOpen, "/"}, // unknown → open (tolerant)
		{"* [ ] star bullet", true, StateOpen, " "},
		{"+ [ ] plus bullet", true, StateOpen, " "},
		{"  - [ ] indented", true, StateOpen, " "},
		{"not a task", false, "", ""},
		{"- plain bullet", false, "", ""},
		{"# heading", false, "", ""},
	}
	for _, c := range cases {
		a, ok := Parse(c.line)
		if ok != c.ok {
			t.Fatalf("Parse(%q) ok=%v want %v", c.line, ok, c.ok)
		}
		if !ok {
			continue
		}
		if a.State != c.state || a.Checkbox != c.cbox {
			t.Errorf("Parse(%q) state=%q cbox=%q want %q/%q", c.line, a.State, a.Checkbox, c.state, c.cbox)
		}
	}
}

func TestParseFields(t *testing.T) {
	line := "- [ ] Email @office [[Acme]] about #contract 📅 2026-07-15 ⏳ 2026-07-12 🛫 2026-07-10 ⏫"
	a, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if a.Due != "2026-07-15" || a.Scheduled != "2026-07-12" || a.Start != "2026-07-10" {
		t.Errorf("dates: due=%q sched=%q start=%q", a.Due, a.Scheduled, a.Start)
	}
	if a.Priority != "high" {
		t.Errorf("priority=%q want high", a.Priority)
	}
	if len(a.Contexts) != 1 || a.Contexts[0] != "office" {
		t.Errorf("contexts=%v", a.Contexts)
	}
	if len(a.Tags) != 1 || a.Tags[0] != "contract" {
		t.Errorf("tags=%v", a.Tags)
	}
	if a.Project != "Acme" {
		t.Errorf("project=%q want Acme", a.Project)
	}
	if wantIn := "Email"; !contains(a.Text, wantIn) {
		t.Errorf("text=%q should contain %q", a.Text, wantIn)
	}
	if contains(a.Text, "📅") || contains(a.Text, "⏫") {
		t.Errorf("text=%q should have date/priority emoji stripped", a.Text)
	}
}

func TestParseDoneDateAndAlias(t *testing.T) {
	a, _ := Parse("- [x] ship it [[proj|Project X]] ✅ 2026-07-09")
	if a.DoneDate != "2026-07-09" {
		t.Errorf("done date=%q", a.DoneDate)
	}
	if a.Project != "proj" { // alias stripped
		t.Errorf("project=%q want proj", a.Project)
	}
}

func TestHashStateIndependentButContentSensitive(t *testing.T) {
	open, _ := Parse("- [ ] call Bob 📅 2026-07-15")
	open.SourcePath = "Daily/2026-07-10.md"
	done, _ := Parse("- [x] call Bob 📅 2026-07-15")
	done.SourcePath = "Daily/2026-07-10.md"
	if open.Hash() != done.Hash() {
		t.Error("hash must be state-independent ([ ] vs [x] equal)")
	}
	resched, _ := Parse("- [ ] call Bob 📅 2026-07-20")
	resched.SourcePath = "Daily/2026-07-10.md"
	if open.Hash() == resched.Hash() {
		t.Error("hash must change when the line content (due date) changes")
	}
	other := open
	other.SourcePath = "Daily/2026-07-11.md"
	if open.Hash() == other.Hash() {
		t.Error("hash must incorporate source path")
	}
}

func TestParsePathological(t *testing.T) {
	// Must never panic: emoji-dense, huge, no fields.
	Parse("- [ ] " + repeat("📅⏫🔺x@a #b [[c]] ", 500))
	Parse("- [ ]")  // no space after bracket → not a task (no body)
	Parse("- [ ] ") // empty body
}

// tiny local helpers so the test file reads self-documenting.
func contains(s, sub string) bool   { return strings.Contains(s, sub) }
func repeat(s string, n int) string { return strings.Repeat(s, n) }
