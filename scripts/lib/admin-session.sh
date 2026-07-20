#!/usr/bin/env bash

bootstrap_admin_session() {
  local base_url="$1"
  local username="${2:-smoke-admin}"
  local password="${3:-smoke administrator password}"
  local setup_token headers response

  headers="$(mktemp)"
  setup_token="$(curl -fsS "$base_url/admin/api/setup" | jq -er '.setup_token')"
  response="$(curl -fsS -D "$headers" -X POST "$base_url/admin/api/setup" \
    -H 'Content-Type: application/json' \
    -H "X-Regstair-Setup-Token: $setup_token" \
    --data-binary "$(jq -nc --arg username "$username" --arg password "$password" \
      '{username:$username,password:$password,display_name:"Smoke test administrator"}')")"

  ADMIN_SESSION_COOKIE="$(awk 'tolower($1) == "set-cookie:" {print $2}' "$headers" | tr -d '\r' | cut -d';' -f1 | tail -n 1)"
  ADMIN_CSRF_TOKEN="$(jq -er '.csrf_token' <<<"$response")"
  rm -f "$headers"

  if [[ -z "$ADMIN_SESSION_COOKIE" || -z "$ADMIN_CSRF_TOKEN" ]]; then
    echo "first-run setup did not return an administrator session" >&2
    return 1
  fi
}

admin_get() {
  local base_url="$1"
  local path="$2"
  curl -fsS -H "Cookie: $ADMIN_SESSION_COOKIE" "$base_url$path"
}

admin_post() {
  local base_url="$1"
  local path="$2"
  local body="$3"
  curl -fsS -X POST \
    -H "Cookie: $ADMIN_SESSION_COOKIE" \
    -H "X-CSRF-Token: $ADMIN_CSRF_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary "$body" \
    "$base_url$path"
}
