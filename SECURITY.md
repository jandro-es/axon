# Security Policy

AXON is a local-first system that handles your notes, secrets (Claude OAuth
tokens) and outbound network requests, so we take security seriously.

## Reporting a vulnerability

**Please do not open public issues for security problems.**

Report privately via one of:

- GitHub's **private vulnerability reporting** ("Report a vulnerability" under the
  repository's *Security* tab), or
- email **jandro@filtercode.com** with details and, ideally, a reproduction.

We'll acknowledge receipt, investigate, and coordinate a fix and disclosure
timeline with you.

## Scope and threat model

AXON runs as a single local daemon per profile. Its security posture (NFR-05):

- **Secrets** live in `.env`/the OS keychain, referenced by name; they are never
  logged, never written to the ledger/events, and never sent to the model.
- **No Claude call bypasses the token manager**, and `ANTHROPIC_API_KEY` is
  refused on subscription/enterprise profiles (`axon doctor` warns).
- **Vault writes are sandboxed and wikilink-safe** — the vault filesystem rejects
  `..`/absolute path traversal; there is no delete; writes are atomic.
- **Ingestion treats fetched content as data, not instructions**: egress is
  allow-listed, redirects are re-validated per hop, link-local/metadata IPs are
  blocked, redaction runs before persistence/model, and agent-driven ingestion of
  local files is refused.
- **The dashboard binds to loopback only**, holds no secrets, and rejects
  non-loopback `Host` headers (anti DNS-rebinding).

The interactive `PreToolUse` hook that discourages raw `rm`/`mv` in Claude Code
sessions is **defense-in-depth for an honest agent**, not a sandbox against a
malicious one; the structural guarantee is that AXON's own writes are
wikilink-safe.

## Supported versions

This is pre-1.0 software; security fixes target the latest `main`. Pin a commit
if you need stability and watch the repository for advisories.
