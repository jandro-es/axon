package actions

import "testing"

func TestExtract(t *testing.T) {
	body := "intro line\n" +
		"## Work\n" +
		"- [ ] first task 📅 2026-07-15\n" +
		"- [x] done task\n" +
		"```\n" +
		"- [ ] fenced not a task\n" +
		"```\n" +
		"## Home\n" +
		"- [ ] second task\n" +
		"<!-- axon:actions:start -->\n" +
		"- [ ] projection reference (must be skipped)\n" +
		"<!-- axon:actions:end -->\n" +
		"- [ ] third task\n"
	got := Extract("Daily/2026-07-10.md", body, false)
	if len(got) != 4 {
		t.Fatalf("got %d actions, want 4: %+v", len(got), got)
	}
	if got[0].Section != "Work" || got[0].SourcePath != "Daily/2026-07-10.md" {
		t.Errorf("action0 section/path = %q/%q", got[0].Section, got[0].SourcePath)
	}
	if got[3].Section != "Home" {
		t.Errorf("action3 section = %q want Home (third task after the skipped block)", got[3].Section)
	}
	for _, a := range got {
		if contains(a.Text, "fenced") || contains(a.Text, "projection") {
			t.Errorf("leaked a skipped line: %q", a.Text)
		}
	}
	// LineNo must be the real body index (used for ordering/display).
	if got[0].LineNo != 2 {
		t.Errorf("action0 LineNo = %d want 2", got[0].LineNo)
	}
}

func TestExtractArchivedFlag(t *testing.T) {
	got := Extract("04-Archive/old.md", "- [ ] archived task\n", true)
	if len(got) != 1 || !got[0].Archived {
		t.Fatalf("expected 1 archived action, got %+v", got)
	}
}
