package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agedFile writes a file and backdates its mtime past the settle window, so
// the sweep considers it stable.
func agedFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

func watchRC(t *testing.T, folders ...string) RunCtx {
	t.Helper()
	rc, _ := newRC(t, nil)
	rc.Config.Capture.WatchFolders = folders
	return rc
}

func TestSweepMovesStableFiles(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "report.pdf", "%PDF-1.4 fake")
	rc := watchRC(t, watched)

	moved, capped, problems := sweepWatchFolders(rc)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if capped {
		t.Fatal("one file must not trip the cap")
	}
	if len(moved) != 1 || moved[0] != "report.pdf" {
		t.Fatalf("want [report.pdf], got %v", moved)
	}
	if !rc.Vault.Exists("00-Inbox/report.pdf") {
		t.Fatal("file did not land in 00-Inbox")
	}
	if _, err := os.Stat(filepath.Join(watched, "report.pdf")); !os.IsNotExist(err) {
		t.Fatal("the source file must be gone after a move")
	}
}

// The security-relevant refusal: a symlink must never be moved in, because
// Ingest would follow it and read its target into the vault and the model.
func TestSweepSkipsSymlinks(t *testing.T) {
	watched := t.TempDir()
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(watched, "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	rc := watchRC(t, watched)

	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 0 {
		t.Fatalf("a symlink must never be moved, got %v", moved)
	}
	if rc.Vault.Exists("00-Inbox/innocent.txt") {
		t.Fatal("a symlink reached the vault — Ingest would follow it to its target")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("the symlink must be left in place, not removed")
	}
}

func TestSweepSkipsUnsettledFiles(t *testing.T) {
	watched := t.TempDir()
	// Written now: still inside the settle window.
	if err := os.WriteFile(filepath.Join(watched, "downloading.pdf"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := watchRC(t, watched)

	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 0 {
		t.Fatalf("a file written just now must wait for the settle window, got %v", moved)
	}

	// Age it past the window; now it moves.
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(filepath.Join(watched, "downloading.pdf"), old, old); err != nil {
		t.Fatal(err)
	}
	moved, _, _ = sweepWatchFolders(rc)
	if len(moved) != 1 {
		t.Fatalf("a settled file must move, got %v", moved)
	}
}

func TestSweepSkipsDirsAndDotfiles(t *testing.T) {
	watched := t.TempDir()
	if err := os.Mkdir(filepath.Join(watched, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	agedFile(t, watched, ".hidden", "x")
	agedFile(t, watched, "real.txt", "y")
	rc := watchRC(t, watched)

	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 1 || moved[0] != "real.txt" {
		t.Fatalf("want just real.txt, got %v", moved)
	}
}

func TestSweepSuffixesNameCollisions(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "notes.txt", "first")
	rc := watchRC(t, watched)
	if _, _, p := sweepWatchFolders(rc); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
	// A second file with the same name arrives later.
	agedFile(t, watched, "notes.txt", "second")
	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 1 || moved[0] != "notes-2.txt" {
		t.Fatalf("want [notes-2.txt], got %v", moved)
	}
	if !rc.Vault.Exists("00-Inbox/notes.txt") || !rc.Vault.Exists("00-Inbox/notes-2.txt") {
		t.Fatal("both files must exist; the first must not be clobbered")
	}
}

func TestSweepCapsPerTick(t *testing.T) {
	watched := t.TempDir()
	for i := 0; i < watchMaxPerTick+5; i++ {
		agedFile(t, watched, "f"+string(rune('a'+i))+".txt", "x")
	}
	rc := watchRC(t, watched)

	moved, capped, _ := sweepWatchFolders(rc)
	if len(moved) != watchMaxPerTick {
		t.Fatalf("want %d moves, got %d", watchMaxPerTick, len(moved))
	}
	if !capped {
		t.Fatal("the cap must be reported so a truncated sweep never reads as complete")
	}
}

func TestSweepSkipsMissingFolderWithoutError(t *testing.T) {
	rc := watchRC(t, filepath.Join(t.TempDir(), "not-mounted"))
	moved, _, problems := sweepWatchFolders(rc)
	if len(moved) != 0 {
		t.Fatalf("nothing to move, got %v", moved)
	}
	if len(problems) != 0 {
		t.Fatalf("a missing folder is a doctor concern, not a per-run problem: %v", problems)
	}
}

func TestSweepIsNoOpWithoutFolders(t *testing.T) {
	rc, _ := newRC(t, nil) // no watch folders configured
	moved, capped, problems := sweepWatchFolders(rc)
	if len(moved) != 0 || capped || len(problems) != 0 {
		t.Fatalf("an empty list must be a complete no-op, got %v %v %v", moved, capped, problems)
	}
}

func TestSweepNeverWritesIntoTheWatchedFolder(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "a.txt", "x")
	rc := watchRC(t, watched)
	if _, _, p := sweepWatchFolders(rc); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
	entries, err := os.ReadDir(watched)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the sweep must only remove, never create: %v", names)
	}
}

func TestEligibleWatchFilesIsContentFree(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "a.txt", "x")
	rc := watchRC(t, watched)
	files := eligibleWatchFiles(rc)
	if len(files) != 1 {
		t.Fatalf("want 1 eligible file, got %v", files)
	}
	if !strings.HasSuffix(files[0].Name, "a.txt") {
		t.Fatalf("unexpected entry: %+v", files[0])
	}
	if files[0].Size == 0 {
		t.Fatal("size is part of the fingerprint and must be populated")
	}
}

// THE regression for this slice. DetectChange runs BEFORE Run, so if the
// fingerprint covers only 00-Inbox, a new file in a watched folder leaves the
// inbox unchanged, capture reports "no change", Run never executes, and the
// sweep never happens — a feature that looks correct and does nothing.
func TestCaptureDetectsChangesInWatchedFolders(t *testing.T) {
	watched := t.TempDir()
	rc := watchRC(t, watched)
	ctx := context.Background()

	// Baseline with an empty inbox and an empty watched folder.
	first, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	rc.LastCursor = first.Cursor
	if again, err := (Capture{}).DetectChange(ctx, rc); err != nil || again.Changed {
		t.Fatalf("nothing moved; want unchanged, got %+v err=%v", again, err)
	}

	// A file appears in the WATCHED folder. The inbox is untouched.
	agedFile(t, watched, "new.txt", "hello")
	after, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Changed {
		t.Fatal("a new file in a watched folder must re-arm the capture gate")
	}
}

// A file in a watched folder is swept into the inbox by Run and reported.
func TestCaptureRunSweepsWatchedFolders(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "dropped.txt", "some captured text")
	rc := watchRC(t, watched)

	res, err := (Capture{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(watched, "dropped.txt")); !os.IsNotExist(err) {
		t.Fatal("the watched folder should be empty after the sweep")
	}
	if !strings.Contains(res.Summary, "watch") && len(res.Changes) == 0 {
		t.Fatalf("the run should report what it swept: %+v", res)
	}
}

// With no watch folders configured, capture's fingerprint and behaviour must
// be exactly what they were before this slice.
func TestCaptureUnchangedWithoutWatchFolders(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	ch, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Cursor == "" {
		t.Fatal("fingerprint must still be produced")
	}
	rc.LastCursor = ch.Cursor
	again, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Fatal("an unchanged empty vault with no watch folders must skip")
	}
}
