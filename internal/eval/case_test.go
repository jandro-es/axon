package eval

import (
	"testing"
	"testing/fstest"
)

func TestLoadCasesAllFamilies(t *testing.T) {
	cases, err := LoadCases("all")
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) < 3 {
		t.Fatalf("want >=3 embedded cases, got %d", len(cases))
	}
	var classify, routine int
	for _, c := range cases {
		if c.Prompt == "" {
			t.Errorf("case %q has empty prompt", c.Name)
		}
		switch c.Family {
		case FamilyClassify:
			classify++
			if len(c.Grade.ExpectJSON) == 0 && c.Grade.ExpectText == "" {
				t.Errorf("classify case %q has no deterministic expectation", c.Name)
			}
		case FamilyRoutine:
			routine++
			if len(c.Grade.MustInclude) == 0 {
				t.Errorf("routine case %q has no must_include gate", c.Name)
			}
		}
	}
	if classify == 0 || routine == 0 {
		t.Fatalf("want both families present, got classify=%d routine=%d", classify, routine)
	}
}

func TestLoadCasesFilter(t *testing.T) {
	cases, err := LoadCases("classify")
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	for _, c := range cases {
		if c.Family != FamilyClassify {
			t.Errorf("filter classify returned %q family %q", c.Name, c.Family)
		}
	}
}

func TestLoadCasesRejectsMalformed(t *testing.T) {
	bad := fstest.MapFS{
		"bad/classify/x.yaml": &fstest.MapFile{
			Data: []byte("name: x\nfamily: classify\nprompt: hi\ngrade: {}\n"),
		},
	}
	_, err := loadCasesFS(bad, "bad", "all")
	if err == nil {
		t.Fatal("a classify fixture with no expectation must fail to load")
	}
}
