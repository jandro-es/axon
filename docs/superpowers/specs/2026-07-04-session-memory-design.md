# Session memory capture — design

**Date:** 2026-07-04
**Status:** approved (brainstormed + user-approved)
**Traces to:** new FR-97…FR-99 (docs/03), ADR-011 (identity layer), ADR-015 (classify tier local routing), Component 12 (docs/12), NFR-14, new ADR-021

## Goal

Answer "what did we decide last week": finished Claude Code sessions in the
vault are distilled — through the chokepoint, on the cheapest tier — into
durable decision/lesson/preference entries in the identity MEMORY note,
where the SessionStart injection already surfaces them to every future
session.

## Background / constraints

- Hooks never call models and must never block or break a session; the Stop
  hook currently emits a static advisory line and ignores its input. The
  hook process has the DB open (`hooks.Deps.DB`).
- Claude Code sends `transcript_path` (+ `session_id`) on every hook event;
  the `Input` struct doesn't parse it yet. Transcripts are JSONL under the
  profile's Claude config tree; the hook-provided path is the only resolver.
- `identity.Remember(ctx, v, Entry{Text, Kind, Source, Date})` appends dated
  entries to MEMORY.md's `axon:memory` block (wikilink-safe, newest-first);
  `memory-distill` produces/compacts the same format and keys provenance on
  `source:`. `Entry.Kind` already models decision|lesson|preference.
- `automation_state` namespaced JSON rows are the established bookkeeping
  pattern (capture, resurfacer, subscriptions).
- Stop fires at the end of EVERY assistant turn, not just session end — the
  recorder must upsert, and the distiller must wait for idleness.
- NFR-14: the memory layer is the most personal data; transcripts more so.

## Decisions (user-approved)

1. **Default ON**, mirroring `memory.inject`: `memory.capture_sessions`
   pointer-default-ON; the work profile opts out in config. Only vault
   sessions are captured (per-vault hook wiring); redaction applies before
   the model sees transcript text and before entries are written.
2. **Full MEMORY vocabulary**: the distiller extracts up to 3 items per
   session as decision | lesson | preference (or NONE).

## Design

### Stop-hook recorder (`internal/hooks`)

- `Input` gains `TranscriptPath string \`json:"transcript_path"\``.
- Dispatch passes input+deps: `case Stop: return stop(ctx, in, deps)`.
- `stop` keeps the existing advisory stdout; additionally, when
  `deps.Memory.SessionCaptureEnabled()` and `in.SessionID != "" &&
  in.TranscriptPath != ""` and `deps.DB != nil`: load the
  `session-distill:pending` row (JSON `map[string]pendingSession{
  TranscriptPath string; LastStop string(RFC3339)}`), upsert this session
  with now, cap the map at 50 newest, save. Every error path is silent
  (hook rule). No transcript content touches the DB — paths only.

### The `session-distill` automation (`internal/automations/sessionmem.go`)

- Registry name `session-distill`; not essential; one classify-tier call per
  session (ADR-015: local-routable; `ValidateOutput` gives local models the
  retry/fallback ladder).
- **DetectChange:** load pending; drop entries whose session is not yet
  idle (`LastStop` newer than 30 min) from consideration; cursor =
  short hash of the sorted ready session ids; skip when none ready or
  cursor unchanged.
- **Run**, per ready session (all bounded):
  1. Read the transcript file (missing/unreadable → mark seen, count
     skipped). Parse JSONL: keep human-visible text — user messages and
     assistant text blocks (tool payloads and thinking skipped; exact JSONL
     shapes verified against a live transcript during implementation — the
     parser is one function, like the stream-json parser).
  2. Tail-cap the extracted conversation to ~8000 chars (respecting
     `budget_tokens` pre-flight; the newest exchange matters most).
  3. Apply profile redaction rules, wrap in the standard data fences with
     `NeutralizeDelimiters`.
  4. One `runModel` call, `Operation: "automation.session-distill"`,
     `ModelKey: "classify"`: "Extract up to 3 durable items worth
     remembering across sessions, each on one line as
     `decision|lesson|preference: <text>`; reply NONE if nothing durable."
     `ValidateOutput` accepts NONE or 1-3 correctly-prefixed lines.
  5. Budget defer → stop processing further sessions this run (they stay
     pending); parse each line → `identity.Remember(Entry{Text, Kind,
     Source: "session", Date: today})`.
  6. Move session id from pending to the `session-distill:seen` set
     (mark-seen-after-attempt, one try per session; seen set capped at 200
     newest).
  - Dry-run: report ready sessions + estimate; no reads beyond the pending
    row, no model call (Authorize-only via runModel), no state changes.
  - Summary: `"distilled N session(s): M entr(ies) remembered, K empty, J
    skipped"`.
