# Outbound notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the daemon push selected events to an ntfy topic so the owner hears about them without opening anything.

**Architecture:** Four layers. `internal/events` gains an exported `KnownKinds` list so a typo'd subscription is refused rather than silent. `internal/config` gains a `notify` block and its validator. `internal/ingestion` gains an exported egress-only policy check (it owns `policy.go`, and `notify` imports it for the redactor regardless). `internal/notify` is a new leaf package: a `Notifier` seam, an ntfy sender, and a bus subscriber with a bounded queue and a rate limit, wired in `cmd/axon/start_cmd.go` beside the existing subscribers.

**Tech Stack:** Go 1.26+, standard library `net/http` — no new dependency.

**Spec:** `docs/superpowers/specs/2026-08-21-notifications-design.md`

## Global Constraints

- **FR IDs:** FR-210 (config, validation, egress rules, doctor), FR-211 (the seam, the sender, the subscriber). **ADR-041** covers the decision; do not write another ADR.
- **No migration.** Schema stays `0007`. **No new automation** — built-ins stay 26. If `registry_test.go`, `seeds_test.go` or `internal/mcp/tools_more_test.go` fails, something is wrong.
- **No new dependency.** `net/http` only.
- **Off by default, both profiles.** `notify.url` and `notify.events` both empty is the default and the off state. There is no separate toggle.
- **The configured URL is the allow-list.** One destination, no discovery, **no redirect following**, no second target.
- **The host must additionally pass `egress_allowlist`** — so a work profile's `["localhost"]` refuses `ntfy.sh`.
- **`BlockedIPReason` is NOT applied** (ADR-041). Self-hosted ntfy on localhost or a LAN must work. Do not "fix" this by adding the guard.
- **`https` required** unless the host is loopback or a private IP.
- **Payload is `Kind`, `Level`, `Message` only — never `Event.Data`.** Redacted via `ingestion.NewRedactor(policy.RedactionRules)` pre-send; **a redactor that fails to compile refuses the send** rather than sending unredacted. Body capped at 512 bytes.
- **Best-effort:** never retry, never surface as an automation failure, never propagate an error into the daemon. Queue capacity 64, request timeout 10s, rate limit 10/minute, drops counted and logged.
- **Go hygiene:** `gofmt`/`goimports` clean, `go vet` and `golangci-lint` green, errors wrapped with `%w`, `context.Context` propagated. Cleanup-path calls that return errors need `_ =` or errcheck fails.
- **Run `go test -race ./...` before pushing** — CI runs the race detector and the local gate does not.
- **Never bind port 7777**; smoke work uses 7799 and isolates `vault_path` and `data_dir`, not just `AXON_HOME`.

---

### Task 1: `events.KnownKinds`

**Files:**
- Modify: `internal/events/event.go`
- Test: `internal/events/kinds_test.go`

**Interfaces:**
- Produces: `events.KnownKinds []string` and `events.IsKnownKind(string) bool`. Task 2's validator calls `IsKnownKind`.

- [ ] **Step 1: Write the failing test**

Create `internal/events/kinds_test.go`:

```go
package events

import "testing"

func TestKnownKindsAreWellFormed(t *testing.T) {
	if len(KnownKinds) == 0 {
		t.Fatal("KnownKinds must not be empty")
	}
	seen := map[string]bool{}
	for _, k := range KnownKinds {
		if k == "" {
			t.Fatal("KnownKinds contains an empty string")
		}
		if seen[k] {
			t.Fatalf("KnownKinds contains a duplicate: %q", k)
		}
		seen[k] = true
	}
	// Spot-check kinds real emitters publish today.
	for _, k := range []string{"automation.fail", "automation.run", "token.deny", "ingest.done"} {
		if !IsKnownKind(k) {
			t.Errorf("%q is emitted in the codebase but missing from KnownKinds", k)
		}
	}
	if IsKnownKind("automation.failed") {
		t.Error("IsKnownKind must reject a near-miss typo")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/events/ -run TestKnownKinds -v`
Expected: FAIL — `undefined: KnownKinds`.

- [ ] **Step 3: Collect the real kinds, then implement**

Enumerate what the codebase actually publishes, so the list is grounded rather than guessed:

```bash
grep -rhoE '"(automation|ingest|token|review|health|service)\.[a-z.]+"' internal/ cmd/ | sort -u
```

Add to `internal/events/event.go`, using that output:

```go
// KnownKinds enumerates the event kinds AXON publishes. It exists so a
// notify.events subscription can be validated at config load: a typo'd kind
// ("automation.failed" for "automation.fail") would otherwise produce a
// notifier that is configured, enabled, and permanently silent.
//
// It lives here rather than in internal/notify to sit as close to the emitters
// as the current structure allows. The trade is real and deliberate: a NEW
// kind added without updating this list is refused by notify validation until
// someone notices. A loud refusal beats a silent notifier.
var KnownKinds = []string{
	// … paste the grep output, one per line, grouped by prefix …
}

// IsKnownKind reports whether kind is one AXON publishes.
func IsKnownKind(kind string) bool {
	for _, k := range KnownKinds {
		if k == kind {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/events/ -v && gofmt -l internal/events`
