# AGENTS.md - System1: the agent subconsious multiplexer daemon
You are a coding agent working on the System1 codebase (Go, Cobra, SQLite).
This file tells you how to work here. Read it fully before doing anything else.

### Hizal MCP

You have an external context system: Hizal MCP. It is your source of truth for your agent identity, memory, and persisted project/org knowledge. Full usage guide is found [here](./agent-hizal-usage.md)

## Your First Three Steps (always, no exceptions)

1. **Start a Hizal session**
2. **Read the task spec from Forge MCP (System1 project_id: `$FORGE_PROJECT_ID`)**
3. **Query Hizal for existing context on the task -- use `read_context` and `search_context` tools**

After completing these three steps, you can start planning or writing code.

---

## 1. Start a Hizal Session

Every coding session starts and ends with Hizal.

```
start_session(lifecycle_slug="dev_coding")
```

This returns a `session_id`. Keep it visible — you'll need it for `register_focus` and `end_session`.

Then register what you're working on:

```
register_focus(
  session_id="<session-id>",
  task="SYS1-XX: <ticket title>",
  project_id="$FORGE_PROJECT_ID"
)
```

### Session Recovery

If you lose your `session_id` (context reset, compaction):

```
get_active_session()
```

- `status="active"` → use the returned `session_id`, call `resume_session` to extend TTL
- `status="none"` → call `start_session` to begin fresh

---

## 2. Read the Task Spec

Specs come from Forge via the Forge MCP:

```
forge_get_task(taskId="<ticket-id>")
```

System1 Forge tickets live in project `$FORGE_PROJECT_ID` and use the `SYS1-###` prefix.
If direct search by full ticket id fails, search within that project by number or title, or list tasks
for that project and locate the ticket there.

The ticket description is the spec. Read it fully before moving to step 3.

---

## 3. Search Hizal for Existing Context

Now that you know what you're building, search Hizal broadly first, then narrow if needed. The Forge ticket may provide exact chunk query keys. If it does, use `read_context` before trying `search_context`

`search_context` can search across all accessible scopes by default:

- `AGENT` — your personal memory and prior investigations
- `PROJECT` — Back Office knowledge and conventions
- `ORG` — org-wide standards and principles

Start with 2-3 broad searches using different phrasings:

```
search_context(query="<key concept from the spec>")
search_context(query="<ticket id or feature name>")
search_context(query="<related subsystem or endpoint>")
```

Then narrow when you need a specific layer of context:

```
# Project-specific knowledge and conventions
search_context(
  query="<key concept from the spec>",
  project_id="$HIZAL_PROJECT_ID",
  scope="PROJECT"
)

# Prior agent memory / investigation notes
search_context(
  query="<key concept from the spec>",
  scope="AGENT",
  chunk_type="MEMORY"
)

# Org-wide principles and standards
search_context(
  query="<key concept from the spec>",
  scope="ORG"
)
```

If you know the exact saved item you're looking for, search by `query_key`.

Examples:

```
search_context(query="<key concept from the spec>", project_id="$HIZAL_PROJECT_ID")
read_context(query_key="<exact-query-key>", project_id="$HIZAL_PROJECT_ID")
```

Run 2-3 searches with different phrasings. Read the returned chunks — they contain
architecture decisions, conventions, and prior work that must inform your implementation.

If an `AGENT` memory chunk turns out to be broadly useful for the team, promote it later by
writing it back as `write_knowledge` or `write_convention`.

Don't rediscover what the team already decided.

---

## Writing Code

### Branch first, always

Before writing a single line of code:

```bash
git fetch origin main
git checkout -b feat/<ticket-id-lowercase>-<short-description> main
# e.g. feat/hizal-146-password-strength-validation
```

This repo commonly uses **git worktrees**. `main` may already be checked out in another worktree,
so do not assume `git checkout main` will succeed. If your current worktree already points at the
same commit as `main`, branch from the current `HEAD`. Otherwise branch from fetched `main`
without trying to switch the other worktree.

**Never commit directly to main.** If you realize you've committed to main, stop —
create a branch from your current HEAD and reset main before pushing.

### Stack

<!-- TODO: fill in -->

### Conventions

<!-- TODO: fill in -->

### Build check

<!-- TODO: fill in -->

### Running tests/linting

<!-- TODO: fill in -->

---

## Write to Hizal As You Build

This is not optional. Write chunks as you make decisions — not just at the end.

| What you're writing | Tool | Scope |
|---------------------|------|-------|
| Architecture or design decision | `write_knowledge` | PROJECT |
| Convention this codebase follows | `write_convention` | PROJECT (always_inject) |
| Something personal you learned | `write_memory` | AGENT |

**Do not use `write_context`** — it's deprecated. Use the purpose-built tools above.

Write one chunk per meaningful decision. Don't batch everything into one chunk at the end.

---

## Open the PR

**Your session is not complete until a PR exists.** Tests passing and code written is not done.
Done means: branch pushed, PR open, reviewers requested.

```bash
gh pr create \
  --repo XferOps/system1 \
  --title "feat/fix(SYS1-XX): <description>" \
  --body "## Summary\n\n<what you built>\n\n## Testing\n\n<what you ran>\n\n---\n**Forge ticket:** [SYS1-XX](https://forge.xferops.dev/projects/$FORGE_PROJECT_ID) — <ticket title>"
```

---

## End Your Session

After the PR is open and the Forge spec is updated:

```
end_session(session_id="<session-id>")
```

Review the returned MEMORY chunks. For each one, decide:
- **Keep** — useful personal observation, leave as AGENT memory
- **Promote** — valuable for the team, call `write_knowledge` with the content
- **Discard** — noise, ignore it

This is how knowledge compounds across agents and sessions.

