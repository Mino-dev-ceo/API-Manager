#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:3000}"
API_KEY="${API_KEY:-}"
MODEL="${MODEL:-claude-sonnet-4-6}"

if [[ -z "$API_KEY" ]]; then
  echo "API_KEY is required. Example: API_KEY=sk-xxxx BASE_URL=https://minotoken.xyz MODEL=claude-sonnet-4-6 $0" >&2
  exit 2
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

contains_json_key() {
  local file="$1"
  local key="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -e "$key" "$file" >/dev/null
  else
    grep -q '"'"${key//./}"'"' "$file"
  fi
}

check_no_internal_terms() {
  local file="$1"
  local lower
  lower="$(tr '[:upper:]' '[:lower:]' < "$file")"
  local terms=(
    "kiro"
    "aws"
    "amazon"
    "bedrock"
    "relay"
    "upstream"
    "channel"
    "reverse proxy"
    "new-api"
    "new api"
    "反代"
    "中转"
    "渠道"
    "上游"
    "内部路由"
  )
  for term in "${terms[@]}"; do
    if grep -q "$term" <<<"$lower"; then
      fail "client response leaks internal implementation term: $term"
    fi
  done
}

echo "==> checking models endpoint"
curl -fsS "$BASE_URL/v1/models" \
  -H "Authorization: Bearer $API_KEY" \
  -o "$tmp_dir/models.json"
contains_json_key "$tmp_dir/models.json" ".data" || fail "models response does not contain data"
check_no_internal_terms "$tmp_dir/models.json"

echo "==> checking non-stream chat completion"
curl -fsS "$BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"请用一句中文回复：连通正常"}],"stream":false}' \
  -o "$tmp_dir/chat.json"
contains_json_key "$tmp_dir/chat.json" ".choices" || fail "chat response does not contain choices"
check_no_internal_terms "$tmp_dir/chat.json"

echo "==> checking stream chat completion"
curl -fsS -N "$BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"回复 OK"}],"stream":true}' \
  -o "$tmp_dir/stream.txt"
grep -q '^data:' "$tmp_dir/stream.txt" || fail "stream response does not contain SSE data lines"
grep -q '\[DONE\]' "$tmp_dir/stream.txt" || fail "stream response does not contain [DONE]"
check_no_internal_terms "$tmp_dir/stream.txt"

echo "==> MINO compatibility check passed"
