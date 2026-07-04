package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTriage(t *testing.T) {
	tests := []struct {
		in      string
		folder  string
		tags    int
		wantErr bool
	}{
		{`{"folder":"02-Areas","tags":["health","routine"]}`, "02-Areas", 2, false},
		{"Sure! Here you go: {\"folder\":\"01-Projects\",\"tags\":[]}", "01-Projects", 0, false},
		{`{"folder":"05-Nope","tags":[]}`, "", 0, true},
		{`not json at all`, "", 0, true},
	}
	for _, tt := range tests {
		out, err := parseTriage(tt.in)
		if (err != nil) != tt.wantErr {
			t.Fatalf("%q: err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err == nil && (out.Folder != tt.folder || len(out.Tags) != tt.tags) {
			t.Fatalf("%q: out = %+v", tt.in, out)
		}
	}
}

func TestInboxTriageStructuredLine(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/idea.md": "a captured thought\n"})
	fake.Reply = `{"folder":"02-Areas","tags":["thinking","ideas"]}`
	if _, err := (InboxTriage{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	q, _ := os.ReadFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"))
	want := "- [ ] triage [[00-Inbox/idea]] → 02-Areas (tags: thinking, ideas)"
	if !strings.Contains(string(q), want) {
		t.Fatalf("queue missing %q:\n%s", want, q)
	}
}

func TestInboxTriageRejectsBadFolder(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/idea.md": "thought\n"})
	fake.Reply = `{"folder":"99-Bogus","tags":[]}`
	if _, err := (InboxTriage{}).Run(context.Background(), rc); err == nil {
		t.Fatal("invalid folder must fail validation at the chokepoint")
	}
}