Expected: PASS, `gofmt -l` silent. If the spot-check fails, the grep missed a kind — add it rather than weakening the test.

- [ ] **Step 5: Commit**

```bash
git add internal/events/event.go internal/events/kinds_test.go
git commit -m "feat(events): KnownKinds, so a typo'd notify subscription is refused (FR-210)"
```

---

### Task 2: `notify` config + validation (FR-210)

**Files:**
- Modify: `internal/config/types.go` (add `NotifyConfig`, and a `Notify` field on `Profile`)
- Create: `internal/config/notify.go`
- Modify: `internal/config/load.go` (call it beside `validateWatchFolders`)
- Test: `internal/config/notify_test.go`

**Interfaces:**
- Consumes: `events.IsKnownKind` (Task 1). Check for an import cycle first: `grep -rn "internal/events" internal/config/` — if `events` imports `config`, stop and raise it rather than working around it.
- Produces: `config.NotifyConfig{URL string; Events []string}`, `Profile.Notify NotifyConfig`, `validateNotify(p Profile) error`, and `(NotifyConfig) Enabled() bool`. Tasks 4, 5 and 6 read the config; Task 6 calls `Enabled`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/notify_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

func TestValidateNotify(t *testing.T) {
	p := func(url string, events ...string) Profile {
		return Profile{Notify: NotifyConfig{URL: url, Events: events}}
	}

	// Off is valid, and is the default.
	if err := validateNotify(Profile{}); err != nil {
		t.Fatalf("both-empty must be valid (it is the off state): %v", err)
	}
	// Fully configured is valid.
	if err := validateNotify(p("https://ntfy.sh/axon-test", "automation.fail")); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	// Plaintext to loopback and to a private IP is allowed (self-hosted ntfy).
	for _, u := range []string{"http://localhost:8080/axon", "http://127.0.0.1:8080/axon", "http://192.168.1.10/axon"} {
		if err := validateNotify(p(u, "automation.fail")); err != nil {
			t.Errorf("self-hosted target %q must be allowed: %v", u, err)
		}
	}

	cases := []struct {
		name string
		prof Profile
		want string
	}{
		{"events without url", p("", "automation.fail"), "notify.url"},
		{"url without events", p("https://ntfy.sh/x"), "notify.events"},
		{"unparseable url", p("://nope", "automation.fail"), "url"},
		{"file scheme", p("file:///etc/passwd", "automation.fail"), "scheme"},
		{"plaintext to a public host", p("http://ntfy.sh/x", "automation.fail"), "https"},
		{"unknown kind", p("https://ntfy.sh/x", "automation.failed"), "automation.failed"},
	}
	for _, c := range cases {
		err := validateNotify(c.prof)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestNotifyEnabled(t *testing.T) {
	if (NotifyConfig{}).Enabled() {
		t.Error("empty config must be disabled")
	}
	if (NotifyConfig{URL: "https://ntfy.sh/x"}).Enabled() {
		t.Error("a url with no events must be disabled")
	}
	if !(NotifyConfig{URL: "https://ntfy.sh/x", Events: []string{"automation.fail"}}).Enabled() {
		t.Error("a fully configured notifier must be enabled")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestValidateNotify|TestNotifyEnabled' -v`
Expected: FAIL — `undefined: NotifyConfig`.

- [ ] **Step 3: Add the config types**

In `internal/config/types.go`, add the struct and a `Profile` field beside `Capture`:

```go
// NotifyConfig configures outbound notifications (FR-210, ADR-041). Both
// fields empty is the default on both profiles and is the off state — there is
// no separate toggle. The URL is the entire allow-list: AXON pushes there and
// nowhere else.
type NotifyConfig struct {
	URL    string   `yaml:"url,omitempty"`
	Events []string `yaml:"events,omitempty"`
}

// Enabled reports whether notifications should run: both a destination and at
// least one subscribed kind.
func (n NotifyConfig) Enabled() bool {
	return strings.TrimSpace(n.URL) != "" && len(n.Events) > 0
}
```

Add `Notify NotifyConfig \`yaml:"notify,omitempty"\`` to `Profile`.

- [ ] **Step 4: Write the validator**

Create `internal/config/notify.go`:

```go
package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/jandro-es/axon/internal/events"
)

// validateNotify enforces the ADR-041 rules. Runs at profile level in
// Config.Validate, beside validateWatchFolders.
func validateNotify(p Profile) error {
	n := p.Notify
	raw := strings.TrimSpace(n.URL)
	if raw == "" && len(n.Events) == 0 {
		return nil // off, the default
	}
	// A half-configured notifier is silent, and silence is indistinguishable
	// from working — so refuse both halves of the mistake loudly.
	if raw == "" {
		return fmt.Errorf("notify.events is set but notify.url is empty — a notifier with no destination is silent")
	}
	if len(n.Events) == 0 {
		return fmt.Errorf("notify.url is set but notify.events is empty — a notifier with no subscribed kinds never fires")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("notify.url %q is not a valid absolute url", n.URL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("notify.url %q: scheme must be http or https (got %q)", n.URL, u.Scheme)
	}
	if u.Scheme == "http" && !isLocalHostname(u.Hostname()) {
		return fmt.Errorf("notify.url %q must use https for a public host — plaintext would push vault activity in the clear", n.URL)
	}
	for _, k := range n.Events {
		if !events.IsKnownKind(strings.TrimSpace(k)) {
			return fmt.Errorf("notify.events: %q is not an event AXON publishes (a typo here is silent forever)", k)
		}
	}
	return nil
}

// isLocalHostname reports whether a host is loopback or a private address —
// the self-hosted-ntfy case, where plaintext is acceptable because the traffic
// never leaves the machine or the LAN.
func isLocalHostname(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
```

Wire it into `internal/config/load.go` right after the `validateWatchFolders` block:

```go
		if err := validateNotify(p); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ ./internal/events/ && gofmt -l internal/config && go vet ./internal/config/`
Expected: PASS. If `internal/config` importing `internal/events` creates a cycle, **stop and raise it** — moving `KnownKinds` into `config` would put it far from the emitters and is a design change, not a fix to apply silently.

- [ ] **Step 6: Commit**

```bash
git add internal/config/types.go internal/config/notify.go internal/config/notify_test.go internal/config/load.go
git commit -m "feat(config): notify block with url/events validation (FR-210)"
```

---

### Task 3: `ingestion.CheckEgressPolicy` + the ntfy sender (FR-211)

**Files:**
- Modify: `internal/ingestion/policy.go` (add the exported egress-only check)
- Create: `internal/notify/notify.go` (the `Note`/`Notifier` types and `Ntfy`)
- Test: `internal/ingestion/policy_test.go` (append), `internal/notify/notify_test.go`

**Interfaces:**
- Consumes: `config.PolicyConfig`, `ingestion.NewRedactor(rules []string) (*Redactor, error)`, `(*Redactor).Redact(string) (string, bool)`, `events.Level`.
- Produces: `ingestion.CheckEgressPolicy(p config.PolicyConfig, host string) error`; `notify.Note{Kind string; Level events.Level; Title, Body string}`; `notify.Notifier` interface with `Send(ctx, Note) error`; `notify.NewNtfy(url string, rules []string) (*Ntfy, error)`. Task 4 constructs the notifier and calls `Send`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ingestion/policy_test.go`:

```go
func TestCheckEgressPolicy(t *testing.T) {
	// A wildcard allowlist permits anything.
	if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"localhost", "*"}}, "ntfy.sh"); err != nil {
		t.Fatalf("wildcard allowlist should permit any host: %v", err)
	}
	// A strict allowlist refuses an unlisted host — the work-profile case.
	if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"localhost"}}, "ntfy.sh"); err == nil {
		t.Fatal("a strict allowlist must refuse an unlisted host")
	}
	// …and permits a listed one.
	if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"localhost"}}, "localhost"); err != nil {
		t.Fatalf("a listed host must be permitted: %v", err)
	}
	// An empty allowlist is permissive (matches CheckIngestPolicy's shape).
	if err := CheckEgressPolicy(config.PolicyConfig{}, "ntfy.sh"); err != nil {
		t.Fatalf("an empty allowlist must not refuse: %v", err)
	}
	// ADR-041: loopback and private targets are NOT blocked here, unlike
	// ingest — a notify URL comes from config, which no model can write.
	for _, h := range []string{"127.0.0.1", "192.168.1.10", "localhost"} {
		if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"*"}}, h); err != nil {
			t.Errorf("self-hosted target %q must be permitted: %v", h, err)
		}
	}
}
```

Create `internal/notify/notify_test.go`:

```go
package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/events"
)

