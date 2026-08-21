package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/core"
)

func withChecks(rc RunCtx, checks ...core.Check) RunCtx {
	rc.SelfCheck = func(context.Context) []core.Check { return checks }
	return rc
}

// writeQueue rewrites the review queue directly. The queue is a plain
// appended file, not a managed block, so tests that need to resolve an item
// edit it in place the way a human would.
func writeQueue(t *testing.T, rc RunCtx, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelfCheckProposesOnlyChecksWithAFix(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc,
		core.Check{Name: "config", Status: core.StatusOK, Detail: "valid"},
		core.Check{Name: "ok-with-fix", Status: core.StatusOK, Detail: "fine", Fix: "nothing"},
		core.Check{Name: "warn-no-fix", Status: core.StatusWarn, Detail: "odd, but nothing to do"},
		core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "PATH cannot resolve claude", Fix: "axon service reinstall"},
		core.Check{Name: "claude-cli", Status: core.StatusFail, Detail: "not found on PATH", Fix: "brew install claude"},
	)
	res, err := (SelfCheck{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	q, err := rc.Vault.Read(context.Background(), ".axon/review-queue.md")
	if err != nil {
		t.Fatalf("queue not written: %v (summary=%s)", err, res.Summary)
	}
	if !strings.Contains(q.Body, "fix service-path — \"PATH cannot resolve claude\" → `axon service reinstall`") {
		t.Fatalf("warn-with-fix must propose:\n%s", q.Body)
	}
	if !strings.Contains(q.Body, "fix claude-cli") {
		t.Fatalf("fail-with-fix must propose:\n%s", q.Body)
	}
	if strings.Contains(q.Body, "warn-no-fix") {
		t.Fatalf("a check with no Fix has nothing to propose:\n%s", q.Body)
	}
	if strings.Contains(q.Body, "ok-with-fix") {
		t.Fatalf("a passing check must never propose:\n%s", q.Body)
	}
	if strings.Contains(q.Body, "fix config") {
		t.Fatalf("a passing check must never propose:\n%s", q.Body)
	}
}

func TestSelfCheckDedupsAcrossRuns(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	ctx := context.Background()
	if _, err := (SelfCheck{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	res, err := (SelfCheck{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "no new") {
		t.Fatalf("a standing warning must propose once, got %q", res.Summary)
	}
	q, _ := rc.Vault.Read(ctx, ".axon/review-queue.md")
	if n := strings.Count(q.Body, "fix service-path"); n != 1 {
		t.Fatalf("want exactly 1 proposal, got %d:\n%s", n, q.Body)
	}
}

func TestSelfCheckReproposesWhenTheFixChanges(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	first := withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	if _, err := (SelfCheck{}).Run(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Same check name, DIFFERENT remediation => genuinely different work.
	// Resolve the pending item first, so the name-based pending skip does not
	// mask the memory-key behaviour under test.
	q, _ := rc.Vault.Read(ctx, ".axon/review-queue.md")
	writeQueue(t, rc, strings.ReplaceAll(q.Body, "- [ ] fix", "- [x] fix"))

	second := withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service install --force"})
	res, err := (SelfCheck{}).Run(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Summary, "no new") {
		t.Fatalf("a changed Fix is different work and must re-propose, got %q", res.Summary)
	}
}

func TestSelfCheckSkipsChecksAlreadyPending(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	if _, err := (SelfCheck{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	// Clear the memory but leave the item PENDING: the name-based skip alone
	// must prevent a second proposal for the same check.
	saveProposalMemory(ctx, rc, selfCheckState, map[string]bool{})
	changed := withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "totally different remedy"})
	res, err := (SelfCheck{}).Run(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "no new") {
		t.Fatalf("a check already pending must not propose again, got %q", res.Summary)
	}
}

func TestSelfCheckCapsProposalsPerRun(t *testing.T) {
	rc, _ := newRC(t, nil)
	var checks []core.Check
	for i := 0; i < selfCheckMaxProposals+3; i++ {
		checks = append(checks, core.Check{
			Name: "check-" + string(rune('a'+i)), Status: core.StatusWarn,
			Detail: "d", Fix: "do thing " + string(rune('a'+i)),
		})
	}
	rc = withChecks(rc, checks...)
	if _, err := (SelfCheck{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	q, _ := rc.Vault.Read(context.Background(), ".axon/review-queue.md")
	if n := strings.Count(q.Body, "- [ ] fix "); n != selfCheckMaxProposals {
		t.Fatalf("want %d proposals, got %d:\n%s", selfCheckMaxProposals, n, q.Body)
	}
}

func TestSelfCheckIdlesWithoutASeam(t *testing.T) {
	rc, _ := newRC(t, nil) // SelfCheck left nil
	ch, err := (SelfCheck{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatalf("a nil seam must idle, not error: %v", err)
	}
	if ch.Changed || !strings.Contains(ch.Reason, "unavailable") {
		t.Fatalf("want an unavailable idle, got %+v", ch)
	}
	res, err := (SelfCheck{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("a nil seam must idle, not error: %v", err)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatalf("a nil seam must write nothing (summary=%s)", res.Summary)
	}
}

func TestSelfCheckDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	res, err := (SelfCheck{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "would") {
		t.Fatalf("dry-run summary wrong: %q", res.Summary)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("dry-run must not write the queue")
	}
}

func TestSelfCheckChangeGate(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc, core.Check{Name: "service-path", Status: core.StatusWarn, Detail: "d", Fix: "axon service reinstall"})
	ctx := context.Background()
	ch, err := (SelfCheck{}).DetectChange(ctx, rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first detect: %+v err=%v", ch, err)
	}
	rc.LastCursor = ch.Cursor
	again, err := (SelfCheck{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Fatalf("an unchanged check set must skip, got %+v", again)
	}
}

// Sanitization keeps the rendered line parseable by review's fixRe: a stray
// double quote or backtick in a check's text would break the round-trip.
func TestSelfCheckSanitizesCheckText(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc = withChecks(rc, core.Check{
		Name: "claude-cli", Status: core.StatusWarn,
		Detail: "claude \"CLI\"\nnot found", Fix: "run `brew install claude`",
	})
	if _, err := (SelfCheck{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	q, _ := rc.Vault.Read(context.Background(), ".axon/review-queue.md")
	line := ""
	for _, l := range strings.Split(q.Body, "\n") {
		if strings.Contains(l, "fix claude-cli") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no proposal rendered:\n%s", q.Body)
	}
	if strings.Count(line, "`") != 2 {
		t.Fatalf("the command must be wrapped in exactly one code span: %q", line)
	}
	if strings.Contains(line, "\\\"") {
		t.Fatalf("inner quotes must be downgraded, not escaped: %q", line)
	}
}
