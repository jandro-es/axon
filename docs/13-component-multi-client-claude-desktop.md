# 13 — Component: Multi-client Integration (Claude Desktop) *(planned — Phase 9)*

**Owns:** FR-74, FR-75, FR-76, ADR-012.
**Goal:** Let AXON's second brain be used from **Claude Desktop** as well as Claude
Code, through the same MCP server, while being honest about which guarantees
travel to each client.

> Status: **planned.** This spec defines the design; it is not yet built. It
> reuses the Phase 5 MCP server and the `claudeassets` generation pattern.

## 1. One server, many clients

AXON already ships a standard stdio MCP server (`axon mcp`, Component 08). Any
MCP client can launch it; the registration JSON shape is identical across
clients — only the file location and the surrounding feature set differ.

```
                     ┌──────────────── axon mcp (stdio) ────────────────┐
  Claude Code  ──────┤  vault_search/read/write/patch/move/links,        │
  Claude Desktop ────┤  daily_append, knowledge_ingest/search,           │
                     │  tokens_status, automations_list/run,             │
                     │  memory.remember (Component 12)                    │
                     └───────────────────────────────────────────────────┘
```

## 2. Wiring Claude Desktop

`axon mcp install --client desktop` (and `--print` to preview) generates a
**profile-scoped** `mcpServers` entry and merges it **non-destructively** into
Claude Desktop's config (preserving any servers already there):

```jsonc
// claude_desktop_config.json  (platform path resolved by AXON)
{
  "mcpServers": {
    "axon": {
      "command": "/abs/path/to/axon",
      "args": ["mcp", "--config", "/abs/axon.config.yaml", "--profile", "personal"],
      "env": { "CLAUDE_CONFIG_DIR": "…", "AXON_HOME": "…" }
    }
  }
}
```

- **Config path** (resolved per OS): macOS `~/Library/Application Support/Claude/claude_desktop_config.json`;
  Windows `%APPDATA%/Claude/claude_desktop_config.json`; Linux `~/.config/Claude/claude_desktop_config.json`.
  (Verify against the current Claude Desktop docs at build time; isolate the path
  logic in one place, as with `claude -p`/hook schemas.)
- **Merge, don't clobber:** read the existing JSON, add/replace only the `axon`
  key, write back. `--print` emits the entry for manual pasting.
- **Profile-scoped & isolated:** the entry carries `--profile`, the absolute
  config path, and the profile's `CLAUDE_CONFIG_DIR`/`AXON_HOME` (NFR-04).
- This is the same block `axon init` writes for Claude Code's project `.mcp.json`
  (Component 08) — so onboarding (Component 12) can offer both from one prompt.

## 3. Capability matrix (be honest — FR-75)

| Capability | Claude Code | Claude Desktop |
|---|---|---|
| AXON MCP tools (vault/knowledge/tokens/automations/memory) | ✅ | ✅ |
| `SessionStart` status + **profile injection** (FR-72) | ✅ | ❌ (no hooks) |
| `PreToolUse` block of unsafe ops over the **client's built-in** file tools | ✅ | ❌ |
| Plugin: skills + subagents + generated `CLAUDE.md` | ✅ | ❌ |
| Headless `claude -p` automations | ✅ | ❌ |
| **Wikilink-safety of AXON's own tools** | ✅ | ✅ (enforced in the server, not the client) |

The critical point: **AXON's own tools are wikilink-safe and path-sandboxed in the
server**, so vault safety for AXON operations does *not* depend on the client's
`PreToolUse` hook. On Desktop you lose the session profile injection and the guard
over Desktop's *built-in* file editing — so the guidance (in the generated
`CLAUDE.md` and the docs) is: **do all vault mutation through the AXON tools.**

## 4. `doctor` reporting (FR-75)

`axon doctor` detects installed/known clients and reports, per client:
- whether the AXON MCP server is registered (and for which profile),
- for Desktop, a one-line note: *"tools available; no hooks/skills/profile
  injection — keep vault edits in AXON tools."*

## 5. Concurrency (FR-76)

Multiple clients (Code + Desktop) may target the same profile/vault at once.
SQLite is single-writer (`SetMaxOpenConns(1)` per process), and the **daemon
remains the owner of scheduled writes**; interactive MCP writes from either client
go through the same wikilink-safe, atomic vault ops. Document that a heavy
concurrent write workload is serialised, and that the daemon (`axon start`) should
be the one running automations.

## 6. Interop note (stretch — FR-54)
The same seam allows pointing a client at a community Obsidian MCP server as an
alternative vault backend behind AXON's tool contract; AXON's own server remains
the default (ADR-005).

## 7. Acceptance checks
- `axon mcp install --client desktop` writes a profile-scoped `axon` entry into
  `claude_desktop_config.json` without removing existing servers; `--print`
  previews it (FR-74).
- From Claude Desktop, `vault_search`/`vault_read`/`vault_write`/`vault_move`/
  `knowledge_ingest`/`tokens_status` work against the active profile.
- `axon doctor` reports Desktop as a tools-only client with the reduced-guarantee
  note (FR-75).
- A `vault_move` from Desktop still rewrites inbound links (safety enforced in the
  server, not the client) — zero broken wikilinks (S5).