func TestNtfyPostsTitleAndBody(t *testing.T) {
	var gotBody, gotTitle, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotTitle, gotPath = string(b), r.Header.Get("Title"), r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/axon-topic", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = n.Send(context.Background(), Note{
		Kind: "automation.fail", Level: events.LevelError,
		Title: "automation failed", Body: "capture failed: disk full",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotPath != "/axon-topic" {
		t.Errorf("path = %q, want /axon-topic", gotPath)
	}
	if gotTitle != "automation failed" {
		t.Errorf("Title header = %q", gotTitle)
	}
	if !strings.Contains(gotBody, "disk full") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestNtfyRedactsBeforeSending(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/t", []string{`sk-[A-Za-z0-9]+`})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), Note{Title: "t", Body: "leaked sk-ABC123 here"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotBody, "sk-ABC123") {
		t.Fatalf("the secret was sent unredacted: %q", gotBody)
	}
}

// A redaction rule that will not compile must REFUSE the send, not send
// unredacted. The failure mode of a bad regex must never be "your data goes
// out unfiltered".
func TestNtfyRefusesWhenRedactorCannotCompile(t *testing.T) {
	if _, err := NewNtfy("https://example.com/t", []string{"([unclosed"}); err == nil {
		t.Fatal("a bad redaction rule must refuse construction, not send unredacted")
	}
}

func TestNtfyCapsBodyLength(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	defer srv.Close()
	n, _ := NewNtfy(srv.URL+"/t", nil)
	if err := n.Send(context.Background(), Note{Title: "t", Body: strings.Repeat("x", maxBodyBytes*2)}); err != nil {
		t.Fatal(err)
	}
	if len(got) > maxBodyBytes+16 {
		t.Fatalf("body not capped: %d bytes", len(got))
	}
}

func TestNtfyReportsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	n, _ := NewNtfy(srv.URL+"/t", nil)
	if err := n.Send(context.Background(), Note{Title: "t", Body: "b"}); err == nil {
		t.Fatal("a non-2xx response must be an error the caller can log")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ingestion/ -run TestCheckEgressPolicy; go test ./internal/notify/`
Expected: both FAIL — `undefined: CheckEgressPolicy`, and no `internal/notify` package.

- [ ] **Step 3: Add the egress check**

Append to `internal/ingestion/policy.go`:

```go
// CheckEgressPolicy applies ONLY the network-level egress allowlist to an
// outbound host. Unlike CheckIngestPolicy it consults no ingest lists and
// applies no IP guard: it exists for destinations the OWNER named in config
// (ADR-041), where BlockedIPReason would block the self-hosted case while
// defending against a threat — a prompt-injected agent choosing the URL —
// that cannot occur on that path.
func CheckEgressPolicy(p config.PolicyConfig, host string) error {
	if host == "" {
		return &PolicyError{Host: host, Reason: "empty host"}
	}
	if len(p.EgressAllowlist) > 0 && !hasWildcard(p.EgressAllowlist) && !matchesAny(p.EgressAllowlist, host) {
		return &PolicyError{Host: host, Reason: "not in egress_allowlist"}
	}
	return nil
}
```

- [ ] **Step 4: Write the sender**

Create `internal/notify/notify.go`:

```go
// Package notify pushes selected events to an outbound destination the owner
// named in config (FR-211, ADR-041). Delivery is best-effort by decision: a
// notifier that can break the daemon is worse than no notifier.
package notify

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/ingestion"
)

// maxBodyBytes caps a notification body. A notification is a nudge, not a
// transcript.
const maxBodyBytes = 512

// Note is one outbound notification. Deliberately narrow: Event.Data never
// reaches here, because its fields are arbitrary and emitter-defined.
type Note struct {
	Kind  string
	Level events.Level
	Title string
	Body  string
}

// Notifier delivers a Note. The seam exists so the Companion-local path
// (macOS user notifications, no egress) can land later without touching the
// subscriber, and so tests use a fake rather than a live host.
type Notifier interface {
	Send(ctx context.Context, n Note) error
}

// Ntfy posts to an ntfy topic URL.
type Ntfy struct {
	url      string
	redactor *ingestion.Redactor
	client   *http.Client
}

// NewNtfy builds a sender. A redaction rule that will not compile is a
// construction error: the failure mode of a bad regex must never be "your
// data goes out unfiltered".
func NewNtfy(url string, redactionRules []string) (*Ntfy, error) {
	r, err := ingestion.NewRedactor(redactionRules)
	if err != nil {
		return nil, fmt.Errorf("notify: redaction rules do not compile, refusing to send unredacted: %w", err)
	}
	return &Ntfy{
		url:      url,
		redactor: r,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// The configured URL is the entire allow-list (ADR-041): never
			// follow a redirect to a destination the owner did not name.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (n *Ntfy) Send(ctx context.Context, note Note) error {
	title, _ := n.redactor.Redact(note.Title)
	body, _ := n.redactor.Redact(note.Body)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "…"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Title", title)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("notify: destination returned %s", resp.Status)
	}
	return nil
}
```

Check `Redactor` is exported and `NewRedactor` returns `(*Redactor, error)`:

```bash
grep -n "type Redactor\|func NewRedactor" internal/ingestion/redact.go
```

If `Redact` has a different signature, adjust the two call sites — do not change `redact.go`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ingestion/ ./internal/notify/ -v 2>&1 | tail -20 && gofmt -l internal/ingestion internal/notify && go vet ./internal/notify/`
Expected: PASS, `gofmt -l` silent, vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/ingestion/policy.go internal/ingestion/policy_test.go internal/notify/
git commit -m "feat(notify): ntfy sender with redaction and no redirect following (FR-211)"
```

---

### Task 4: The subscriber — queue, rate limit, drops (FR-211)

**Files:**
- Create: `internal/notify/subscriber.go`
- Modify: `cmd/axon/start_cmd.go` (start it beside the existing subscribers)
- Test: `internal/notify/subscriber_test.go`

**Interfaces:**
- Consumes: `events.Bus`, `events.Event`, `notify.Notifier` and `notify.Note` (Task 3), `config.NotifyConfig` (Task 2), `ingestion.CheckEgressPolicy` (Task 3).
- Produces: `notify.Run(ctx context.Context, bus *events.Bus, kinds []string, n Notifier, log *slog.Logger)`. Nothing later depends on it.

- [ ] **Step 1: Write the failing test**

Create `internal/notify/subscriber_test.go`:

```go
package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/events"
)

type fakeNotifier struct {
	mu    sync.Mutex
	sent  []Note
	err   error
	block chan struct{} // when non-nil, Send waits on it
}

