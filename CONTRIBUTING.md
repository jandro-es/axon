# Contributing to AXON

Thanks for your interest in AXON. This document covers how to build, test and
contribute.

## Development setup

Prerequisites: **Go 1.26+**, **Node 18+** (to build the dashboard), and — to run
the daemon meaningfully — the **Claude Code CLI** and **Ollama**.

```bash
git clone https://github.com/jandro-es/axon.git && cd axon
make            # builds the dashboard SPA + the binary
make test       # run the test suite
make race       # run with the race detector
```

`go build ./...` works without the SPA build (the dashboard serves a fallback
page until `web/dist` is built).

## Project layout

```
cmd/axon/      the CLI (the only package main; wires cobra)
internal/      private application packages:
  config db vault embeddings agent tokens ingestion search
  automations scheduler mcp hooks dashboard core scaffold claudeassets service events
web/           the Vite + React + Recharts dashboard (built to web/dist, embedded)
docs/          PRD, architecture + ADRs, requirements, component specs, and GUIDE.md
```

Dependency rule: `internal/config` is imported by everyone and imports nothing
internal. Leaf packages don't import each other's callers; `core` and the CLI
compose them. Go enforces an acyclic graph — fix a cycle, don't work around it.

## Coding conventions

- `gofmt`/`goimports` clean; `go vet` green. Run `make fmtcheck vet` before pushing.
- Wrap errors with `%w`; don't panic in library code. Propagate `context.Context`
  through all I/O and Claude/Ollama calls.
- Prefer small interfaces defined at the consumer; table-driven tests.
- Every change should have tests. Run `make race` for anything touching
  concurrency.

## The two cardinal rules (never violate)

1. **No Claude call bypasses the token manager.** Every path that reaches Claude
   goes through `internal/tokens` `Manager.Run`. The only Claude adapter is
   `internal/agent`. A PR that calls the agent directly will be rejected.
2. **No vault mutation that isn't wikilink-safe.** All vault writes go through the
   `internal/vault` helpers (atomic, sandboxed); renames use `Move` (rewrites
   inbound links); edits use `Patch` (managed blocks). There is no delete.

## Traceability

The design lives in `docs/` — the requirements (`docs/03-requirements.md`,
`FR-*`/`NFR-*`) are the contract, and the architecture decisions are in
`docs/02-architecture.md` (`ADR-*`). When you add or change behaviour, reference
the relevant IDs in your PR, and add an ADR for any significant architectural
deviation.

## Pull requests

- Keep PRs focused; describe what changed and why, and which `FR/NFR` it touches.
- Ensure `make fmtcheck vet test` passes (CI runs `race` too).
- Be kind and constructive in reviews.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md). Do not
open public issues for security problems.

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
