# Contributing

Thanks for contributing to System-1.

This project is still in early scaffold/MVP phase.
It is intentionally vibe-coded: favor fast, behavior-driven iteration that improves subconscious reliability for the conscious agent.

## For now

- prefer small, spec-aligned changes
- keep the MVP thin
- do not collapse the generic artifact model into hardcoded long-term limitations
- preserve MCP-first Introspection as the primary interface
- prefer changes that reduce conscious-agent tool-calling and context-management responsibilities
- favor explicitness over framework magic
- respect the 8 canonical invariants in `SPEC_TRACEABILITY.md` — use them by number in PR checklists

## Before opening a PR

- make sure the change maps clearly to an active Forge ticket
- reference the relevant System-1 spec query keys in your PR description
- use only canonical keys from the 19-chunk System-1 spec set
- fill out the PR template completely, especially spec chunks, invariants touched, drift check, and testing
- keep the implementation narrow if the ticket is MVP-scoped
- run `./scripts/check-spec-keys.sh` and make sure it passes

More detailed contribution guidance will be added as the repository matures.