func (f *fakeNotifier) Send(ctx context.Context, n Note) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	return f.err
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// eventually polls until cond holds or the deadline passes — delivery is
// asynchronous by design, so a bare assert would be flaky.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestSubscriberDeliversSubscribedKindsOnly(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, bus, []string{"automation.fail"}, f, quietLog())
	time.Sleep(20 * time.Millisecond) // let the subscription register

	bus.Publish(events.Event{Kind: "automation.fail", Level: events.LevelError, Message: "capture failed"})
	bus.Publish(events.Event{Kind: "ingest.done", Level: events.LevelInfo, Message: "ingested a page"})

	eventually(t, func() bool { return f.count() == 1 }, "the subscribed kind was not delivered")
	time.Sleep(50 * time.Millisecond)
	if f.count() != 1 {
		t.Fatalf("an unsubscribed kind was delivered: %+v", f.sent)
	}
	if f.sent[0].Kind != "automation.fail" || f.sent[0].Body != "capture failed" {
		t.Fatalf("wrong note: %+v", f.sent[0])
	}
}

// Event.Data must never reach the payload.
func TestSubscriberNeverSendsEventData(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, bus, []string{"automation.fail"}, f, quietLog())
	time.Sleep(20 * time.Millisecond)

	bus.Publish(events.Event{
		Kind: "automation.fail", Message: "failed",
		Data: map[string]any{"secret_path": "/Users/me/.ssh/id_rsa"},
	})
	eventually(t, func() bool { return f.count() == 1 }, "not delivered")
	if got := f.sent[0]; got.Body != "failed" || got.Title == "" {
		t.Fatalf("unexpected note shape: %+v", got)
	}
	// The struct has no Data field at all — this asserts the shape stays that
	// way if someone adds one.
	if len(f.sent[0].Body) > len("failed") {
		t.Fatalf("body carries more than the message: %q", f.sent[0].Body)
	}
}

// A slow notifier must never block the publisher.
func TestSubscriberDoesNotBlockThePublisher(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	block := make(chan struct{})
	f := &fakeNotifier{block: block}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, bus, []string{"automation.fail"}, f, quietLog())
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			bus.Publish(events.Event{Kind: "automation.fail", Message: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a stuck notifier")
	}
	close(block)
}

// A Send error must not stop the loop.
func TestSubscriberSurvivesSendErrors(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{err: errors.New("host down")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, bus, []string{"automation.fail"}, f, quietLog())
	time.Sleep(20 * time.Millisecond)

	bus.Publish(events.Event{Kind: "automation.fail", Message: "one"})
	eventually(t, func() bool { return f.count() >= 1 }, "first not attempted")
	bus.Publish(events.Event{Kind: "automation.fail", Message: "two"})
	eventually(t, func() bool { return f.count() >= 2 }, "the loop stopped after a send error")
}

func TestSubscriberStopsOnContextCancel(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, bus, []string{"automation.fail"}, f, quietLog()); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return on context cancellation")
	}
}
```

Check the bus constructor name first — `grep -n "func NewBus" internal/events/bus.go` — and adjust if it takes arguments.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/notify/ -run TestSubscriber`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the subscriber**

Create `internal/notify/subscriber.go`:

```go
package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/jandro-es/axon/internal/events"
)

const (
	queueCap      = 64              // pending notifications before dropping
	rateBurst     = 10              // deliveries per rateWindow
	rateWindow    = time.Minute     //
	sendTimeout   = 10 * time.Second
)

// Run subscribes to the bus and delivers matching events, mirroring
// dashboard.PersistEvents: it returns on context cancellation or bus close,
// and never propagates an error.
//
// events.Bus.Publish DROPS rather than blocks on a slow subscriber, so a hung
// POST would silently lose events. Delivery therefore runs on its own
// goroutine behind a bounded queue: the bus-facing loop never waits on HTTP.
// Drops are counted and logged — a silent cap reads as "nothing happened",
// which is the failure this feature exists to prevent.
func Run(ctx context.Context, bus *events.Bus, kinds []string, n Notifier, log *slog.Logger) {
	if bus == nil || n == nil || len(kinds) == 0 {
		return
	}
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	queue := make(chan Note, queueCap)
	go deliver(ctx, queue, n, log)

	sub := bus.Subscribe()
	defer sub.Close()
	dropped := 0
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if !want[e.Kind] {
				continue
			}
			select {
			case queue <- noteFor(e):
			default:
				dropped++
				log.Warn("notify: queue full, dropping notification",
					"kind", e.Kind, "dropped_total", dropped)
			}
		}
	}
}

// noteFor projects an event onto the wire shape. Event.Data is deliberately
// absent: its fields are arbitrary and emitter-defined (ADR-041).
func noteFor(e events.Event) Note {
	return Note{Kind: e.Kind, Level: e.Level, Title: e.Kind, Body: e.Message}
}

// deliver drains the queue at a bounded rate. A send failure is logged and
// discarded — never retried, never propagated.
func deliver(ctx context.Context, queue <-chan Note, n Notifier, log *slog.Logger) {
	ticker := time.NewTicker(rateWindow / rateBurst)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case note, ok := <-queue:
			if !ok {
				return
			}
			sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
			if err := n.Send(sendCtx, note); err != nil {
				log.Warn("notify: delivery failed", "kind", note.Kind, "err", err)
			}
			cancel()
			// Rate limit AFTER the send, so the first notification is
			// immediate and a burst is spread rather than delayed wholesale.
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/notify/ -v 2>&1 | tail -20`
Expected: PASS. Use `-race` here specifically — this is the one concurrent component in the slice.

