# System-1

System-1 is a subconscious runtime for AI agents.

This repo is the Go implementation of the first thin vertical slice:

- single daemon
- single agent
- file backend only
- startup Waking Mind
- MCP-first Introspection

## Current status

This repository is intentionally scaffold-first.

The immediate goal is to prove the System-1 MVP loop:

1. ingest one agent's session logs
2. build turn-based spans
3. extract candidate artifacts conservatively
4. run policy, dedup, and deferral
5. persist to file backend
6. assemble ambient context + Waking Mind at session start
7. answer `introspect(...)` queries with grounded recall

## Repo shape

```text
cmd/system1              # binary entrypoint
internal/app             # top-level wiring
internal/cli             # cobra commands
internal/config          # explicit config loading
internal/logging         # slog setup
internal/daemon          # root runtime loop
internal/artifacts       # core structs
internal/ingest          # log watching + span building
internal/extract         # candidate extraction
internal/policy          # approval / reject / defer / dedup
internal/backend/file    # JSON artifacts + SQLite sidecar
internal/session         # ambient context + Waking Mind
internal/introspect      # retrieval + synthesis entrypoints
internal/mcp             # MCP-facing tool surface
internal/obs             # status and traces
testdata                 # fixtures
```

## CLI

Current scaffold commands:

- `system1 serve`
- `system1 doctor`
- `system1 session start`
- `system1 session end`
- `system1 introspect "..."`

These commands are intentionally thin. The real conscious-agent interface remains MCP-first.

## Build

```bash
make build
make test
```

## Review automation

For PRs with CodeRabbit feedback, this repo includes a helper script that:

- fetches CodeRabbit "Prompt for AI Agents" blocks from a PR
- skips resolved review-thread comments by default
- runs one non-interactive `opencode run` per prompt
- defaults to patch-and-commit mode without extra verification
- pushes the branch once at the end

Example:

```bash
./scripts/coderabbit-opencode-cycle.sh 13 --repo XferOps/system1 --dry-run
./scripts/coderabbit-opencode-cycle.sh 13 --repo XferOps/system1
./scripts/coderabbit-opencode-cycle.sh 13 --repo XferOps/system1 --verify
```

By default, the script skips the aggregate "Prompt for all review comments..." block to avoid duplicating the individual prompt runs.
Use `--include-resolved` if you intentionally want to re-run prompts from resolved review threads.

## MVP principles

- core generic, defaults narrow
- type registry stays generic even if MVP only enables `MEMORY` and `KNOWLEDGE`
- introspection is MCP-first, CLI-mirrored for development and debugging
- no fake abstractions beyond what the MVP needs

## MVP explicitly out of scope

The MVP deliberately excludes:

- multi-agent multiplexing (single agent only)
- Hizal backend integration (file backend only)
- dynamic focus-shift-driven ambient refresh (startup-only)
- rich introspection depth beyond one configurable extra pass
- setup wizard and runtime reconfiguration
- local model support and host capability detection
- production-grade security hardening for multi-agent isolation

These will be addressed in post-MVP tickets.

See `VISION.md` for the product direction.
