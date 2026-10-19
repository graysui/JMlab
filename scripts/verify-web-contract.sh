#!/usr/bin/env bash
set -euo pipefail

# Lightweight contract smoke test. Run inside the deployment host or against a
# forwarded Compose port after the image has been built and marked healthy.
BASE_URL="${JMLAB_BASE_URL:-http://127.0.0.1:8097}"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

expect_status() {
  local expected="$1"
  local path="$2"
  local body="$tmp_dir/body"
  local status
  status="$(curl -sS -o "$body" -w '%{http_code}' "$BASE_URL$path")"
  if [[ "$status" != "$expected" ]]; then
    echo "${path}: expected HTTP ${expected}, got ${status}" >&2
    cat "$body" >&2
    return 1
  fi
}

expect_method_status() {
  local expected="$1"
  local method="$2"
  local path="$3"
  local body="$tmp_dir/body"
  local status
  status="$(curl -sS -o "$body" -w '%{http_code}' -X "$method" -H 'Content-Type: application/json' -d '{}' "$BASE_URL$path")"
  if [[ "$status" != "$expected" ]]; then
    echo "${method} ${path}: expected HTTP ${expected}, got ${status}" >&2
    cat "$body" >&2
    return 1
  fi
}

for path in / /admin /admin/items /admin/tasks /admin/data /admin/backup /rankings /search; do
  expect_status 200 "$path"
done

expect_status 200 /health/live
expect_status 200 /health/ready

for path in /api/v1/home /api/v1/me/preferences; do
  expect_status 401 "$path"
done
expect_method_status 401 POST /api/v1/items/import
expect_method_status 401 POST /api/v1/rankings/generate

error_body="$tmp_dir/error.json"
error_status="$(curl -sS -o "$error_body" -w '%{http_code}' "$BASE_URL/api/v1/me/preferences")"
[[ "$error_status" == "401" ]]
jq -e '.error.code and .error.message and .error.requestId' "$error_body" >/dev/null

echo "Web contract OK: ${BASE_URL}"
