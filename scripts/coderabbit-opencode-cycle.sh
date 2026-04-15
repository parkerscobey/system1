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

if [[ -z "$REPO" ]]; then
  echo "Invalid --repo value: expected OWNER/REPO" >&2
  exit 1
fi

if [[ ! "$REPO" =~ ^[^/]+/[^/]+$ ]]; then
  echo "Invalid --repo value: expected OWNER/REPO" >&2
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
META_JSON="$TMP_DIR/prompt-meta.json"

OWNER=${REPO%%/*}
REPO_NAME=${REPO#*/}

gh api "repos/$REPO/issues/$PR_NUMBER/comments" --paginate > "$ISSUE_JSON"
gh api "repos/$REPO/pulls/$PR_NUMBER/reviews" --paginate > "$REVIEW_JSON"
gh api "repos/$REPO/pulls/$PR_NUMBER/comments" --paginate > "$INLINE_JSON"

# Fetch review threads with cursor-based pagination and comment-level pagination,
# including comment bodies for prompt extraction and thread resolution tracking.
python3 - "$OWNER" "$REPO_NAME" "$PR_NUMBER" "$THREADS_JSON" <<'PY'
import json, subprocess, sys

owner, repo, pr_number, out_path = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]

all_threads = []
cursor = None
has_more = True

while has_more:
    query = (
        'query($owner:String!, $repo:String!, $number:Int!, $cursor:String) {'
        ' repository(owner:$owner, name:$repo) {'
        ' pullRequest(number:$number) {'
        ' reviewThreads(first:100, after:$cursor) {'
        ' pageInfo { hasNextPage endCursor }'
        ' nodes { id isResolved path comments(first:100) {'
        ' pageInfo { hasNextPage endCursor }'
        ' nodes { id body author { login } } } } } } } }'
    )
    cmd = ["gh", "api", "graphql", "-f", f"query={query}", "-f", f"owner={owner}", "-f", f"repo={repo}", "-F", f"number={pr_number}", "-F", f"cursor={cursor or 'null'}", "--jq", ".data"]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"gh api failed (code {result.returncode}): {cmd}\nstderr: {result.stderr}\nstdout: {result.stdout}")
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError as e:
        raise RuntimeError(f"failed to parse JSON from gh api: {cmd}\nstdout: {result.stdout[:500]}\nerror: {e}")
    threads_obj = data["repository"]["pullRequest"]["reviewThreads"]
    page_info = threads_obj["pageInfo"]

    for thread in threads_obj["nodes"]:
        comment_data = thread.get("comments") or {}
        comment_page_info = comment_data.get("pageInfo") or {}
        comment_nodes = comment_data.get("nodes", [])

        if comment_page_info.get("hasNextPage"):
            c_cursor = comment_page_info.get("endCursor")
            while True:
                c_cmd = ["gh", "api", "graphql", "-f", "query=" +
                    'query($threadId:ID!, $cursor:String) { node(id:$threadId) { ... on ReviewThread { comments(first:100, after:$cursor) { pageInfo { hasNextPage endCursor } nodes { id body author { login } } } } } }',
                    "-F", f"threadId={thread['id']}", "-F", f"cursor={c_cursor or 'null'}", "--jq", ".data"]
                c_result = subprocess.run(c_cmd, capture_output=True, text=True)
                if c_result.returncode != 0:
                    raise RuntimeError(f"gh api failed (code {c_result.returncode}): {c_cmd}\nstderr: {c_result.stderr}")
                try:
                    c_data = json.loads(c_result.stdout)
                except json.JSONDecodeError as e:
                    raise RuntimeError(f"failed to parse JSON: {c_cmd}\nstdout: {c_result.stdout[:500]}")
                c_comms = c_data["node"]["comments"]
                comment_nodes.extend(c_comms.get("nodes", []))
                c_pi = c_comms.get("pageInfo", {})
                if not c_pi.get("hasNextPage"):
                    break
                c_cursor = c_pi.get("endCursor")

        thread["comments"] = {"nodes": comment_nodes}
        all_threads.append(thread)

    has_more = page_info.get("hasNextPage", False)
    cursor = page_info.get("endCursor")

output = {"data": {"repository": {"pullRequest": {"reviewThreads": {"nodes": all_threads}}}}}
with open(out_path, "w") as f:
    json.dump(output, f)
