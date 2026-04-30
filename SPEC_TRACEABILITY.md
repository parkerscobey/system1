# System-1 Spec Traceability and Drift Audit

## Purpose

This file is the canonical bridge between:

1. the 19 Hizal spec chunks
2. the current codebase
3. the Forge ticket plan

Use this instead of re-deriving intent from memory, old chat context, or scattered ticket descriptions.

## Source of truth

Authority order:

1. the 19 canonical Hizal spec chunks
2. this traceability file
3. Forge tickets
4. current implementation

If Forge or code disagree with the 19 chunks, the chunks win unless Parker explicitly changes the spec.

## Canonical spec chunk keys

- `system-1-problems`
- `system-1-invariants`
- `system-1-substrate-span-pipeline`
- `system-1-internal-artifact-model`
- `system-1-session-lifecycle-waking-mind`
- `system-1-introspection-interface`
- `system-1-backend-abstraction-contract`
- `system-1-extraction-layer-design`
- `system-1-policy-gate-and-persistence-flow`
- `system-1-ambient-context-and-waking-mind`
- `system-1-file-backend-reference-design`
- `system-1-hizal-backend-design`
- `system-1-truth-provenance-and-anti-hallucination`
- `system-1-glossary-and-naming`
- `system-1-retrieval-and-introspection-internals`
- `system-1-focus-shift-inference`
- `system-1-setup-wizard-and-configuration`
- `system-1-observability-and-debugging`
- `system-1-security-and-isolation`

## Canonical invariants

These 8 non-negotiable rules come from the `system-1-invariants` chunk. They are architectural laws, not preferences.

1. **Supportive, not sovereign** — System-1 supports the conscious agent; it never replaces it or becomes a hidden autonomous planner.
2. **Attributable, not mysterious** — every meaningful output is traceable to source agent, session, and underlying evidence; behavior is inspectable.
3. **Isolated, not leaky** — one agent's private data must never mix into another agent's pipeline; per-agent isolation is hard requirement.
4. **Grounded in source evidence** — raw experience precedes synthesis; derived artifacts must not obscure or falsely become the original source of truth.
5. **Resilient under failure** — failures degrade rather than destroy; raw logs and durable state allow recovery, retry, or reprocessing.
6. **Simpler for the conscious agent** — System-1 succeeds only if it removes memory burden from the conscious loop; trend toward introspection, not tool choreography.
7. **Multi-agent compatible** — architecture preserves a clean path to multi-agent operation even if single-agent is primary target.
8. **Bounded intelligence, not open-ended agency** — System-1 uses constrained intelligence for classify/extract/filter; it has no agenda or independent tool-use.

When checking PRs or tickets against invariants, use this numbered list. PR template references these by number.

### Invariant test coverage

| Invariant | Tests | Package |
|---|---|---|
| 1 — supportive, not sovereign | `TestExtractionOnlyProposesNeverApproves` | `extract` |
| 2 — attributable, not mysterious | `TestApprovedCandidateRetainsProvenance` | `policy` |
| 3 — isolated, not leaky | no automated test yet (single-agent MVP) | — |
| 4 — grounded in source evidence | `TestCandidateWithoutEvidenceIsRejected`, `TestCandidateWithEmptyEvidenceStringIsRejected`, `TestCandidateWithWhitespaceEvidenceStringIsRejected`, `TestCandidateWithMixedGoodAndEmptyEvidenceIsRejected`, `TestCandidateWithEvidenceIsApproved`, `TestCandidateProvenanceCarriesEvidenceSnippets`, `TestCandidateProvenanceReferencesOrigin`, `TestCandidateTitleAndBodyAreResolvedContent`, `TestQueryDebugModeIncludesProvenance`, `TestSessionStartPersistsAmbientSnapshot` | `policy`, `extract`, `introspect`, `session` |
| 5 — resilient under failure | `TestSessionStartDegradesGracefullyOnPartialBackendFailure`, `TestSessionStartHandlesEmptyBackend`, `TestLoadAmbientSnapshotMissingDir`, `TestLoadAmbientSnapshotCorruptFile`, `TestSessionEndWithoutStartIsNoOp`, `TestStore_Search_CorruptChunkReturnsError`, `TestStore_Get_CorruptChunkReturnsError` | `session`, `backend/hizal` |
| 6 — simpler for the conscious agent | no automated test yet | — |
| 7 — multi-agent compatible | no automated test yet (single-agent MVP) | — |
| 8 — bounded intelligence | `TestExtractionAbstainsOnLowSignalContent`, `TestExtractionAbstainsOnNoRefs`, `TestExtractionAbstainsOnUnreadableRef`, `TestExtractionRejectsUnregisteredTypes` | `extract` |

