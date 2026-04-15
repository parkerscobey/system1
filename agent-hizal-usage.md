# Hizal MCP - AGENT usage guide

### Source of Truth

- Hizal identity is your primary identity source.
- Hizal memory is your primary long-term memory/search source

### Session Lifecycle

- `start_session` — begins a session and returns automatically-injected chunks like IDENTITY and PRINCIPLES. Don't re-search for them.
- `get_active_session` — recover the current session id after context reset or reconnect
- `resume_session` — extend session TTL and re-inject matching chunks
- `register_focus` — declare current task and tags; enables focus-tag-based chunk injection when configured
- `end_session` — close the session and return chunks for consolidation review

### Read/Search

- `search_context` — semantic search across all accessible scopes by default. Narrow with `scope` when you need it:
  - `scope: "ORG"` — only org-wide chunks (PRINCIPLE, KNOWLEDGE)
  - `scope: "AGENT"` — only your own memory and identity
  - `scope: "PROJECT"` — only project-specific context (requires `project_id`)
  - Other filters: `chunk_type`, `always_inject_only`, `query_key`, `limit` (default 10)
  - ⚠️ **Note:** search results do not return `scope` or `chunk_type` fields — you can't distinguish chunk origins from re sult data alone
- `read_context` — fetch a specific chunk by `id` or `query_key` - use this tool if you know a chunk's `query_key`
- `compact_context` — retrieve related chunks for agent-side synthesis (read-only)
- `get_context_versions` — inspect version history for a chunk
- `list_projects` — may be useful if working on multiple projects

### Write (Purpose-Built)

- `write_identity` — agent-scoped identity
- `write_memory` — agent-scoped episodic memory
- `write_knowledge` — project-scoped knowledge
- `write_convention` — project-scoped conventions and patterns
- `write_org_knowledge` — org-scoped knowledge
- `store_principle` — org-scoped principles; requires human promotion
- `write_chunk` — generic writer for custom chunk types

### Modify/Delete

- `update_context` — update an existing chunk with versioning
- `review_context` — submit usefulness and correctness feedback
- `delete_context` — delete a chunk and all versions

### Hizal Working Rules

- Use `read_context` when you already know the exact chunk/query key
- Use session lifecycle tools intentionally; do not leave long-running work floating without a session plan
- Prefer `search_context` before assuming you remember something
- Use `write_memory` instead of saying you will remember something later
- Keep writes concise, specific, and reusable
- Do not delete or overwrite context casually; prefer versioned updates and reviews
- After `start_session`, inspect `stale_signals` on search results — if present, the chunk may need `update_context` or `review_context`

### How `inject_audience` Works

`inject_audience` controls deterministic automatic injection at session start and resume.

- Chunks with matching `inject_audience` rules are injected automatically during `start_session` and `resume_session`
- Chunks without `inject_audience` are search-only
- The rule format is disjunctive normal form (DNF): rules are OR'd together, conditions within one rule are AND'd together

Examples:

```json
{ "rules": [{ "all": true }] }
```

```json
{
  "rules": [{ "agent_types": ["dev"], "project_ids": ["proj-abc"] }, { "agent_ids": ["agent-xyz"] }]
}
```

This means:

- inject when the agent is type `dev` and working on project `proj-abc`
- or inject when the agent is specifically `agent-xyz`

Available predicates include:

- `all: true` <- boolean flag; matches unconditionally
- `agent_ids`
- `agent_types`
- `project_ids`
- `org_ids`
- `agent_tags`
- `focus_tags`
- `lifecycle_types`

Important defaults to remember:

- `write_identity`, `write_convention`, and `store_principle` auto-apply {"rules":[{"all":true}]} as the inject_audience default — you don't need to specify it. `write_memory`, `write_knowledge`, `write_org_knowledge`, and `write_chunk` do NOT auto-inject — inject_audience is null unless you pass it explicitly.
- When `register_focus` is called, chunks with matching `focus_tags` rules are added to the session's `inject_set` immediately — they don't require a session restart.
- `agent_tags` — matches against the agent's permanent tag profile; no `register_focus` required
- `focus_tags` — matches against the current session focus tags; only populated after `register_focus`

### Available Chunk Types

|   Chunk Type   |  Scope  |       Custom Fields     |
| -------------- | ------- | ----------------------- |
| IDENTITY       | AGENT   |  None                   |
| MEMORY         | AGENT   |  None                   |
| CONSTRAINT     | PROJECT |  None                   |
| CONVENTION     | PROJECT |  None                   |
| DECISION       | PROJECT |  None                   |
| KNOWLEDGE      | PROJECT |  None                   |
| LESSON         | PROJECT |  None                   |
| PRINCIPLE      | ORG     |  None                   |
| RESEARCH       | PROJECT |  None                   |
| PLAN           | PROJECT |  None                   |
| IMPLEMENTATION | PROJECT | Pull Request link: url  |
| SPEC           | PROJECT | status: text (required) |

