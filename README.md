# System-1

System-1 is a subconscious runtime for AI agents.

Its job is to reduce the memory-management and tool-orchestration burden of the conscious agent so the foreground agent can spend less time managing context and more time doing useful work.

This repository is the Go implementation of the first thin vertical slice. Today it focuses on a small but real end-to-end loop:

- single daemon
- single agent
- file backend and Hizal backend (both active)
- real daemon loop: ingest -> extract -> policy -> persist
- OpenCode auto-discovery (JSONL and SQLite)
- startup Waking Mind
- MCP-first Introspection

## Current status

This repository is still MVP-thin, but it is no longer just scaffold.

In plain terms: this repo prototypes the subconscious layer so the main agent can stay simple. If a feature does not clearly reduce conscious-agent context or tool overhead, it is probably not the right priority.

The active MVP loop in daemon runtime is:

1. ingest one agent's session logs
2. build turn-based spans
3. extract candidate artifacts conservatively
4. run policy, dedup, deferral, and early silent-rectification routing
5. persist artifacts to a backend
6. assemble ambient context + Waking Mind at session start
7. answer `introspect(...)` queries with grounded recall

## Repo shape

```text
cmd/system1              # binary entrypoint
internal/app             # top-level wiring
internal/cli             # cobra commands
internal/config          # explicit config loading
internal/logging         # slog setup
internal/daemon          # runtime loop + ingestion/extraction/policy orchestration
internal/artifacts       # core structs
internal/ingest          # source discovery + ingest + span building
internal/extract         # candidate extraction
internal/policy          # approval / reject / defer / dedup / update-existing routing
internal/backend/file    # JSON artifacts + SQLite sidecar
internal/session         # ambient context + Waking Mind
internal/introspect      # retrieval + synthesis entrypoints
internal/mcp             # MCP-facing tool surface
internal/obs             # status and traces
testdata                 # fixtures
```

## CLI

Current commands:

- `system1 serve`
- `system1 doctor`
- `system1 session start`
- `system1 session end`
- `system1 introspect "..."`

These commands are intentionally thin. The real conscious-agent interface remains MCP-first.

## Logging

- Daemon logs go to `stdout` by default (text format).
- Default level is `info`.
- Configure with:
  - `SYSTEM1_LOG_LEVEL` = `debug|info|warn|error`
  - `SYSTEM1_LOG_FORMAT` = `text|json`

## Ingestion Sources and Auto-Discovery

System-1 currently runs one active ingestion source at a time.

Source resolution order:
1. `SYSTEM1_SESSION_LOG_PATH` (explicit override)
2. System-1 default file: `~/.system1/sessions.jsonl`
3. OpenCode JSONL auto-discovery (command probes + common paths)
4. OpenCode SQLite auto-discovery:
   - `~/.local/share/opencode/opencode.db`
   - `~/.opencode/opencode.db`

When OpenCode SQLite is used, System-1 normalizes message parts into a local mirror file:
- `~/.system1/.ingest_opencode_mirror.jsonl`

### Ingestion Tuning Flags

- `SYSTEM1_INGEST_INITIAL_BACKFILL_HOURS`
  - default: `24`
  - used for first OpenCode SQLite backfill window
- `SYSTEM1_INGEST_MAX_EVENTS_PER_CYCLE`
  - default: `200`
  - max normalized events processed per ingestion cycle

### Extraction Logging Flag

- `SYSTEM1_TRACE_EXTRACTION`
  - default: `false`
  - when `true`, enables per-span extraction debug logs
  - when `false`, daemon emits compact cycle summaries

### Hizal Session End Opt-Out

- `SYSTEM1_HIZAL_SKIP_END_SESSION`
  - default: `false`
  - when `true`, `system1.end_session` does not call remote `hizal.end_session`

## Known Follow-Up

- Silent rectification (`update_existing`) is implemented but needs additional live validation for conflicting profile/location memories.
- Current behavior can still create parallel conflicting artifacts in some extraction/policy shapes.
- Keep this in active testing before treating background correction as complete.

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
- optimize for less conscious-agent tool orchestration over time

## MVP explicitly out of scope

The MVP deliberately excludes:

- multi-agent multiplexing (single agent only)
- full Hizal parity, especially surfacing/consolidation semantics and dedicated agent-scoped session ownership
- dynamic focus-shift-driven ambient refresh (startup-only)
- rich introspection depth beyond one configurable extra pass
- setup wizard and runtime reconfiguration
- local model support and host capability detection
- production-grade security hardening for multi-agent isolation

These will be addressed in post-MVP tickets.

See `VISION.md` for the product direction.
