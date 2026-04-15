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
THREADS_NODES="[]"
THREADS_CURSOR="null"
HAS_MORE_THREADS=true

fetch_all_thread_comments() {
  local thread_id="$1"
  local comment_ids="[]"
  local cursor="null"
  
  while true; do
    local query="query(\$threadId:ID!, \$cursor:String) { node(id:\$threadId) { ... on ReviewThread { comments(first:100, after:\$cursor) { pageInfo { hasNextPage endCursor } nodes { id } } } } }"
    
    local response
    response=$(gh api graphql -f query="$query" -F threadId="$thread_id" -F cursor="$cursor" --jq '.data' 2>/dev/null)
    
    local page_info nodes
    page_info=$(echo "$response" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('node',{}).get('comments',{}).get('pageInfo',{})))")
    nodes=$(echo "$response" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('node',{}).get('comments',{}).get('nodes',[])))")
    
    comment_ids=$(echo "$comment_ids" "$nodes" | python3 -c "import json,sys; a=json.loads(sys.stdin.readline() or '[]'); b=json.loads(sys.stdin.readline() or '[]'); a.extend(b); print(json.dumps(a))")
    
    local has_more
    has_more=$(echo "$page_info" | python3 -c "import json,sys; d=json.load(sys.stdin); print('true' if d.get('hasNextPage') else 'false')")
    
    [ "$has_more" != "true" ] && break
    cursor=$(echo "$page_info" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('endCursor') or 'null')")
  done
  
  echo "$comment_ids"
}

paged_threads='[]'

while [ "$HAS_MORE_THREADS" = "true" ]; do
  THREADS_QUERY='query($owner:String!, $repo:String!, $number:Int!, $cursor:String) { repository(owner:$owner, name:$repo) { pullRequest(number:$number) { reviewThreads(first:100, after:$cursor) { pageInfo { hasNextPage endCursor } nodes { id isResolved comments(first:100) { pageInfo { hasNextPage endCursor } nodes { id } } } } } } }'

  RESPONSE=$(gh api graphql \
    -f query="$THREADS_QUERY" \
    -f owner="$OWNER" \
    -f repo="$REPO_NAME" \
    -F number="$PR_NUMBER" \
    -F cursor="$THREADS_CURSOR" \
    --jq '.data')

  THREADS_PAGE=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('repository',{}).get('pullRequest',{}).get('reviewThreads',{})))")
  PAGE_NODES=$(echo "$THREADS_PAGE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('nodes',[])))")
  PAGE_INFO=$(echo "$THREADS_PAGE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('pageInfo',{})))")

  HAS_MORE_THREADS=$(echo "$PAGE_INFO" | python3 -c "import json,sys; d=json.load(sys.stdin); print('true' if d.get('hasNextPage') else 'false')")
  THREADS_CURSOR=$(echo "$PAGE_INFO" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('endCursor') or 'null')")

  for thread in $(echo "$PAGE_NODES" | python3 -c "import json,sys; print(' '.join([t.get('id','') for t in json.load(sys.stdin)]))" 2>/dev/null); do
    thread_json=$(echo "$PAGE_NODES" | python3 -c "import json,sys; d=json.load(sys.stdin); print(next((json.dumps(t) for t in d if t.get('id')=='$thread'), '{}'))")
    is_resolved=$(echo "$thread_json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('isResolved', False))")
    first_comment_ids=$(echo "$thread_json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps([c.get('id') for c in d.get('comments',{}).get('nodes',[]) if c.get('id')]))")
    comments_page_info=$(echo "$thread_json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('comments',{}).get('pageInfo',{})))")
    has_more_comments=$(echo "$comments_page_info" | python3 -c "import json,sys; d=json.load(sys.stdin); print('true' if d.get('hasNextPage') else 'false')")
    
    if [ "$has_more_comments" = "true" ]; then
      comment_ids=$(fetch_all_thread_comments "$thread")
    else
      comment_ids="$first_comment_ids"
    fi
    
    enriched_thread=$(echo "$thread_json" "$comment_ids" | python3 -c "import json,sys; t=json.loads(sys.stdin.readline()); cids=json.loads(sys.stdin.readline()); t['comments']={'nodes':[{'id':cid} for cid in cids]}; print(json.dumps(t))")
    paged_threads=$(echo "$paged_threads" "$enriched_thread" | python3 -c "import json,sys; a=json.loads(sys.stdin.readline() or '[]'); b=json.loads(sys.stdin.readline()); a.append(b); print(json.dumps(a))")
  done
done

echo "$paged_threads" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps({'data': {'repository': {'pullRequest': {'reviewThreads': {'nodes': d}}}}}))" > "$THREADS_JSON"

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
    line_count=$(wc -l < "$prompt_file")
    head -n 220 "$prompt_file"
    echo
    echo "---"
    echo "[Prompt truncated to 220 lines; total lines: $line_count]"
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
