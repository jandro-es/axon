# Outbound notifications — the daemon reaches the owner (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
**ntfy first** with a `Notifier` seam for the Companion path later; **per-event,
opt-in by kind, empty default** rather than a digest; **config-sourced URLs skip
the IP guard**, so self-hosted ntfy on localhost or a LAN works).
**FR-210, FR-211; ADR-041.** Graduates `docs/20` **B1**.

**No migration.** Schema stays `0007`. No new automation — built-ins stay **26**.

## The idea

The briefing, the review queue and the "Needs you" panel all wait to be opened.
B1 asks for the inverse: the daemon telling the owner something happened.

The machinery is nearly all present. `events.Bus` already carries every
interesting moment (`automation.fail`, `token.deny`, `automation.briefing`,
`ingest.done`, …), and `dashboard.PersistEvents` is an existing subscriber to
copy: subscribe, select on `ctx.Done()` and the channel, never propagate an
error upward. What is new is one HTTP POST and the rules around it.

**Why this needs an ADR.** Every outbound connection AXON makes today is a
*pull* the owner or an automation initiated — an ingest fetch, a Claude call,
an Ollama request. This is the first **push**: the daemon sending vault
activity to a third party on its own initiative. The constitution's rule 2
("egress passes the policy engine or it does not happen") has to be
*interpreted* for that shape rather than reused, because the policy engine was
written for ingest — and two facts force the interpretation.

**Fact one: the default `egress_allowlist` is `["localhost", "*"]`.** A
wildcard. Treating it as *the* guard would be vacuous on a default personal
install.

**Fact two: `ingestion.BlockedIPReason` refuses loopback and private
addresses** — precisely where a self-hosted ntfy lives. Applying it verbatim
would break the most privacy-preserving deployment and push users toward the
public `ntfy.sh`, which is backwards for a local-first system.

## FR-210 — Config, validation, and the egress rules

### The config surface

```yaml
notify:
  url: "https://ntfy.sh/axon-a8f3c2d1"     # empty ⇒ off
  events:                                  # empty ⇒ off
    - "automation.fail"
    - "token.deny"
```

`Profile.Notify NotifyConfig` with `URL string` and `Events []string`. **Both
empty is the default on both profiles**, so the feature ships disabled with no
separate toggle — the `watch_folders` pattern.

### Validation, at config load

`validateNotify(p Profile) error` in the `Config.Validate` per-profile loop,
beside `validateWatchFolders`:

| refusal | why |
|---|---|
| `events` set but `url` empty, or `url` set but `events` empty | a half-configured notifier is silent, and silence is indistinguishable from working |
| `url` not parseable, or scheme not `http`/`https` | no `file://`, no shell-adjacent schemes |
| `http://` to a non-loopback, non-private host | a plaintext push of vault activity to a public host should not be reachable by typo |
| an event kind not in the known set | a typo'd kind is silent forever |

The known-kind check is the one that earns its keep: `notify.events:
["automation.failed"]` (past tense — the real kind is `automation.fail`) would
otherwise produce a notifier that is configured, enabled, and never fires.

**Where the known set lives, and its one risk.** Event kinds are string
literals at their emitters today, so the list has to be written down
somewhere. It goes in **`internal/events`** as an exported
`KnownKinds` slice — beside the `Event` type and as close to the emitters as
the current structure allows — not in `internal/notify`, which would put it a
package away from everything that could invalidate it. The risk is real and
worth stating: a **new** event kind added without updating the list would be
refused by `notify.events` validation until someone notices. That is the
deliberate trade — a loud refusal at config load beats a notifier that is
enabled and permanently silent — and a test asserts every kind in `KnownKinds`
is non-empty and unique, so at least the list cannot rot into duplicates.

### The egress rules, stated precisely

- **The configured URL is the allow-list.** AXON sends there and nowhere else.
  There is no discovery, no redirect-following, and no second destination.
- **The host must additionally pass `egress_allowlist`** (via the existing
  matcher), so a work profile's strict `["localhost"]` still refuses
  `ntfy.sh`. The wildcard default never stands alone: the URL had to be named
  in config to exist at all.
- **`BlockedIPReason` is deliberately not applied.** It exists because a
  prompt-injected agent can influence an *ingest* URL. A notify URL comes from
  `config.yaml`, outside every model write path (ADR-039). Applying it would
  block localhost and LAN targets — the best case — while defending against a
  threat that cannot occur here. Recorded in ADR-041 so it does not read as an
  oversight.

### The doctor check

A `notify` check: `off` when either field is empty; OK naming the host and the
subscribed kind count; **warn** when the host fails `egress_allowlist`, with
`Fix: "add the host to policy.egress_allowlist, or clear notify.url"`. It
carries a `Fix`, so `self-check` (FR-207) files it automatically.

## FR-211 — The `Notifier` seam, the ntfy sender, and the subscriber

### The seam

```go
// internal/notify
type Note struct {
	Kind    string
	Level   events.Level
	Title   string
	Body    string
}

type Notifier interface {
	Send(ctx context.Context, n Note) error
}
```

