package eval

import "testing"

func TestGradeClassifyJSONSemantic(t *testing.T) {
	c := Case{Family: FamilyClassify, Grade: Grade{ExpectJSON: `{"kind":"article"}`}}
	// Key order and whitespace differ but the JSON is semantically equal.
	if v := gradeClassify(c, "{ \"kind\" : \"article\" }"); !v.Pass {
		t.Fatalf("semantically-equal JSON should pass: %+v", v)
	}
	if v := gradeClassify(c, `{"kind":"note"}`); v.Pass {
		t.Fatal("different value must fail")
	}
	if v := gradeClassify(c, "not json"); v.Pass {
		t.Fatal("non-JSON candidate must fail, not panic")
	}
}

func TestGradeClassifyTextNormalized(t *testing.T) {
	c := Case{Family: FamilyClassify, Grade: Grade{ExpectText: "high"}}
	if v := gradeClassify(c, "  HIGH\n"); !v.Pass {
		t.Fatalf("normalized text should pass: %+v", v)
	}
	if v := gradeClassify(c, "low"); v.Pass {
		t.Fatal("wrong text must fail")
	}
}

func TestMustIncludeGate(t *testing.T) {
	ok, missing := mustInclude([]string{"Alice", "Bob"}, "Alice shipped it; Bob is blocked")
	if !ok {
		t.Fatalf("all anchors present should pass, missing=%q", missing)
	}
	ok, missing = mustInclude([]string{"Alice", "Carol"}, "Alice shipped it")
	if ok || missing != "Carol" {
		t.Fatalf("want fail on missing Carol, got ok=%v missing=%q", ok, missing)
	}
}

func TestParseJudge(t *testing.T) {
	pass, reason, err := parseJudge(`{"pass":true,"reason":"looks good"}`)
	if err != nil || !pass || reason != "looks good" {
		t.Fatalf("parseJudge good: pass=%v reason=%q err=%v", pass, reason, err)
	}
	// Judges sometimes wrap JSON in prose/fences; extract the object.
	pass, _, err = parseJudge("Sure!\n```json\n{\"pass\":false,\"reason\":\"missing Bob\"}\n```")
	if err != nil || pass {
		t.Fatalf("parseJudge fenced: pass=%v err=%v", pass, err)
	}
	if _, _, err := parseJudge("no json here"); err == nil {
		t.Fatal("malformed judge output must return an error, not panic")
	}
}
