#!/usr/bin/env bash
# check-spec-keys.sh — find stale or non-canonical System-1 spec chunk keys in repo files.
#
# Usage:
#   ./scripts/check-spec-keys.sh              # check .md and .go files
#   ./scripts/check-spec-keys.sh --strict     # also check JSON fixtures
#
# Exit code 0 = clean, 1 = stale keys found.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

CANONICAL=(
  system-1-problems
  system-1-invariants
  system-1-substrate-span-pipeline
  system-1-internal-artifact-model
  system-1-session-lifecycle-waking-mind
  system-1-introspection-interface
  system-1-backend-abstraction-contract
  system-1-extraction-layer-design
  system-1-policy-gate-and-persistence-flow
  system-1-ambient-context-and-waking-mind
  system-1-file-backend-reference-design
  system-1-hizal-backend-design
  system-1-truth-provenance-and-anti-hallucination
  system-1-glossary-and-naming
  system-1-retrieval-and-introspection-internals
  system-1-focus-shift-inference
  system-1-setup-wizard-and-configuration
  system-1-observability-and-debugging
  system-1-security-and-isolation
)

STALE_PREFIXES=(
  "system-1-mvp-definition"
  "system-1-introspection-concept"
  "hizal-subconscious-devspec"
  "hizal_daemon_memory_architecture"
  "winnow-product-vision"
)

find_cmd() {
  if [[ "${1:-}" == "--strict" ]]; then
    find "$REPO_ROOT" -type f \( -name '*.md' -o -name '*.go' -o -name '*.json' \) \
      -not -path '*/.git/*' -not -path '*/node_modules/*' -print0
  else
    find "$REPO_ROOT" -type f \( -name '*.md' -o -name '*.go' \) \
      -not -path '*/.git/*' -not -path '*/node_modules/*' -print0
  fi
}

found_stale=0

for prefix in "${STALE_PREFIXES[@]}"; do
  matches=$(find_cmd "$@" | xargs -0 grep -l "$prefix" 2>/dev/null || true)
  if [[ -n "$matches" ]]; then
    echo "STALE KEY: $prefix"
    echo "$matches" | head -10
    found_stale=1
  fi
done

# Check for any spec-like key that isn't in the canonical set
all_refs=$(find_cmd "$@" | xargs -0 grep -roEh 'system-1-[a-z0-9_-]+|hizal-subconscious[a-z0-9_-]*|winnow-[a-z0-9_-]+' 2>/dev/null | sort -u || true)

while IFS= read -r ref; do
  [[ -z "$ref" ]] && continue
  is_canonical=0
  for c in "${CANONICAL[@]}"; do
    if [[ "$ref" == "$c" ]]; then
      is_canonical=1
      break
    fi
  done
  if [[ $is_canonical -eq 0 ]]; then
    echo "NON-CANONICAL KEY: $ref"
    found_stale=1
  fi
done <<< "$all_refs"

if [[ $found_stale -eq 0 ]]; then
  echo "OK — all spec chunk keys in repo are canonical."
  exit 0
else
  echo "FAIL — stale or non-canonical spec keys found."
  exit 1
fi