One implementation ships: `Ntfy{URL string, HTTP *http.Client}`, an HTTP POST
of the body with the title in the `Title` header. The interface exists so the
Companion-local path (macOS user notifications raised from the SSE stream, no
egress at all) can land later without touching the subscriber — and so tests
use a fake rather than a live host.

### The payload is deliberately thin

Only `Kind`, `Level` and `Message` leave the machine. **Never `Event.Data`** —
its fields are arbitrary and emitter-defined, so a future emitter could put a
path, a query or a note excerpt there, and a notification payload is the last
place that should inherit that by default.

Title and body are redacted with the profile's `redaction_rules` through the
existing `ingestion.NewRedactor` before send. **If the redactor fails to
compile, the send is refused** rather than proceeding unredacted — the failure
mode of a bad regex must not be "your data goes out unfiltered". Body is
capped at 512 bytes.

### The subscriber, and the two failure modes the bus creates

`Run(ctx context.Context, bus *events.Bus, cfg Config, n Notifier, log *slog.Logger)`
mirrors `dashboard.PersistEvents`: subscribe, select on `ctx.Done()` and
`sub.C`, return on either, never propagate an error.

`events.Bus.Publish` **drops rather than blocks** when a subscriber's channel
is full ("never block a publisher on a slow subscriber"). That is the right
behaviour for the bus and it creates two problems here:

- **A hung POST loses events silently.** Delivery therefore runs behind a
  bounded internal queue (capacity 64) with a 10-second request timeout, so the
  bus-facing side of the subscriber never waits on HTTP. A full queue drops,
  counts, and logs.
- **A burst spams the host.** A token bucket caps delivery at 10 per minute.
  Dropped notifications are counted and logged, never silently discarded — a
  silent cap reads as "nothing happened", which is exactly the failure this
  feature exists to prevent.

Delivery failures are logged and dropped: never retried, never surfaced as an
automation failure, never allowed to propagate. A notification is best-effort
by definition, and a notifier that can break the daemon is worse than no
notifier.

### Wiring

Started in `cmd/axon/start_cmd.go` beside the existing event subscribers, only
when the profile's notify config is complete. Nothing else in the daemon knows
it exists.

## Out of scope

- **The Companion-local path.** The seam is here; the Swift side is its own
  slice with its own release cycle.
- **A digest.** Per-event-kind opt-in subsumes it: the daily briefing is an
  event, so pushing it is one list entry. A second delivery path with its own
  schedule and dedup semantics is not justified.
- **Retries and delivery guarantees.** Best-effort, by decision.
- **Capture-back** (replying to a notification to file something) — that is
  `docs/20` B2, and it is an ingress with an entirely different threat model.
- **Any provider beyond ntfy**, and **`Event.Data` in the payload**.

## Verification

**Unit — config:** a `validateNotify` table covering both-empty (valid, the
default); `events` without `url` and vice versa; an unparseable URL; a
`file://` scheme; `http://` to a public host (refused) and to `localhost` and a
private IP (allowed); an unknown event kind; a valid full config.

**Unit — the sender:** against an `httptest.Server` — the POST reaches the
configured path, the title header is set, the body is the message; a redaction
rule rewrites the body before it leaves; a redactor that fails to compile
refuses the send rather than sending unredacted; a body over the cap is
truncated; `Event.Data` never appears in the request.

**Unit — the subscriber:** an event of a subscribed kind is delivered; an event
of an unsubscribed kind is not; a slow notifier does not block the bus (publish
many, assert `Publish` returns promptly and the queue drops with a count); the
rate limiter caps delivery and logs the drop; a `Send` error does not stop the
loop — a subsequent event is still delivered; `ctx` cancellation returns.

**Unit — egress:** a host absent from a non-wildcard `egress_allowlist` is
refused before any request is made (assert with a test server that would
otherwise record a hit); a loopback URL is allowed with no `BlockedIPReason`
involvement.

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**; isolate `vault_path` and `data_dir` too, not just `AXON_HOME`;
`mkdir -p` the data dir or SQLite fails with `unable to open database file
(14)`; `cd` back to the repo root by absolute path after `cd web && npm run
build`; run `go test -race ./...` before pushing, since CI does): point
`notify.url` at a local `httptest`-style listener (a `nc -l` or a tiny Go
server on 127.0.0.1), trigger a subscribed event, and confirm the POST arrives
with a redacted body; set a strict `egress_allowlist` and confirm the same
config now refuses to send; confirm `axon doctor` shows the `notify` check in
all three states and that `self-check` files the warning.

## Docs to update on completion

`docs/03` (FR-210/FR-211 rows), `docs/02` (ADR-041 planned → built at the cut),
`docs/04` (the `notify` config reference), `docs/09-component-dashboard-observability.md`
(the event bus gains a third subscriber), `docs/GUIDE.md` (a short "get told
when something happens" section), `axon.config.example.yaml` (a commented
`notify` block), `docs/20` B1 (shipped, with both open decisions resolved and
the two egress findings recorded), and `CHANGELOG.md` `[Unreleased]`.
