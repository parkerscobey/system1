#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  coderabbit-opencode-cycle.sh <pr-number> [options]

Options:
  --repo OWNER/REPO        GitHub repo (default: inferred from git remote)
  --branch BRANCH          Branch to push at the end (default: current branch)
  --verify                 Ask opencode to run relevant verification before commit
  --include-aggregate      Also run the aggregate "Prompt for all review comments..." block
  --include-resolved       Include prompts from resolved review threads too
  --no-push                Do not push after runs complete
  --dry-run                Extract prompts and print them without running opencode
  -h, --help               Show this help

What it does:
  1. Fetches CodeRabbit comments from the PR via gh api (GraphQL + REST)
  2. Extracts "Prompt for AI Agents" code blocks
  3. Runs opencode once per prompt, sequentially
  4. If opencode makes no changes, resolves the review thread via API
  5. Pushes the branch when done (unless --no-push)

Notes:
  - By default, aggregate "Prompt for all review comments with AI agents" blocks are skipped
    to avoid duplicating the individual prompt runs.
  - By default, prompts from resolved review threads are skipped.
  - By default, opencode is told to patch and commit without extra verification.
  - No-op prompts (already addressed) resolve their review threads via GraphQL.
  - Assumes opencode is already configured for non-interactive use in this repo.
EOF
}

require_bin() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required binary: $1" >&2
    exit 1
  fi
}

infer_repo() {
  local remote url
  remote=$(git remote get-url origin)
  url=${remote%.git}
  url=${url#ssh://git@github.com/}
  url=${url#ssh://github.com/}
  url=${url#git@github.com:}
  url=${url#https://github.com/}
  printf '%s\n' "$url"
}

PR_NUMBER=""
REPO=""
BRANCH=""
INCLUDE_AGGREGATE=0
INCLUDE_RESOLVED=0
PUSH_AT_END=1
DRY_RUN=0
VERIFY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      if [[ $# -lt 2 || -z "$2" || "$2" == -* ]]; then
        echo "Error: --repo requires a non-empty argument that does not start with '-'" >&2
        usage
        exit 1
      fi
      REPO=$2
      shift 2
      ;;
    --branch)
      if [[ $# -lt 2 || -z "$2" || "$2" == -* ]]; then
        echo "Error: --branch requires a non-empty argument that does not start with '-'" >&2
        usage
        exit 1
      fi
      BRANCH=$2
      shift 2
      ;;
    --verify)
      VERIFY=1
      shift
      ;;
    --include-aggregate)
      INCLUDE_AGGREGATE=1
      shift
      ;;
    --include-resolved)
      INCLUDE_RESOLVED=1
      shift
      ;;
    --no-push)
      PUSH_AT_END=0
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -* )
      echo "Unknown option: $1" >&2
      usage
      exit 1
      ;;
    *)
      if [[ -z "$PR_NUMBER" ]]; then
        PR_NUMBER=$1
      else
        echo "Unexpected argument: $1" >&2
        usage
        exit 1
      fi
      shift
      ;;
  esac
done

if [[ -z "$PR_NUMBER" ]]; then
  usage
  exit 1
fi

require_bin gh
require_bin python3
require_bin git
require_bin opencode

for prompt_file in "${PROMPT_FILES[@]}"; do
  title=$(basename "$prompt_file")
  echo
  echo "==> $title"
  echo

  if [[ "$DRY_RUN" == "1" ]]; then
    line_count=$(wc -l < "$prompt_file")
    head -n 220 "$prompt_file"
    if [[ $line_count -gt 220 ]]; then
      echo
      echo "---"
      echo "[Prompt truncated to 220 lines; total lines: $line_count]"
      echo "---"
    fi
    continue
  fi

  PRE_CHANGE_SHA=$(git rev-parse HEAD)

  PROMPT_CONTENT=$(cat "$prompt_file")
  if [[ "$VERIFY" == "1" ]]; then
    FULL_PROMPT="$PROMPT_CONTENT

If changes are needed, implement them, run relevant tests, and commit these changes when you're done. Do not push."
  else
    FULL_PROMPT="$PROMPT_CONTENT

If changes are needed, implement them, do only minimal obvious sanity checks, avoid extra environment probing or exploratory verification, and commit these changes when you're done. Do not push."
  fi

  opencode run "$FULL_PROMPT"

  POST_CHANGE_SHA=$(git rev-parse HEAD)

  if [[ "$PRE_CHANGE_SHA" == "$POST_CHANGE_SHA" ]]; then
    filename=$(basename "$prompt_file")
    THREAD_ID=$(python3 - "$META_JSON" "$filename" <<'PY'
import json, sys
meta = json.load(open(sys.argv[1]))
entry = meta.get(sys.argv[2], {})
print(entry.get("thread_id", ""))
PY
    )

    if [[ -n "$THREAD_ID" ]]; then
      echo "  (no changes — resolving review thread via API)"
      gh api graphql \
        -f query='mutation($threadId:ID!) { resolveReviewThread(input:{threadId:$threadId}) { thread { id } } }' \
        -F threadId="$THREAD_ID" --jq '.data.resolveReviewThread.thread.id' >/dev/null 2>&1 || true
    else
      echo "  (no changes — no thread ID, skipping resolution)"
    fi
  fi
done

if [[ "$DRY_RUN" == "1" ]]; then
  echo "Dry run only. No opencode runs or push performed."
  exit 0
fi

if [[ "$PUSH_AT_END" == "1" ]]; then
  echo
  echo "Pushing branch $BRANCH to origin..."
  git push origin "$BRANCH"
fi

echo "Done."