PY

# Extract prompts. Uses GraphQL comment bodies from review threads (thread_id
# tracking) and REST API bodies from issue/review comments (no thread tracking).
python3 - "$ISSUE_JSON" "$REVIEW_JSON" "$INLINE_JSON" "$THREADS_JSON" "$PROMPTS_JSON" "$INCLUDE_AGGREGATE" "$INCLUDE_RESOLVED" <<'PY'
import json, pathlib, re, sys
issue_path, review_path, inline_path, threads_path, out_path, include_aggregate, include_resolved = sys.argv[1:8]
include_aggregate = include_aggregate == "1"
include_resolved = include_resolved == "1"

# Load review threads to build comment_id → thread_id mapping and detect resolved.
threads_data = json.loads(pathlib.Path(threads_path).read_text())
thread_nodes = (((threads_data.get("data") or {}).get("repository") or {}).get("pullRequest") or {}).get("reviewThreads", {}).get("nodes", [])

comment_to_thread = {}
resolved_comment_ids = set()
for thread in thread_nodes:
    thread_id = thread.get("id")
    is_resolved = thread.get("isResolved", False)
    for comment in ((thread.get("comments") or {}).get("nodes") or []):
        cid = comment.get("id")
        if cid and thread_id:
            comment_to_thread[cid] = thread_id
            if is_resolved:
                resolved_comment_ids.add(cid)

# Collect sources from REST API (issues, reviews, inline PR comments).
rest_sources = []
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
        rest_sources.append({"body": body, "comment_id": str(node_id), "thread_id": ""})

# Collect sources from GraphQL review threads (includes body, has thread_id).
graphql_sources = []
for thread in thread_nodes:
    thread_id = thread.get("id", "")
    for comment in ((thread.get("comments") or {}).get("nodes") or []):
        user = ((comment.get("author") or {}).get("login") or "")
        body = comment.get("body") or ""
        cid = comment.get("id") or ""
        if user not in {"coderabbitai", "coderabbitai[bot]"} or not body:
            continue
        if not include_resolved and cid in resolved_comment_ids:
            continue
        graphql_sources.append({"body": body, "comment_id": cid, "thread_id": thread_id})

# Deduplicate: prefer GraphQL sources (they have thread_id). Fall back to REST.
seen_comments = set(s["body"] for s in graphql_sources)
all_sources = list(graphql_sources)
for s in rest_sources:
    if s["body"] not in seen_comments:
        all_sources.append(s)
        seen_comments.add(s["body"])

pattern = re.compile(
    r"<summary>🤖\s*(?P<title>Prompt[^<]*)</summary>.*?```(?:\w+)?\n(?P<prompt>.*?)```",
    re.S,
)

seen = set()
prompts = []
for src in all_sources:
    for m in pattern.finditer(src["body"]):
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
        prompts.append({
            "title": title,
            "prompt": prompt,
            "aggregate": aggregate,
            "comment_id": src["comment_id"],
            "thread_id": src.get("thread_id", ""),
        })

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

# Write prompt files and metadata (filename → thread_id mapping).
python3 - "$PROMPTS_JSON" "$TMP_DIR" "$META_JSON" <<'PY'
import json, pathlib, re, sys
prompts_path, out_dir, meta_path = sys.argv[1], sys.argv[2], sys.argv[3]
items = json.load(open(prompts_path))
out = pathlib.Path(out_dir)
meta = {}
for i, item in enumerate(items, start=1):
    slug = re.sub(r'[^a-z0-9]+', '-', item['title'].lower()).strip('-') or f'prompt-{i}'
    filename = f'{i:03d}-{slug}.txt'
    path = out / filename
    path.write_text(item['prompt'])
    meta[filename] = {
        "title": item["title"],
        "thread_id": item.get("thread_id", ""),
        "comment_id": item.get("comment_id", ""),
    }
pathlib.Path(meta_path).write_text(json.dumps(meta, indent=2))
PY

PROMPT_FILES=()
while IFS= read -r prompt_file; do
  PROMPT_FILES+=("$prompt_file")
done < <(find "$TMP_DIR" -maxdepth 1 -name '*.txt' | sort)

if [[ "$DRY_RUN" != "1" ]]; then
  require_bin opencode
fi

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