## Current audit summary

### Status refresh (Apr 2026)

This file contains older drift notes from early scaffold phases. Current runtime has advanced beyond several of those statements.

Key updates to keep in mind while reading the matrix below:

- daemon runtime now executes a real loop: ingest -> extract -> policy -> persist
- OpenCode source discovery now supports JSONL and SQLite (`opencode.db`) ingestion paths
- introspection now includes model-assisted multi-pass retrieval behavior for Hizal-backed flows
- policy includes early silent-rectification routing (`update_existing`) but requires more live validation
- Hizal backend save/read/session and end-session behaviors are active; not scaffold-only

Use package-level code and latest tickets/chunks for final authority when this file's older paragraphs conflict with current implementation details.

### Healthy enough

- substrate to span pipeline exists
- core artifact model exists and is generic enough for MVP
- file backend exists with JSON plus SQLite sidecar and FTS
- policy layer exists with approve/reject/defer and dedup
- session start, ambient snapshot, MCP surface, and basic introspection all exist

### Main drift

1. **Forge query-key drift**
   - tickets cited non-canonical keys from the old pre-canonical set
   - normalized during first audit pass, script check-spec-keys.sh now catches regressions

2. **Some behavior remains partially scaffold-like**
   - session lifecycle still lacks full surfacing/consolidation semantics
   - Waking Mind quality is improved but still prompt/heuristic constrained
   - extraction remains conservative but still not fully spec-grade
   - maintenance/rectification requires more live validation

3. **Critical invariants are represented in structs more than enforced in behavior**
   - provenance exists
   - uncertainty is partially surfaced
   - isolation and anti-hallucination guarantees are not yet hardened

## Spec -> code -> Forge matrix

| Spec chunk | Code status | Forge coverage | Drift note |
|---|---|---|---|
| 1. Problems | Partial | SYS1-12, SYS1-18 | Big product intent present in docs, not encoded as design guardrails in code |
| 2. Invariants | Partial | SYS1-12, SYS1-17, SYS1-18 | Provenance and single-agent assumptions exist, but hard invariants not systematically tested/enforced |
| 3. Substrate, Span Model, and Processing Pipeline | Mostly MVP | SYS1-2 | Turn/segment span building exists in `internal/ingest/service.go`, but still narrow and single-source |
| 4. Internal Artifact Model | Mostly MVP | SYS1-3, SYS1-21 | `internal/artifacts/types.go` is solid MVP substrate |
| 5. Session Lifecycle, Waking Mind, and Ambient Context Surfacing | Partial | SYS1-7, SYS1-19, SYS1-24 | `start_session` works, `end_session` is mostly no-op, surfacing/consolidation behavior not real yet |
| 6. Introspection Interface and Behavior | Partial+ | SYS1-8, SYS1-19, SYS1-20, SYS1-24, SYS1-16 | Interface is active with model-assisted retrieval/synthesis, but still not full spec-depth across all modes |
| 7. Backend Abstraction Contract | Partial | SYS1-3, SYS1-6, SYS1-11, SYS1-22 | Backend interface exists, but richer contract boundaries still fuzzy around session/native behaviors |
| 8. Extraction Layer Design | Partial | SYS1-4, SYS1-21 | Conservative shape exists, but extraction is heuristics over text, not robust spec-grade extraction |
| 9. Policy Gate, Deferred Decisions, Dedup, and Persistence Flow | Partial+ | SYS1-5, SYS1-25, SYS1-26 | Core loop exists in daemon runtime with dedup and early update-existing routing; rectification tuning still in progress |
| 10. Ambient Context Selection and Waking Mind Generation | Partial | SYS1-7, SYS1-20, SYS1-24, SYS1-13 | Ambient selection is simple recency sort, not spec-level continuity ranking |
| 11. File Backend Reference Design | Mostly MVP | SYS1-6 | Strongest aligned area today |
| 12. Hizal Backend Design | Partial+ | SYS1-11, SYS1-22, SYS1-25, SYS1-26 | Save/read/session integration is active; remaining drift is deeper native lifecycle and consolidation semantics |
| 13. Truth, Provenance, and Anti-Hallucination Guarantees | Partial | SYS1-4, SYS1-5, SYS1-8, SYS1-9, SYS1-17 | Evidence exists in structs and debug output, but guarantees are not yet systemically enforced |
| 14. Glossary and Naming Conventions | Partial | SYS1-1 | Naming mostly good, but no canonical in-repo glossary/reference doc until this file |
| 15. Retrieval and Introspection Internals | Partial+ | SYS1-8, SYS1-20, SYS1-24, SYS1-16 | Retrieval now supports multi-step Hizal search/read planning with intent reconstruction, still short of full spec endpoint |
| 16. Focus-Shift Inference | Not started | SYS1-13 | Ticket exists, code does not |
| 17. Setup Wizard and Configuration Model | Not started / minimal | SYS1-14, SYS1-15 | Env config and `doctor` exist, not a real setup model |
| 18. Observability and Debugging | Partial | SYS1-9, SYS1-17 | Basic health and introspection traces exist, but not full cognitive traceability |
| 19. Security and Isolation | Bare scaffold only | SYS1-12, SYS1-18 | Single-agent scaffold respects future boundary, but hard isolation not implemented |