- Starter config: `session-distill: {enabled: true, schedule: "15 */2 * * *",
  model: classify, budget_tokens: 30_000, catch_up: skip}`.

### Config (`internal/config`)

`MemoryConfig` gains `CaptureSessions *bool \`yaml:"capture_sessions"\`` +
`SessionCaptureEnabled() bool` (pointer-default-ON, mirroring
`InjectEnabled`). Reaches the hook via the existing `deps.profile.Memory`.

### Privacy (NFR-14)

Vault sessions only (per-vault hook wiring); paths-not-contents in SQLite;
redaction before the model and before writes; `memory.capture_sessions:
false` kills the recorder (and the distiller finds nothing);
`policy.allowed_automations` can exclude `session-distill` independently;
entries are human-visible and editable in MEMORY.md, and memory-distill's
compaction curates them over time. A poisoned transcript is data inside
fences — worst case one capped classify call.

### Testing

- hooks: Stop with capture on/off, missing fields, DB row upsert + cap,
  advisory line preserved, errors silent (nil DB).
- config: pointer-default-ON accessor.
- automation: table-driven with fixture JSONL transcripts in a temp dir —
  extraction parsing (user+assistant text only), tail cap, idle threshold
  (fresh session not processed), NONE → no entries + seen, 2 entries →
  MEMORY.md gains both with `(source: session)`, one-try-per-session across
  runs, budget defer leaves sessions pending, dry-run makes no call, seen
  cap, missing transcript → skipped+seen.
- Registration tests (registry 15, catalog, mcp count).
- Live smoke: scratch vault — simulate a Stop hook via
  `axon hook Stop < payload.json` with a hand-written JSONL transcript in
  the scratchpad, then `axon run session-distill` (real classify call) and
  inspect MEMORY.md.

### Docs

- **ADR-021**: "Session memory via a recording hook and a distilling
  automation" — the split is forced by the no-model-hooks rule; records
  idle-threshold, mark-seen-after-attempt, caps, and the NFR-14 posture.
- **FR-97…FR-99** in docs/03:
  - FR-97 (M): Stop-hook session recorder (deterministic, silent-failure,
    paths only, toggle-gated).
  - FR-98 (M): session distiller — one classify-tier chokepoint call per
    idle session, once ever per session, entries via `identity.Remember`
    with `source: session`; budget defer leaves sessions pending.
  - FR-99 (M): privacy controls — `memory.capture_sessions` default-ON
    pointer toggle, redaction before model and writes, vault-sessions-only.
- docs/12 (Component 12) section; config reference; CHANGELOG.

## Trade-offs accepted

- Stop fires per turn, so the recorder writes a small DB row frequently
  during active sessions (an upsert of a tiny JSON map — negligible).
- One-try-per-session means a transiently failed distill (unreadable file)
  is not retried; the session's insights are recoverable manually via
  `memory_remember`.
- The 30-minute idle threshold delays capture of long-running sessions
  until they pause — intended (don't distill half a conversation).

## Out of scope

- Non-vault sessions; retroactive capture of sessions before this feature.
- Full-transcript archival into the vault.
- Per-entry human review before writing (MEMORY.md is the review surface).
- SessionEnd-based capture (Claude Code's Stop suffices; SessionEnd wiring
  can replace the idle heuristic later if it proves worthwhile).
