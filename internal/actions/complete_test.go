package actions

import (
	"strings"
	"testing"
)

func TestComplete(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
		want string // when ok
	}{
		{"- [ ] call bob", true, "- [x] call bob ✅ 2026-07-10"},
		{"- [/] in progress", true, "- [x] in progress ✅ 2026-07-10"},         // unknown-open marker
		{"  * [ ] indented star", true, "  * [x] indented star ✅ 2026-07-10"}, // preserves indent+bullet
		{"- [ ] has date 📅 2026-07-15", true, "- [x] has date 📅 2026-07-15 ✅ 2026-07-10"},
		{"- [x] already done", false, ""},
		{"- [X] already done", false, ""},
		{"- [-] cancelled", false, ""},
		{"not a task", false, ""},
	}
	for _, c := range cases {
		got, ok := Complete(c.line, "2026-07-10")
		if ok != c.ok {
			t.Fatalf("Complete(%q) ok=%v want %v", c.line, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("Complete(%q) = %q want %q", c.line, got, c.want)
		}
	}
}

func TestCompleteIdempotentTick(t *testing.T) {
	got, ok := Complete("- [ ] weird ✅ 2026-07-01", "2026-07-10")
	if !ok {
		t.Fatal("expected ok")
	}
	if strings.Count(got, "✅") != 1 {
		t.Errorf("must not double-stamp ✅: %q", got)
	}
	if !strings.HasPrefix(got, "- [x]") {
		t.Errorf("marker not flipped: %q", got)
	}
}

func TestBucketFieldsMatchesBucket(t *testing.T) {
	today := day("2026-07-10")
	a := Action{State: StateOpen, Due: "2026-07-09"}
	if BucketFields("open", "2026-07-09", "", "", nil, today) != "overdue" {
		t.Error("BucketFields overdue wrong")
	}
	if Bucket(a, today) != BucketFields(string(a.State), a.Due, a.Scheduled, a.Start, a.Tags, today) {
		t.Error("Bucket must delegate to BucketFields")
	}
}
