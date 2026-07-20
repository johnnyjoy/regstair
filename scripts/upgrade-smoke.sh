#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/admin-session.sh"

project="${COMPOSE_PROJECT_NAME:-regstair-upgrade-smoke}"
legacy_username="${REGSTAIR_CLIENT_CI_USERNAME:-ci}"
legacy_password="${REGSTAIR_CLIENT_CI_PASSWORD:-secret}"
tmpdir="$(mktemp -d)"
legacy_config="$tmpdir/legacy.yaml"
local_config="$tmpdir/local.yaml"

free_port() {
  python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()'
}

regstair_port="${REGSTAIR_PORT:-$(free_port)}"
regstair_url="http://127.0.0.1:$regstair_port"

cleanup() {
  docker compose -p "$project" down -v >/dev/null 2>&1 || true
  rm -rf "$tmpdir"
}
trap cleanup EXIT

for command in docker curl jq python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing required command: $command" >&2
    exit 1
  fi
done

wait_http() {
  for _ in $(seq 1 60); do
    if curl -fsS "$regstair_url/healthz" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  echo "timed out waiting for Regstair at $regstair_url" >&2
  exit 1
}

write_base_config() {
  local path="$1"
  local include_client="$2"
  {
    printf 'version: 1\n'
    if [[ "$include_client" == "1" ]]; then
      cat <<'YAML'
clients:
  - id: legacy-ci
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
    allowed:
      pull: [library]
      push: []
YAML
    fi
    cat <<'YAML'
sources:
  - id: unused
    name: Upgrade fixture
    endpoint: http://unused.invalid
    type: external
    enabled: true
routes:
  - name: library
    match: library/**
    precedence: 10
    pull:
      sources: [unused]
      authoritative: unused
      external_fallback: false
    push:
      deny: true
YAML
  } >"$path"
}

compose_up() {
  local config_path="$1"
  REGSTAIR_CONFIG="$config_path" \
  REGSTAIR_PORT="$regstair_port" \
  REGSTAIR_CLIENT_CI_USERNAME="$legacy_username" \
  REGSTAIR_CLIENT_CI_PASSWORD="$legacy_password" \
    docker compose -p "$project" up -d --build --force-recreate regstair
  wait_http
}

write_base_config "$legacy_config" 1
write_base_config "$local_config" 0

echo "Starting the YAML-client deployment..."
docker compose -p "$project" down -v >/dev/null 2>&1 || true
compose_up "$legacy_config"
curl -fsS -u "$legacy_username:$legacy_password" "$regstair_url/v2/" >/dev/null

echo "Adding local identity while preserving the legacy client..."
bootstrap_admin_session "$regstair_url" upgrade-admin "upgrade administrator password"
docker_token="$(admin_post "$regstair_url" "/admin/api/account/docker-tokens" \
  '{"label":"upgrade-overlap","expires_in_days":1}' | jq -er '.secret')"
curl -fsS -u "upgrade-admin:$docker_token" "$regstair_url/v2/" >/dev/null
curl -fsS -u "$legacy_username:$legacy_password" "$regstair_url/v2/" >/dev/null

echo "Removing YAML clients after local-token validation..."
compose_up "$local_config"
legacy_status="$(curl -sS -u "$legacy_username:$legacy_password" -o /dev/null -w '%{http_code}' "$regstair_url/v2/")"
if [[ "$legacy_status" != "401" ]]; then
  echo "removed YAML client returned $legacy_status, want 401" >&2
  exit 1
fi
curl -fsS -u "upgrade-admin:$docker_token" "$regstair_url/v2/" >/dev/null
curl -fsS "$regstair_url/v2/" >/dev/null
curl -fsS "$regstair_url/admin/api/setup" | jq -e '.required == false' >/dev/null

echo "YAML-auth upgrade smoke test passed."
