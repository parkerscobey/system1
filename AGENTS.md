# AGENTS.md - System-1: the agent subconscious multiplexer daemon

You are a coding agent working on the System-1 codebase.

Primary stack here:
- Go
- Cobra
- SQLite
- MCP-first interfaces

Read this file before doing anything else.

For deeper Hizal reference, see [./agent-hizal-usage.md](./agent-hizal-usage.md).

## Your first three steps

Do these in order, every session:

1. Start a Hizal session
2. Read the Forge task spec
3. Retrieve relevant Hizal context

Do not start planning or coding before doing all three.

Resolve `HIZAL_PROJECT_ID` to `d5bca61a-3f27-4256-bb6b-14654b0fcd3f` and `FORGE_PROJECT_ID` to `cmnz3po9s000hjx01phjymcpv`. Use literal resolved values in tool calls. Never pass `HIZAL_PROJECT_ID` or `FORGE_PROJECT_ID` verbatim.

If Hizal fails at any point, report it to the user immediately. Do not let Hizal session failures, read/search failures, write failures, or suspicious missing context pass silently. Say what failed, where it failed, and whether work is blocked or can continue safely with reduced continuity.

---

## 1. Start a Hizal session

Every coding session starts with Hizal.

Default for this repo:

```txt
start_session(
  lifecycle_slug="dev_coding",
  project_id="HIZAL_PROJECT_ID"
)
```

This returns a `session_id` and injected chunks. Inspect injected chunks first. Do not immediately re-search IDENTITY, PRINCIPLE, or other already-injected context unless you need more detail.

Then register focus:

```txt
register_focus(
  session_id="<session-id>",
  task="SYS1-XX: <ticket title>",
  tags=["system1", "ticket:SYS1-XX", "area:<subsystem>"]
)
```

Use `dev_coding` as the normal lifecycle for repo work. If this project later uses ADH or another lifecycle, keep the same retrieval contract and only change the lifecycle slug or focus tags as needed.

### Session recovery

If you lose your `session_id`:

```txt
get_active_session()
```

- `status="active"` → use returned `session_id`, then call `resume_session`
- `status="none"` → call `start_session` again

---

## 2. Read the task spec

Specs come from Forge MCP.

```txt
forge_get_task(taskId="<ticket-id>")
```

System-1 Forge tickets live in project `FORGE_PROJECT_ID` and use the `SYS1-###` prefix.

If direct lookup by ticket id fails, search within that project by number or title, or list project tasks and locate it there.

The ticket description is the working spec. Read it fully before retrieving Hizal context.

---

## 3. Retrieve Hizal context

Use Hizal as the continuity layer for this repo.

### Retrieval order

1. If the Forge ticket includes exact Hizal query keys, use `read_context` first
2. Then do broad discovery with `search_context`
3. Then rerun narrower searches or read exact chunks before coding from a result

### Exact retrieval

If you know the exact chunk you want, use `read_context`.

```txt
read_context(
  project_id="HIZAL_PROJECT_ID",
  query_key="<exact-query-key>"
)
```

Use `read_context` when the ticket already tells you the exact spec chunk, design chunk, or convention chunk to load.

### Discovery search

Start with 2-3 broad searches using different phrasings:

```txt
search_context(query="<ticket id or feature name>")
search_context(query="<key concept from the spec>")
search_context(query="<related subsystem or endpoint>")
```

Then narrow by scope when needed:

```txt
search_context(
  query="<key concept from the spec>",
  project_id="HIZAL_PROJECT_ID",
  scope="PROJECT"
)

search_context(
  query="<key concept from the spec>",
  scope="AGENT",
  chunk_type="MEMORY"
)

search_context(
  query="<key concept from the spec>",
  scope="ORG"
)
```

### Important search rule

Broad search hits are leads, not authority.

`search_context` results do not include `scope` or `chunk_type`, so do not blindly trust a broad hit as the final answer. If a hit matters:

- rerun the search with narrower filters
- or load the exact chunk with `read_context`
- then code from the verified result

### Authority order

When sources disagree, trust them in this order:

1. Forge ticket spec and referenced spec chunks
2. PROJECT knowledge and conventions
3. ORG principles
4. AGENT memory

Treat AGENT memory as prior investigation and useful leads, not final project truth.

If an AGENT memory chunk is broadly useful, promote it later with `write_knowledge` or `write_convention`.

Do not rediscover decisions the team already made.

---

## Writing code

### Branch first, always

Before writing code:

```bash
git fetch origin main
git checkout -b feat/<ticket-id-lowercase>-<short-description> main
```

Example:

```bash
git checkout -b feat/sys1-8-introspection-api main
```

This repo commonly uses git worktrees. `main` may already be checked out elsewhere, so do not assume `git checkout main` will succeed. If your current worktree already points at the same commit as `main`, branch from current `HEAD`. Otherwise branch from fetched `main` without trying to switch another worktree.

Never commit directly to `main`.

### Local conventions

- Keep the MVP thin
- Preserve the generic artifact model even when MVP enables only a narrow subset
- Prefer explicit config over framework magic
- Introspection is MCP-first. CLI mirrors exist for dev and debug, not as the primary interface
- Match existing repo structure and naming unless the ticket explicitly changes it

### Build check

Before opening a PR, run:

```bash
go fmt ./...
go test ./...
go build ./cmd/system1
```

---

## Write to Hizal as you build

This is required.

Write durable context when you make a meaningful decision, learn a reusable convention, or uncover something worth preserving.

| What you're writing | Tool | Scope |
|---------------------|------|-------|
| Architecture or design decision | `write_knowledge` | PROJECT |
| Convention this codebase follows | `write_convention` | PROJECT |
| Something personal to your investigation or workflow | `write_memory` | AGENT |

Do not use `write_context`. It is deprecated.

Do not wait until the end of the task to write everything.

Every session must produce at least one durable AGENT memory. Before ending the session, write at least one `write_memory` chunk that captures what you did, what changed, what you learned, or what should carry forward into the next run.

---

## Open the PR

The task is not complete until a PR exists.

Done means:
- branch pushed
- PR open
- reviewers requested

```bash
gh pr create \
  --repo XferOps/system1 \
  --title "feat/fix(SYS1-XX): <description>" \
  --body "## Summary\n\n<what you built>\n\n## Testing\n\n<what you ran>\n\n---\n**Forge ticket:** [SYS1-XX](https://forge.xferops.dev/projects/FORGE_PROJECT_ID) - <ticket title>"
```

---

## End your session

After the PR is open and the Forge task is updated:

```txt
end_session(session_id="<session-id>")
```

Before ending the session, make sure you have written at least one `write_memory` chunk for that session.

Review the returned surfaced chunks.

For each one:
- **Keep** → useful as-is
- **Promote** → rewrite as `write_knowledge` or `write_convention` if it should become shared project context
- **Discard** → ignore noise

This is how knowledge compounds across sessions and agents.
