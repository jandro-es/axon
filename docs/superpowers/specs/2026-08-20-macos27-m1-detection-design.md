# macOS 27 M1 — Detection & doctor (design)

**Status:** approved 2026-08-20 (all three decisions Jandro-picked-recommended:
reject both reserved ref forms deferring naming to M2; license-pending is the
one WARN with a fix line; probe = presence + license + `fm available` summary,
quota deferred to M2). FR-191/FR-192. No new ADR — hardens ADR-015 routing and
follows the FR-185 fix-line convention. Graduates docs/21 M1.

## Reconciliation (what docs/21 didn't know)

The codebase already ships an `apple` generation provider: `agent.AppleFM`
(compiled Swift helper on the FoundationModels framework, ADR-013 pattern),
chokepoint-routed, classify-tier-only (`validateLocalRouting` rejects
routine/synthesis), doctor-covered by a helper-present check. M1 therefore does
**not** introduce Apple-tier detection from scratch; it adds what is missing —
awareness of the macOS 27 **`fm` CLI** — and M2's real open decision becomes
*transport* (existing Swift helper vs `fm serve`), not whether an Apple tier
exists.

Field findings from a real macOS 27.0 machine (Darwin 27, `/usr/bin/fm`):
`fm` refuses everything until a **machine-wide privileged license agreement**
(`sudo fm license`); the refusal arrives with **inconsistent exit codes**
(`fm available` → 0, `fm --help` → 1) and 24-bit-colour ANSI styling. Exit
codes are therefore untrustworthy; state detection must parse stripped output.

## FR-191 — `fm` detection matrix in doctor

`core.DetectFM(ctx)` returns `FMStatus{State, Detail, OSVersion, AppleSilicon}`
with exactly one state:

| State | Trigger | Doctor rendering |
|---|---|---|
| `not-macos` | `GOOS != darwin` | check not emitted at all |
| `os-too-old` | `sw_vers -productVersion` major < 27 | OK — "fm CLI requires macOS 27 (this is X.Y); the on-device `apple` tier is unaffected" |
| `absent` | `exec.LookPath("fm")` fails on ≥ 27 | OK — informational |
| `license-pending` | stripped output carries the license markers ("NOT AGREED" / "fm license") | **WARN**, `Fix: sudo fm license` — the one actionable state. AXON never runs it itself (privileged, machine-wide legal agreement). |
| `ready` | probe succeeds, no license markers | OK — "fm ready: <summary>" (first two non-empty stripped lines, capped) |
| `unresponsive` | probe errors/times out without license markers | OK — informational with capped error |

Probe discipline: license markers are checked **before** the exec error (exit
codes lie); ANSI stripped with one escape-sequence regex; 3 s context timeout +
`WaitDelay` (the dashboard-port probe's paranoia); all persisted output capped.
`sw_vers` failure/parse failure degrades to probing anyway — version display is
best-effort, the binary probe is the real signal. Apple silicon = `GOARCH ==
"arm64"`, reported in Detail. All seams (`lookPath`, `osVersion`, `runFM`)
injectable for table-driven state tests, including the 27-ready state an
unlicensed machine cannot reach.

`fmCheck()` appends after `mediaCheck` in doctor composition, gated
`runtime.GOOS == "darwin"` at the composition site. Advisory: never FAIL.

## FR-192 — reserved Apple ref forms are rejected, not misrouted

Today `models.classify: apple:pcc` (or `apple-fm:x`) parses through
`ParseModelRef` as a **Claude model string** and silently misroutes to
`claude -p`. `validateLocalRouting` now rejects, for every tier string, any
value of the form `apple:<suffix>`, `apple-fm:<suffix>`, or bare `apple-fm`,
with an actionable error: reserved for the macOS 27 Apple-tier work (docs/21
M2); use `apple` (on-device), `ollama:<model>`, or a Claude model. Bare
`apple` behaviour is unchanged. The suffix *naming* decision (extend `apple:`
vs adopt `apple-fm:`) is deliberately deferred to M2.

## Out of scope (M2+)

`fm serve`/tier wiring, PCC anything (including `fm quota-usage`), eval
admission, vision tier (M3), any config key. Zero behaviour change on every
existing path; non-macOS builds unaffected.

## Verification

Table-driven tests over the injected seams for all six states + ANSI/caps +
marker-before-error ordering; validation table tests for accepted/rejected
refs; live smoke on the real macOS 27 machine (unlicensed → genuine WARN +
fix; real `sw_vers`). The ready-state live smoke requires the human-run
`sudo fm license` and is optional for acceptance.
