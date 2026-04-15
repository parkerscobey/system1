# System-1 Vision

System-1 exists to take memory and context management out of the main agent's conscious loop.

The conscious agent should not have to manually orchestrate a bag of memory tools every few turns.
It should be able to introspect.

System-1 is the layer beneath that interface:

- observes runtime substrate
- extracts durable signals conservatively
- persists grounded context
- preloads ambient context at session start
- generates Waking Mind
- answers introspection queries with grounded recall

## Product-facing concepts

- **Introspection**: the conscious agent's primary interface to memory and continuity
- **Waking Mind**: startup orientation derived from preloaded ambient context

## MVP

The first real version of System-1 is intentionally narrow:

- single agent
- file backend only
- turn-based extraction
- startup-only ambient context
- bounded introspection

The MVP proves the subconscious loop.

## Longer-term direction

After the MVP, System-1 grows into:

- Hizal-native backend support
- multiplexed multi-agent runtime
- focus-shift inference and refresh
- richer introspection depth
- setup wizard and local model support
- stronger observability and isolation

The architecture is backend-agnostic at the storage boundary, but not falsely neutral. Hizal is the most native backend for the full System-1 model.
