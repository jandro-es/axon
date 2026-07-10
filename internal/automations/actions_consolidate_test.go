package automations

import (
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/db"
)

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestRenderActionsSections(t *testing.T) {
	today := day("2026-07-10")
	rows := []db.Action{
		{SourcePath: "01-Projects/work.md", Section: "Sprint", Text: "fix login", State: "open", Checkbox: " ", Due: "2000-01-01", Priority: "high", Project: "Auth"},
		{SourcePath: "Daily/2026-07-10.md", Text: "standup notes", State: "open", Checkbox: " ", Due: "2026-07-10"},
		{SourcePath: "01-Projects/work.md", Text: "write RFC", State: "open", Checkbox: " ", Due: "2026-07-14"}, // within 7d → This week
		{SourcePath: "01-Projects/work.md", Text: "refactor later", State: "open", Checkbox: " "},                // no date → Next actions
		{SourcePath: "01-Projects/work.md", Text: "hear from legal", State: "open", Checkbox: " ", Tags: []string{"waiting"}, Due: "2026-07-01"}, // waiting outranks overdue
		{SourcePath: "Ideas.md", Text: "learn rust", State: "open", Checkbox: " ", Tags: []string{"someday"}},
		{SourcePath: "01-Projects/work.md", Text: "ship v2", State: "done", Checkbox: "x", DoneDate: "2026-07-09"},       // done this week
		{SourcePath: "01-Projects/work.md", Text: "ancient done", State: "done", Checkbox: "x", DoneDate: "2026-01-01"}, // outside 7d window
		{SourcePath: "01-Projects/work.md", Text: "scrapped", State: "cancelled", Checkbox: "-"},                       // omitted
		{SourcePath: "04-Archive/old.md", Text: "archived", State: "open", Checkbox: " ", Archived: true},              // omitted
	}
	body, total := renderActionsSections(rows, today)

	// Open total excludes done/cancelled/archived: overdue1 + today1 + week1 + next1 + waiting1 + someday1 = 6.
	if total != 6 {
		t.Fatalf("open total = %d, want 6\n%s", total, body)
	}
	sec := func(h string) string { // slice of body under heading h up to the next "## "
		i := strings.Index(body, h)
		if i < 0 {
			t.Fatalf("missing section %q:\n%s", h, body)
		}
		rest := body[i+len(h):]
		if j := strings.Index(rest, "\n## "); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	if !strings.Contains(sec("## 📅 Today"), "standup notes") {
		t.Error("today section wrong")
	}
	if !strings.Contains(sec("## ⏳ This week"), "write RFC") {
		t.Error("this-week section wrong")
	}
	if !strings.Contains(sec("## ▶ Next actions"), "refactor later") {
		t.Error("next-actions section wrong")
	}
	if !strings.Contains(sec("## 🕓 Waiting for"), "hear from legal") {
		t.Error("waiting task (with due) must be in Waiting, not Overdue/This week")
	}
	if !strings.Contains(sec("## 💭 Someday"), "learn rust") {
		t.Error("someday section wrong")
	}
	if !strings.Contains(sec("## ✅ Done this week"), "ship v2") || strings.Contains(body, "ancient done") {
		t.Error("done-this-week window wrong")
	}
	if strings.Contains(body, "scrapped") || strings.Contains(body, "archived") {
		t.Error("cancelled/archived must be omitted")
	}
	// The overdue task lands under Overdue, in reference format (NOT a checkbox),
	// carrying [[source]] + due + priority glyph.
	if !strings.Contains(sec("## 🔴 Overdue"), "fix login") {
		t.Error("overdue task misfiled")
	}
	if !strings.Contains(body, "- fix login — [[01-Projects/work]]") || !strings.Contains(body, "📅 2000-01-01") || !strings.Contains(body, "⏫") {
		t.Errorf("reference line format wrong:\n%s", body)
	}
	if strings.Contains(body, "- [ ]") || strings.Contains(body, "- [x]") {
		t.Error("projection must contain NO checkboxes")
	}
}

func TestRenderActionsSectionsEmpty(t *testing.T) {
	body, total := renderActionsSections(nil, day("2026-07-10"))
	if total != 0 {
		t.Errorf("empty index open total = %d, want 0", total)
	}
	for _, h := range []string{"## 🔴 Overdue", "## 📅 Today", "## ⏳ This week", "## ▶ Next actions", "## 🕓 Waiting for", "## 💭 Someday / Maybe", "## ✅ Done this week"} {
		if !strings.Contains(body, h) {
			t.Errorf("missing section %q", h)
		}
	}
	if !strings.Contains(body, "_none_") {
		t.Error("empty sections should render _none_")
	}
}

func TestActionsNoteStub(t *testing.T) {
	s := actionsNoteStub()
	if !strings.Contains(s, "type: actions") || !strings.Contains(s, "never overwrites") || !strings.Contains(s, "axon:actions") {
		t.Errorf("stub missing frontmatter/preamble:\n%s", s)
	}
}
