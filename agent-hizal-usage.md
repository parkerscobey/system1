# Hizal MCP - agent usage guide

This file is the detailed reference for Hizal usage in the System-1 repo.

Use `AGENTS.md` for the operating contract.
Use this file when you need deeper tool semantics or edge-case guidance.

## Source of truth

- Hizal identity is your primary identity source
- Hizal memory is your primary long-term continuity layer
- Hizal project and org knowledge are the durable source for prior decisions, conventions, and shared context

## Default repo pattern

For normal System-1 coding work:

```txt
start_session(
  lifecycle_slug="dev_coding",
  project_id="$HIZAL_PROJECT_ID"
)
```

Then:

```txt
register_focus(
  session_id="<session-id>",
  task="SYS1-XX: <ticket title>",
  tags=["system1", "ticket:SYS1-XX", "area:<subsystem>"]
)
```

If this repo also uses ADH or another lifecycle later, keep the same search/read contract. Change lifecycle slug and focus tags only when that workflow genuinely differs.

## Session lifecycle

- `start_session` — begins a session and returns automatically injected chunks. Inspect those first before re-searching.
- `get_active_session` — recover the current session id after context reset or reconnect.
- `resume_session` — extend session TTL and re-inject matching chunks.
- `register_focus` — declare current task and tags. This enables focus-tag-based injection when configured.
- `end_session` — close the session and return surfaced chunks for consolidation review.

## Read/search contract

### Core rule

- `read_context` = exact retrieval
- `search_context` = discovery
- `compact_context` = synthesis after discovery, not the first move

### `read_context`

Use `read_context` when you already know the chunk you want.

Common cases:
- the Forge ticket gives an exact `query_key`
- you already know the chunk id
- you need the authoritative contents of one specific chunk

Examples:

```txt
read_context(project_id="$HIZAL_PROJECT_ID", query_key="system-1-problems")
read_context(id="<chunk-id>")
```

Prefer `read_context` over `search_context(query_key=...)` when the exact key is known. It is more direct and less ambiguous.

### `search_context`

Use `search_context` to discover relevant context semantically.

It searches across all accessible scopes by default:
- `AGENT` — your own memory and identity
- `PROJECT` — project-specific context, requires `project_id` when scoped to PROJECT
- `ORG` — org-wide knowledge and principles

Useful filters:
- `scope`
- `project_id`
- `chunk_type`
- `always_inject_only`
- `query_key`
- `limit`

Examples:

```txt
search_context(query="SYS1-8 introspection api")

search_context(
  query="introspection retrieval design",
  project_id="$HIZAL_PROJECT_ID",
  scope="PROJECT"
)

search_context(
  query="introspection retrieval design",
  scope="AGENT",
  chunk_type="MEMORY"
)

search_context(
  query="grounded synthesis principles",
  scope="ORG"
)
```

### Important search caveat

`search_context` results do not return `scope` or `chunk_type` fields.

That means:
- broad search is good for discovery
- broad search is not enough for authority
- if a result matters, narrow and verify before coding from it

Recommended pattern:
1. broad search for recall
2. narrower search by `scope`, `project_id`, or `chunk_type`
3. exact `read_context` for authoritative chunk contents when available

### Authority hierarchy

When synthesizing multiple sources:
1. task spec and referenced spec chunks
2. PROJECT knowledge and conventions
3. ORG principles
4. AGENT memory

Treat AGENT memory as prior investigation or leads, not final project truth.

### `compact_context`

Use `compact_context` when you need a synthesized set of related chunks after you already know the topic.

Good uses:
- preparing an implementation brief
- assembling several known related chunks
- condensing a cluster of relevant context before writing or reviewing

Do not use it as a replacement for exact reads when the needed chunk is already known.

### Other read helpers

- `get_context_versions` — inspect version history for a chunk
- `list_projects` — useful if working across more than one project

## Write tools

Use purpose-built writers.

- `write_identity` — agent-scoped identity
- `write_memory` — agent-scoped episodic memory
- `write_knowledge` — project-scoped knowledge
- `write_convention` — project-scoped conventions and patterns
- `write_org_knowledge` — org-scoped knowledge
- `store_principle` — org-scoped principles requiring human promotion
- `write_chunk` — generic writer for custom chunk types

Do not use deprecated `write_context`.

## Modify/delete tools

- `update_context` — update an existing chunk with versioning
- `review_context` — submit usefulness/correctness feedback
- `delete_context` — delete a chunk and all versions

Prefer updates and reviews over deletions.

## Working rules

- Prefer `search_context` before assuming you remember something
- Use `read_context` when exact chunk id or query key is known
- Inspect injected chunks from `start_session` before re-searching identity or principles
- Use `write_memory` instead of promising to remember later
- Keep writes concise, specific, and reusable
- If search results include `stale_signals`, consider `update_context` or `review_context`

## `inject_audience` basics

`inject_audience` controls deterministic automatic injection during `start_session` and `resume_session`.

- matching chunks are auto-injected
- non-matching chunks stay search-only
- rule format is DNF: rules are OR'd, conditions within a rule are AND'd

Examples:

```json
{ "rules": [{ "all": true }] }
```

```json
{
  "rules": [
    { "agent_types": ["dev_coder"], "project_ids": ["proj-abc"] },
    { "agent_ids": ["agent-xyz"] }
  ]
}
```

Available predicates include:
- `all`
- `agent_ids`
- `agent_types`
- `project_ids`
- `org_ids`
- `agent_tags`
- `focus_tags`
- `lifecycle_types`

Important defaults:
- `write_identity`, `write_convention`, and `store_principle` auto-apply `{ "rules": [{ "all": true }] }`
- `write_memory`, `write_knowledge`, `write_org_knowledge`, and `write_chunk` do not auto-inject unless configured
- after `register_focus`, matching `focus_tags` chunks can be injected immediately without restarting the session
- `agent_tags` match permanent agent profile tags
- `focus_tags` match current session focus tags

## Available chunk types

| Chunk Type | Scope | Custom Fields |
| ---------- | ----- | ------------- |
| IDENTITY | AGENT | None |
| MEMORY | AGENT | None |
| CONSTRAINT | PROJECT | None |
| CONVENTION | PROJECT | None |
| DECISION | PROJECT | None |
| KNOWLEDGE | PROJECT | None |
| LESSON | PROJECT | None |
| PRINCIPLE | ORG | None |
| RESEARCH | PROJECT | None |
| PLAN | PROJECT | None |
| IMPLEMENTATION | PROJECT | Pull Request link: url |
| SPEC | PROJECT | status: text (required) |
