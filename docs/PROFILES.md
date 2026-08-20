# Personal and work profiles

AXON's answer to "one brain at home, another at the office" is **profiles** —
and it's important to understand what a profile is: **a configuration
convention, not a code path.** Nothing in the daemon branches on the profile
name; `"personal"` and `"work"` are just the two shapes the example config
teaches. Everything below is enforced by generic policy machinery that reads
whatever profile is active.

## The model

- **One installation runs one active profile.** Personal and work are separate
  installations (separate config, data dir, vault, Claude login, service
  unit). Nothing is shared across profiles — not the database, not the token
  ledger, not the OAuth token, not the automation set.
- **Resolution order** for the active profile:
  `--profile` flag → `AXON_PROFILE` env → `config.active_profile` → `default`.
- Each profile fully isolates: data dir, secrets (`CLAUDE_CONFIG_DIR`,
  `auth_mode`, OAuth token), policy block, and automation set. `axon profiles`
  shows the resolved paths.

## Personal vs work at a glance

This is the shape `axon.config.example.yaml` demonstrates. Every row is plain
config — copy and adjust.

| Dimension | `personal` | `work` |
|---|---|---|
| `claude.auth_mode` | `subscription` (Max plan) | `enterprise` (SSO via `claude login`) |
| OAuth token | `env:CLAUDE_CODE_OAUTH_TOKEN_PERSONAL` | `env:CLAUDE_CODE_OAUTH_TOKEN_WORK` (may be unset if the org bans long-lived tokens) |
| API key | must **not** be set | none — org exposes no Console API |
| Embeddings | `ollama` / `nomic-embed-text` / dim 768 | `ollama` / `bge-m3` / dim 1024 (different model ⇒ a separate index) |
| Model tiers | haiku / sonnet / opus | haiku / sonnet / sonnet — no Opus |
| Budgets | 1.5M day / 8M week, guard at 80% | 600k day / 3M week, guard at **70%** |
| Retrieval | top-k 8, 12k context | top-k 6, 8k context |
| `egress_allowlist` | `["localhost", "*"]` | `["localhost"]` — no wildcard |
| `ingest_domains_allow` | `["*"]` | explicit hosts only |
| `ingest_domains_deny` | `[]` | `["*"]` — **deny by default** |
| `redaction_rules` | `[]` | client-name and credential patterns, scrubbed before anything reaches Claude |
| `allowed_automations` | `["*"]` | a short strict list (`heartbeat`, `daily-log`, `inbox-triage`, `knowledge-reindex`, `budget-guard`) |
| Automation overrides | full default set | `compaction`, `knowledge-digest`, `research-questions`, `link-suggester` off |
| `memory.inject` | `true` — USER/SOUL/MEMORY injected at SessionStart | **`false`** — identity stays in the vault, never auto-injected |
| `research.enabled` | opt-in (`false` until you enable it) | unset — deep research never runs on work |
| `data_residency` | `local-only` | `local-only` |

## How it's enforced

Determinism over good intentions: every row above is enforced in code, not by
asking the model nicely.

- **`allowed_automations` is a hard gate.** `AllowedByPolicy()` in the
  automation registry treats an empty list or `"*"` as permit-all; a non-empty
  list is a strict allow-list checked *in addition to* `enabled:`. An
  automation you enable but don't allow stays off, and
  `axon automations` shows `enabled` and `allowed` separately.
- **Egress and ingestion policy** (`internal/ingestion/policy.go`): precedence
  is deny-list → explicit allow → `ingest_domains_allow` (a non-empty,
  non-wildcard list means deny-by-default) → `egress_allowlist` as the network
  backstop. Loopback, private and link-local addresses are **always** refused
  (SSRF guard), even under `"*"`, and the check is re-applied after every
  redirect.
- **Redaction** runs before content leaves the machine: the profile's
  `redaction_rules` are threaded into both the Claude Code hooks and the
  ingestion pipeline, so matched patterns are scrubbed before any model sees
  the text.
- **Auth is guarded both ways.** `doctor` warns when a stray
  `ANTHROPIC_API_KEY` is present in `subscription`/`enterprise` mode (Claude
  Code would silently divert onto API billing), and the subprocess adapter
  strips that variable from the child environment at runtime. `doctor`'s auth
  check knows the four states: token+session (ok), token only (headless
  ready), session only (warn — scheduled automations will fail; run
  `claude setup-token`), neither (warn). Exact `count_tokens` and dollar cost
  exist only in `api_key` mode; subscription/enterprise ledger tokens with
  cost recorded as null.

## Practical guidance

- Starting fresh: `axon setup` writes a personal-shaped single-profile config.
  For a work machine, copy the `work` profile block from
  `axon.config.example.yaml` and adjust hosts, models and budgets.
- The work posture in one sentence: **deny egress by default, redact before
  send, allow-list the few automations you want, and keep `memory.inject`
  off.** Loosen deliberately, per line, rather than starting open.
- Don't run two profiles against one vault or one data dir — isolation is the
  point, and the single-instance guard will fight you.