- [ ] **Step 5: Wire it into the daemon**

In `cmd/axon/start_cmd.go`, find where the other bus subscribers are started (`grep -n "PersistEvents" cmd/axon/start_cmd.go`) and add beside them:

```go
	// Outbound notifications (FR-211): off unless the profile names both a
	// destination and at least one event kind.
	if nc := deps.profile.Notify; nc.Enabled() {
		host := ""
		if u, err := url.Parse(nc.URL); err == nil {
			host = u.Hostname()
		}
		if err := ingestion.CheckEgressPolicy(deps.profile.Policy, host); err != nil {
			log.Warn("notify: destination refused by egress policy — notifications disabled", "host", host, "err", err)
		} else if n, err := notify.NewNtfy(nc.URL, deps.profile.Policy.RedactionRules); err != nil {
			log.Warn("notify: disabled", "err", err)
		} else {
			wg.Add(1)
			go func() { defer wg.Done(); notify.Run(ctx, bus, nc.Events, n, log) }()
		}
	}
```

Match the surrounding names for `ctx`, `bus`, `log` and the waitgroup — read the neighbouring subscriber start first. Add `net/url`, `internal/ingestion` and `internal/notify` imports as needed.

- [ ] **Step 6: Full gate and commit**

```bash
go build ./... && go test -race ./... && golangci-lint run
git add internal/notify/ cmd/axon/start_cmd.go
git commit -m "feat(notify): bus subscriber with bounded queue and rate limit (FR-211)"
```

---

### Task 5: The doctor check (FR-210)

**Files:**
- Modify: `internal/core/doctor.go`
- Test: `internal/core/doctor_test.go` (append)

**Interfaces:**
- Consumes: `config.Profile.Notify` and `(NotifyConfig) Enabled()` (Task 2), `ingestion.CheckEgressPolicy` (Task 3), `core.Check`.
- Produces: a `notify` check. Nothing depends on it.

Check first whether `internal/core` already imports `internal/ingestion` (`grep -n "internal/ingestion" internal/core/*.go`). If not, and adding it creates a cycle, inline the allowlist comparison instead of importing — and say so in the commit.

- [ ] **Step 1: Write the failing test**

Append to `internal/core/doctor_test.go`:

```go
func TestNotifyCheck(t *testing.T) {
	off := notifyCheck(config.Profile{})
	if off.Status != StatusOK || !strings.Contains(off.Detail, "off") {
		t.Fatalf("empty config should read as off: %+v", off)
	}
	if off.Fix != "" {
		t.Fatalf("an off check has nothing to fix: %+v", off)
	}

	okProf := config.Profile{
		Notify: config.NotifyConfig{URL: "https://ntfy.sh/x", Events: []string{"automation.fail"}},
		Policy: config.PolicyConfig{EgressAllowlist: []string{"*"}},
	}
	good := notifyCheck(okProf)
	if good.Status != StatusOK || !strings.Contains(good.Detail, "ntfy.sh") {
		t.Fatalf("a permitted destination should pass and name the host: %+v", good)
	}

	blocked := okProf
	blocked.Policy = config.PolicyConfig{EgressAllowlist: []string{"localhost"}}
	warn := notifyCheck(blocked)
	if warn.Status != StatusWarn {
		t.Fatalf("a destination outside the egress allowlist must warn: %+v", warn)
	}
	if warn.Fix == "" {
		t.Fatal("the warning must carry a Fix — self-check only proposes checks that have one")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestNotifyCheck`
Expected: FAIL — `undefined: notifyCheck`.

- [ ] **Step 3: Write the check**

Add to `internal/core/doctor.go`:

```go
// notifyCheck reports on outbound notifications (FR-210). The warn path
// carries a Fix, so self-check (FR-207) files it.
func notifyCheck(p config.Profile) Check {
	const name = "notify"
	if !p.Notify.Enabled() {
		return Check{Name: name, Status: StatusOK,
			Detail: "off (set notify.url and notify.events to be told when something happens)"}
	}
	host := ""
	if u, err := url.Parse(p.Notify.URL); err == nil {
		host = u.Hostname()
	}
	if err := ingestion.CheckEgressPolicy(p.Policy, host); err != nil {
		return Check{Name: name, Status: StatusWarn,
			Detail: fmt.Sprintf("destination %s is refused by the egress policy — no notifications will be sent (%v)", host, err),
			Fix:    "add the host to policy.egress_allowlist, or clear notify.url"}
	}
	return Check{Name: name, Status: StatusOK,
		Detail: fmt.Sprintf("%d event kind(s) → %s", len(p.Notify.Events), host)}
}
```