## Concrete implementation notes

### Areas that are pretty aligned

- `internal/artifacts/types.go`
  - good generic artifact model
  - provenance preserved structurally
- `internal/ingest/service.go`
  - deterministic ingestion and turn boundary logic
- `internal/backend/file/store.go`
  - good MVP reference backend shape
- `internal/policy/service.go`
  - explicit approve/reject/defer states and dedup path

### Areas showing meaningful drift

- `internal/session/service.go`
  - ambient context selection is just latest artifacts by type
  - Waking Mind is simple string formatting over artifact snippets
  - session end does not yet perform meaningful surfacing/consolidation

- `internal/introspect/service.go`
  - major improvements landed (intent reconstruction, mode-conditioned behavior, multi-step Hizal retrieval)
  - still needs deeper confidence/uncertainty surfacing and broader live hardening

- `internal/extract/service.go`
  - extraction is keyword heuristic based
  - candidate generation is thin and likely too brittle for spec intent

- `internal/backend/hizal/*`
  - session start/end and read/write flows are active
  - still needs deeper native surfacing/consolidation semantics for full parity

- `internal/obs/*.go`
  - enough for MVP smoke checks
  - not enough for spec-level inspectability and trust surfaces

## Forge drift found already

During the first audit pass, Forge tickets contained stale and non-canonical spec key references. These have been normalized to the canonical 19 chunk keys, and SYS1-27 was added to carry ongoing drift control. The check-spec-keys.sh script now catches any regression.

## Recommended process change

Do **not** review chunk by chunk in isolation.

Use this loop instead:

1. **Canonicalize**
   - maintain this file as the single traceability map
   - every ticket must cite only canonical spec keys from the 19-chunk list

2. **Map**
   - every ticket gets:
     - primary spec chunks
     - touched packages
     - invariant checklist
     - explicit out-of-scope note

3. **Audit**
   - for each package, mark status as:
     - aligned
     - partial
     - drifted
     - not started

4. **Gate**
   - PR template must include:
     - spec chunks referenced
     - invariants affected
     - drift introduced or retired
     - tests proving the invariant still holds

5. **Review on two axes**
   - code correctness
   - spec alignment

## Suggested near-term next actions

### First

Complete SYS1-27 so spec drift checks become part of the normal delivery loop, not a one-time cleanup.

### Second

Create an explicit package ownership map in this file:

- ingest -> chunks 3, 13
- artifacts -> chunks 4, 7, 13, 14
- extract -> chunks 8, 13
- policy -> chunks 9, 13
- backend/file -> chunks 7, 11
- backend/hizal -> chunks 7, 12
- session -> chunks 5, 10
- introspect -> chunks 6, 15
- obs -> chunks 18, 13
- config/setup -> chunks 17, 19

### Third

Add a CI-level drift check later:

- parse ticket or PR body for canonical chunk keys
- fail if non-canonical keys are used
- fail if changed packages have no mapped spec references

## Recommendation on the “one huge design doc” idea

Yes, but **internal canonical doc first, public article second**.

Use this file as the canonical engineering reference.

Then later derive a cleaned-up public article from it. Do not make the dev.to article the source of truth. Public writing should trail the internal canonical spec, not replace it.
