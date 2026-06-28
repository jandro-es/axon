package vault

import (
	"context"
	"strings"
	"testing"
)

func TestFakeWriteReadList(t *testing.T) {
	v := NewFake()
	ctx := context.Background()

	if err := v.Write(ctx, "01-Projects/a.md", &Note{Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Write(ctx, "00-Inbox/b.md", &Note{Body: "world"}); err != nil {
		t.Fatal(err)
	}

	paths, err := v.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "00-Inbox/b.md" {
		t.Errorf("List returned %v, want sorted 2-element list", paths)
	}

	n, err := v.Read(ctx, "01-Projects/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.Body != "hello" || n.Path != "01-Projects/a.md" {
		t.Errorf("Read returned %+v", n)
	}

	if _, err := v.Read(ctx, "missing.md"); err == nil {
		t.Error("expected error reading missing note")
	}
}

func TestFakePatchManagedBlock(t *testing.T) {
	v := NewFake()
	ctx := context.Background()
	_ = v.Write(ctx, "n.md", &Note{Body: "Human prose."})

	// First patch appends a managed block, leaving human prose intact.
	if err := v.Patch(ctx, "n.md", "summary", "first"); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Read(ctx, "n.md")
	if !strings.Contains(n.Body, "Human prose.") {
		t.Error("human prose was clobbered by Patch")
	}
	if !strings.Contains(n.Body, "<!-- axon:summary:start -->\nfirst\n<!-- axon:summary:end -->") {
		t.Errorf("managed block not written correctly:\n%s", n.Body)
	}

	// Second patch replaces the block content, not duplicating it.
	if err := v.Patch(ctx, "n.md", "summary", "second"); err != nil {
		t.Fatal(err)
	}
	n, _ = v.Read(ctx, "n.md")
	if strings.Count(n.Body, "axon:summary:start") != 1 {
		t.Errorf("managed block duplicated:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "first") || !strings.Contains(n.Body, "second") {
		t.Errorf("block content not replaced:\n%s", n.Body)
	}
}

func TestFakeMoveRewritesWikilinks(t *testing.T) {
	v := NewFake()
	ctx := context.Background()
	_ = v.Write(ctx, "old.md", &Note{Body: "I am old."})
	_ = v.Write(ctx, "ref.md", &Note{Body: "See [[old]] for details."})

	if err := v.Move(ctx, "old.md", "01-Projects/new.md"); err != nil {
		t.Fatal(err)
	}

	if _, err := v.Read(ctx, "old.md"); err == nil {
		t.Error("old path should no longer exist after Move")
	}
	if _, err := v.Read(ctx, "01-Projects/new.md"); err != nil {
		t.Errorf("note not found at new path: %v", err)
	}

	ref, _ := v.Read(ctx, "ref.md")
	if !strings.Contains(ref.Body, "[[01-Projects/new]]") {
		t.Errorf("inbound wikilink not rewritten: %q", ref.Body)
	}
	if strings.Contains(ref.Body, "[[old]]") {
		t.Error("stale wikilink remains after Move")
	}
}

func TestFakeMoveErrors(t *testing.T) {
	v := NewFake()
	ctx := context.Background()
	_ = v.Write(ctx, "a.md", &Note{})
	_ = v.Write(ctx, "b.md", &Note{})

	if err := v.Move(ctx, "missing.md", "x.md"); err == nil {
		t.Error("expected error moving missing note")
	}
	if err := v.Move(ctx, "a.md", "b.md"); err == nil {
		t.Error("expected error moving onto an existing note")
	}
}
