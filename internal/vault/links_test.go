package vault

import (
	"reflect"
	"testing"
)

func TestParseLinks(t *testing.T) {
	body := "See [[Alpha]] and [[02-Areas/Beta|the beta]] plus [[Gamma#Section]].\n" +
		"An embed ![[Delta]] and a tag #topic/sub. Same-file [[#heading]] is skipped.\n" +
		"Not a tag: C# and a markdown heading:\n# Heading\n"

	got := ParseLinks(body)
	want := []Link{
		{Target: "Alpha", Kind: KindWikilink},
		{Target: "02-Areas/Beta", Display: "the beta", Kind: KindWikilink},
		{Target: "Gamma", Heading: "Section", Kind: KindWikilink},
		{Target: "Delta", Kind: KindEmbed},
		{Target: "topic/sub", Kind: KindTag},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLinks mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestSplitWikilink(t *testing.T) {
	tests := []struct {
		in                       string
		target, heading, display string
	}{
		{"Note", "Note", "", ""},
		{"Note|Alias", "Note", "", "Alias"},
		{"Note#Head", "Note", "Head", ""},
		{"Note#Head|Alias", "Note", "Head", "Alias"},
		{"dir/Note#Head|Alias", "dir/Note", "Head", "Alias"},
		{"Note#^block1", "Note", "^block1", ""}, // Obsidian block reference
		{"Note^block1", "Note", "^block1", ""},  // tolerated bare-caret form
		{"Note#^block1|Alias", "Note", "^block1", "Alias"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			tg, h, d := splitWikilink(tt.in)
			if tg != tt.target || h != tt.heading || d != tt.display {
				t.Errorf("got (%q,%q,%q), want (%q,%q,%q)", tg, h, d, tt.target, tt.heading, tt.display)
			}
		})
	}
}

func TestResolvesTo(t *testing.T) {
	// bare target resolves by basename; path target resolves by relpath.
	if !resolvesTo("Beta", "Beta", "02-Areas/Beta") {
		t.Error("bare basename should resolve")
	}
	if !resolvesTo("02-Areas/Beta", "Beta", "02-Areas/Beta") {
		t.Error("path form should resolve")
	}
	if resolvesTo("02-Areas/Beta", "Beta", "03-Other/Beta") {
		t.Error("path form must not resolve to a different relpath")
	}
	if resolvesTo("Other", "Beta", "02-Areas/Beta") {
		t.Error("non-matching basename must not resolve")
	}
	// Obsidian resolves links case-insensitively: [[beta]] finds Beta.md.
	if !resolvesTo("beta", "Beta", "02-Areas/Beta") {
		t.Error("case-insensitive basename should resolve (Obsidian semantics)")
	}
	if !resolvesTo("02-areas/beta", "Beta", "02-Areas/Beta") {
		t.Error("case-insensitive path form should resolve (Obsidian semantics)")
	}
}

func TestRewriteLinksForMove(t *testing.T) {
	body := "Links: [[Beta]], [[02-Areas/Beta|Display]], ![[Beta]], [[Beta#Heading]].\n" +
		"Unrelated [[Gamma]] stays.\n"
	got, n := rewriteLinksForMove(body, "02-Areas/Beta.md", "03-Resources/Renamed.md")
	if n != 4 {
		t.Fatalf("rewrote %d links, want 4", n)
	}
	want := "Links: [[03-Resources/Renamed]], [[03-Resources/Renamed|Display]], ![[03-Resources/Renamed]], [[03-Resources/Renamed#Heading]].\n" +
		"Unrelated [[Gamma]] stays.\n"
	if got != want {
		t.Errorf("rewrite mismatch:\n got %q\nwant %q", got, want)
	}
}

// TestRewriteLinksForMoveCaseAndBlocks: case-variant links and block
// references must be rewritten on move too, or they dangle silently.
func TestRewriteLinksForMoveCaseAndBlocks(t *testing.T) {
	body := "Case: [[beta]] and [[02-areas/BETA|d]]. Blocks: [[Beta#^quote1]] and [[Beta^quote2]].\n"
	got, n := rewriteLinksForMove(body, "02-Areas/Beta.md", "03-Resources/Renamed.md")
	if n != 4 {
		t.Fatalf("rewrote %d links, want 4:\n%s", n, got)
	}
	want := "Case: [[03-Resources/Renamed]] and [[03-Resources/Renamed|d]]. " +
		"Blocks: [[03-Resources/Renamed#^quote1]] and [[03-Resources/Renamed#^quote2]].\n"
	if got != want {
		t.Errorf("rewrite mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestTargetKey(t *testing.T) {
	k, isPath := TargetKey("dir/Note.md")
	if k != "dir/Note" || !isPath {
		t.Errorf("got (%q,%v), want (dir/Note,true)", k, isPath)
	}
	k, isPath = TargetKey("Note")
	if k != "Note" || isPath {
		t.Errorf("got (%q,%v), want (Note,false)", k, isPath)
	}
}
