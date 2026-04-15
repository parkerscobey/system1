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
  1. Fetches CodeRabbit comments from the PR via gh api
  2. Extracts "Prompt for AI Agents" code blocks
  3. Runs opencode once per prompt, sequentially
  4. Appends a default patch-and-commit instruction to each prompt
  5. Pushes the branch when done (unless --no-push)

Notes:
  - By default, aggregate "Prompt for all review comments with AI agents" blocks are skipped
    to avoid duplicating the individual prompt runs.
  - By default, prompts from resolved review threads are skipped.
  - By default, opencode is told to patch and commit without extra verification.
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

if [[ -z "$REPO" ]]; then
  REPO=$(infer_repo)
fi

if [[ -z "$BRANCH" ]]; then
  BRANCH=$(git branch --show-current)
fi

if [[ -z "$BRANCH" ]]; then
  echo "Could not determine current git branch" >&2
  exit 1
fi

if ! git diff --quiet || ! git diff --cached --quiet || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  echo "Working tree is not clean. Commit or stash changes first." >&2
  exit 1
fi

TMP_DIR=$(mktemp -d)
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

ISSUE_JSON="$TMP_DIR/issues.json"
REVIEW_JSON="$TMP_DIR/reviews.json"
INLINE_JSON="$TMP_DIR/inline.json"
THREADS_JSON="$TMP_DIR/threads.json"
PROMPTS_JSON="$TMP_DIR/prompts.json"

OWNER=${REPO%%/*}
REPO_NAME=${REPO#*/}

gh api "repos/$REPO/issues/$PR_NUMBER/comments" --paginate > "$ISSUE_JSON"
gh api "repos/$REPO/pulls/$PR_NUMBER/reviews" --paginate > "$REVIEW_JSON"
gh api "repos/$REPO/pulls/$PR_NUMBER/comments" --paginate > "$INLINE_JSON"
gh api graphql \
  -f query='query($owner:String!, $repo:String!, $number:Int!) { repository(owner:$owner, name:$repo) { pullRequest(number:$number) { reviewThreads(first:100) { nodes { isResolved comments(first:100) { nodes { id } } } } } } }' \
  -f owner="$OWNER" \
  -f repo="$REPO_NAME" \
  -F number="$PR_NUMBER" > "$THREADS_JSON"

python3 - "$ISSUE_JSON" "$REVIEW_JSON" "$INLINE_JSON" "$THREADS_JSON" "$PROMPTS_JSON" "$INCLUDE_AGGREGATE" "$INCLUDE_RESOLVED" <<'PY'
import json, pathlib, re, sys
issue_path, review_path, inline_path, threads_path, out_path, include_aggregate, include_resolved = sys.argv[1:8]
include_aggregate = include_aggregate == "1"
include_resolved = include_resolved == "1"

resolved_comment_ids = set()
threads_data = json.loads(pathlib.Path(threads_path).read_text())
thread_nodes = (((threads_data.get("data") or {}).get("repository") or {}).get("pullRequest") or {}).get("reviewThreads", {}).get("nodes", [])
for thread in thread_nodes:
    if not thread.get("isResolved"):
        continue
    for comment in ((thread.get("comments") or {}).get("nodes") or []):
        comment_id = comment.get("id")
        if comment_id:
            resolved_comment_ids.add(comment_id)

sources = []
for path in [issue_path, review_path, inline_path]:
    p = pathlib.Path(path)
    if not p.exists():
        continue
    try:
        data = json.loads(p.read_text())
    except Exception:
        continue
    if isinstance(data, dict):
        data = [data]
    for item in data:
        user = ((item.get("user") or {}).get("login") or "")
        body = item.get("body") or ""
        node_id = item.get("node_id") or item.get("id") or ""
        if user not in {"coderabbitai", "coderabbitai[bot]"} or not body:
            continue
        if not include_resolved and node_id in resolved_comment_ids:
            continue
        sources.append(body)

pattern = re.compile(
    r"<summary>🤖\s*(?P<title>Prompt[^<]*)</summary>.*?```(?:\w+)?\n(?P<prompt>.*?)```",
    re.S,
)

seen = set()
prompts = []
for body in sources:
    for m in pattern.finditer(body):
        title = re.sub(r"\s+", " ", m.group("title").strip())
        prompt = m.group("prompt").strip()
        if not prompt:
            continue
        aggregate = "all review comments" in title.lower()
        if aggregate and not include_aggregate:
            continue
        key = (title, prompt)
        if key in seen:
            continue
        seen.add(key)
        prompts.append({"title": title, "prompt": prompt, "aggregate": aggregate})

pathlib.Path(out_path).write_text(json.dumps(prompts, indent=2))
PY

PROMPT_COUNT=$(python3 - "$PROMPTS_JSON" <<'PY'
import json, sys
items = json.load(open(sys.argv[1]))
print(len(items))
PY
)

if [[ "$PROMPT_COUNT" == "0" ]]; then
  echo "No matching CodeRabbit prompts found for PR #$PR_NUMBER in $REPO"
  exit 0
fi

echo "Found $PROMPT_COUNT CodeRabbit prompt(s) for PR #$PR_NUMBER in $REPO"

python3 - "$PROMPTS_JSON" "$TMP_DIR" <<'PY'
import json, pathlib, re, sys
prompts_path, out_dir = sys.argv[1:3]
items = json.load(open(prompts_path))
out = pathlib.Path(out_dir)
for i, item in enumerate(items, start=1):
    slug = re.sub(r'[^a-z0-9]+', '-', item['title'].lower()).strip('-') or f'prompt-{i}'
    path = out / f'{i:03d}-{slug}.txt'
    path.write_text(item['prompt'])
PY

PROMPT_FILES=()
while IFS= read -r prompt_file; do
  PROMPT_FILES+=("$prompt_file")
done < <(find "$TMP_DIR" -maxdepth 1 -name '*.txt' | sort)

require_bin opencode

for prompt_file in "${PROMPT_FILES[@]}"; do
  title=$(basename "$prompt_file")
  echo
  echo "==> $title"
  echo

  if [[ "$DRY_RUN" == "1" ]]; then
    sed -n '1,220p' "$prompt_file"
    echo
    echo "---"
    continue
  fi

  PROMPT_CONTENT=$(cat "$prompt_file")
  if [[ "$VERIFY" == "1" ]]; then
    FULL_PROMPT="$PROMPT_CONTENT

If changes are needed, implement them, run relevant tests, and commit these changes when you're done. Do not push."
  else
    FULL_PROMPT="$PROMPT_CONTENT

If changes are needed, implement them, do only minimal obvious sanity checks, avoid extra environment probing or exploratory verification, and commit these changes when you're done. Do not push."
  fi

  opencode run "$FULL_PROMPT"
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