Append it beside `watchFoldersCheck` in `Doctor`'s profile-scoped block.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ ./cmd/axon/ && gofmt -l internal/core && golangci-lint run`
Expected: PASS, silent, 0 issues.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/doctor_test.go
git commit -m "feat(core): notify doctor check with a Fix (FR-210)"
```

---

### Task 6: Documentation and the roadmap

**Files:**
- Modify: `docs/03-requirements.md`, `docs/02-architecture.md` (ADR-041 planned → built), `docs/04-data-model-and-config.md`, `docs/09-component-dashboard-observability.md`, `docs/GUIDE.md`, `axon.config.example.yaml`, `docs/20-roadmap-ai-os.md`, `CHANGELOG.md`, `CLAUDE.md`

- [ ] **Step 1: FR rows in `docs/03-requirements.md`**

After the FR-209 row, add a section and two rows:

```markdown
### Outbound notifications (docs/20 B1) *(built 2026-08-21)*

FR-210…FR-211 trace to **ADR-041** and graduate `docs/20` B1; spec in
`docs/superpowers/specs/2026-08-21-notifications-design.md`. No migration, no
new automation.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-210 | S | **`notify` config, validation, and doctor (ADR-041).** `notify.url` + `notify.events` on the profile; **both empty is the default on both profiles and is the off state** — there is no separate toggle. `validateNotify(p Profile)` runs in the `Config.Validate` per-profile loop and refuses: either field set without the other (a half-configured notifier is silent, and silence is indistinguishable from working); an unparseable URL or a scheme other than http/https; `http://` to a non-loopback, non-private host (plaintext would push vault activity in the clear); and any kind absent from `events.KnownKinds` (a typo'd kind is silent forever — the list lives in `internal/events`, near the emitters, and a new kind added without updating it is refused until noticed, which is the deliberate trade). A `notify` doctor check reports off / N kinds → host / a warn when the host fails the egress allowlist, carrying a `Fix` so `self-check` (FR-207) files it. |
| FR-211 | S | **The `Notifier` seam, the ntfy sender, and the subscriber (ADR-041).** `notify.Notifier` fronts delivery so the Companion-local path can land later; `Ntfy` POSTs the message body with the kind as the `Title` header. **The configured URL is the entire allow-list** — one destination, and redirects are never followed. The host must additionally pass the new `ingestion.CheckEgressPolicy` (egress allowlist only: no ingest lists, and **no `BlockedIPReason`**, so self-hosted ntfy on localhost or a LAN works — a notify URL comes from config, outside every model write path). Payload is `Kind`/`Level`/`Message` only, **never `Event.Data`**, redacted with the profile's `redaction_rules`; **a redactor that fails to compile refuses construction** rather than sending unredacted; body capped at 512 bytes. `Run` mirrors `dashboard.PersistEvents` but delivers behind a bounded queue (64) with a 10s timeout and a 10/minute rate limit, because `events.Bus.Publish` drops rather than blocks on a slow subscriber — a hung POST would otherwise lose events silently. Drops are counted and logged; delivery failures are logged and discarded, never retried, never surfaced as an automation failure. |
```

- [ ] **Step 2: Flip ADR-041 to built**

In `docs/02-architecture.md`, change ADR-041's `*(accepted — planned)*` to `*(accepted — built)*`.

- [ ] **Step 3: Config reference and example**

Document `notify` in `docs/04-data-model-and-config.md` (both fields, the off state, the https rule, the known-kind rule, the egress interaction).

In `axon.config.example.yaml`, add inside the personal profile:

```yaml
    # notify:                                 # push events to an ntfy topic (ADR-041)
    #   url: "https://ntfy.sh/axon-CHANGE-ME" # the ONLY destination; pick an unguessable topic
    #   events:                               # nothing is sent unless a kind is listed
    #     - "automation.fail"
    #     - "token.deny"
    # Both empty = off (the default, both profiles). https is required unless
    # the host is loopback/LAN, so a self-hosted ntfy works. The host must also
    # pass policy.egress_allowlist. Only the kind, level and message are sent —
    # never event data — and redaction_rules apply before sending.
```

Verify: `go run ./cmd/axon config validate --config axon.config.example.yaml`.

- [ ] **Step 4: Component docs, GUIDE, roadmap**

- `docs/09-component-dashboard-observability.md`: the event bus gains a third subscriber (SSE, persistence, notifications) — note that it is opt-in and best-effort.
- `docs/GUIDE.md`: a short "Get told when something happens" section — create an unguessable ntfy topic, add the two config keys, pick kinds; note the caps and that nothing is sent by default.
- `docs/20-roadmap-ai-os.md` B1 → shipped, resolving both open decisions (**ntfy first**, with the Companion path as a later slice behind the same seam; **per-event opt-in by kind**, which subsumes the digest because the briefing is itself an event) and recording the two egress findings (the wildcard default; the IP guard deliberately not applied). Update the sequencing sketch, which names B1 as a remaining theme-opener.

- [ ] **Step 5: CHANGELOG and CLAUDE.md**

`CHANGELOG.md` under `[Unreleased]` → `### Added`:

```markdown
- **AXON can now tell you when something happens.** (FR-210, FR-211,
  **ADR-041**; no schema change.) Point `notify.url` at an ntfy topic and list
  the event kinds you care about, and the daemon pushes them — a failed
  automation, a denied budget, the daily briefing. Off by default: nothing is
  sent unless you name both a destination and at least one kind. Only the kind,
  level and message leave the machine, redacted with your `redaction_rules`;
  a self-hosted ntfy on localhost or your LAN works, and https is required for
  anything public.
```

`CLAUDE.md`: FR range → `FR-01…FR-211`, ADR range → `ADR-001…041`, plus a line recording the slice.

- [ ] **Step 6: Final gate and commit**

```bash
gofmt -l . && go build ./... && go test -race ./... && golangci-lint run
git add -A && git commit -m "docs: FR-210/FR-211, ADR-041 built, notify config reference"
```

---

### Task 7: Live smoke

**Files:**
- Create: `<scratchpad>/notify-smoke/` (throwaway)

- [ ] **Step 1: Build and isolate**

```bash
cd /Users/jandro/Projects/axon/web && npm run build
cd /Users/jandro/Projects/axon
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/notify-smoke
mkdir -p "$S/home/profiles/personal" "$S/vault"
go build -o "$S/axon" ./cmd/axon
cp axon.config.example.yaml "$S/home/config.yaml"
sed -i '' 's/port: 7777/port: 7799/g' "$S/home/config.yaml"
grep -rn 7777 "$S" && echo "STOP: 7777 present" || echo "port clean"
```

`cd` back to the repo root by absolute path before `go build`. Then point the personal profile's `vault_path` at `$S/vault` and `data_dir` at `$S/home/profiles/personal`.

- [ ] **Step 2: Stand up a local receiver**

Write a tiny Go receiver to `$S/recv.go` that listens on `127.0.0.1:7801`, logs the `Title` header and body of every POST to `$S/received.log`, and returns 200. Run it in the background. Set in the config:

```yaml
    notify:
      url: "http://127.0.0.1:7801/axon"
      events: ["automation.fail"]
```

`http://` is correct here and must be *accepted* — loopback is exempt from the https rule. Confirm: `"$S/axon" config validate --config "$S/home/config.yaml"`.

- [ ] **Step 3: Trigger a real notification**

Start the daemon, then cause a subscribed event. The most reliable trigger is a deliberately broken automation — for example set `capture.watch_folders` to a path that exists, then remove read permission (`chmod 000`) so the sweep errors, or run an automation configured with an unreachable model so it fails. Confirm `$S/received.log` gains a line whose Title is the kind and whose body is the message.

If no subscribed event fires within a few minutes, do **not** declare success — pick a kind you can trigger on demand and record which one you used.

- [ ] **Step 4: Confirm redaction and the egress refusal**

Add a redaction rule matching a token you can force into a message, trigger again, and confirm the received body is redacted.

Then set `policy.egress_allowlist: ["localhost"]` with the URL still on `127.0.0.1` — confirm `axon doctor` shows the `notify` check warning and that the daemon logs "destination refused by egress policy". (`127.0.0.1` does not match the literal `localhost` pattern, which is the point: the allowlist is a host-pattern list, not a resolver.)

- [ ] **Step 5: Confirm the off state, then clean up**

Clear `notify.events`, restart, confirm `axon doctor` reports the check off and no POSTs arrive. Confirm the daemon log says `daemon running`, the live daemon on 7777 is untouched (`lsof -ti :7777`), SIGTERM exits cleanly with the pidfile removed. Delete `$S`.

---

## Self-Review

**Spec coverage:** `KnownKinds` and the typo refusal → Task 1; the config surface, all six validation refusals, `Enabled()` → Task 2; the egress-only check, the URL-as-allow-list, no redirects, redaction-refuses-on-bad-regex, the body cap, the thin payload → Task 3; the subscriber, the bounded queue, the rate limit, drop counting, error survival, cancellation, and daemon wiring → Task 4; the doctor check and its `Fix` → Task 5; FR rows, ADR flip, config reference, GUIDE, roadmap, CHANGELOG → Task 6; live smoke including the https-exemption, redaction and egress-refusal paths → Task 7. Out-of-scope items (the Companion path, a digest, retries, capture-back, other providers, `Event.Data`) appear in no task, correctly.

**Type consistency:** `events.KnownKinds`/`IsKnownKind` defined in Task 1, used in Task 2. `config.NotifyConfig{URL, Events}` + `Enabled()` defined in Task 2, read in Tasks 4, 5. `ingestion.CheckEgressPolicy(config.PolicyConfig, string) error` defined in Task 3, called in Tasks 4 and 5. `notify.Note{Kind, Level, Title, Body}`, `notify.Notifier`, `notify.NewNtfy(url string, rules []string) (*Ntfy, error)` defined in Task 3 and used in Task 4. `notify.Run(ctx, bus, kinds, n, log)` defined in Task 4 and called once, in `start_cmd.go`. `maxBodyBytes`, `queueCap`, `rateBurst`, `rateWindow`, `sendTimeout` declared once each.

**Three fragile points, flagged rather than hidden.** (1) Task 2 has `internal/config` import `internal/events` — if that is a cycle, the step says stop and raise it rather than relocating `KnownKinds` silently, because that relocation is a design change. (2) Task 5 has `internal/core` import `internal/ingestion`, which may also be new — the task says check first and inline the comparison if it cycles. (3) Task 4's tests are timing-based by necessity (delivery is asynchronous); they use polling with a deadline rather than fixed sleeps, and must be run under `-race`.
