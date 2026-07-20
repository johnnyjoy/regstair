#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/admin-session.sh"

project="${COMPOSE_PROJECT_NAME:-regstair-docker-client-smoke}"
client_username="${REGSTAIR_CLIENT_CI_USERNAME:-ci}"
client_password="${REGSTAIR_CLIENT_CI_PASSWORD:-secret}"

free_port() {
  python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()'
}

choose_port() {
  local env_name="$1"
  local fallback="$2"
  local configured="${!env_name:-}"
  if [[ -n "$configured" ]]; then
    printf '%s\n' "$configured"
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    free_port
    return
  fi
  printf '%s\n' "$fallback"
}

regstair_port="$(choose_port REGSTAIR_PORT 38080)"
internal_port="$(choose_port INTERNAL_REGISTRY_PORT 35001)"
external_port="$(choose_port EXTERNAL_REGISTRY_PORT 35002)"
destination_port="$(choose_port DESTINATION_REGISTRY_PORT 35003)"

regstair_host="127.0.0.1:$regstair_port"
external_host="127.0.0.1:$external_port"
destination_url="http://127.0.0.1:$destination_port"
regstair_url="http://$regstair_host"
internal_url="http://127.0.0.1:$internal_port"
external_url="http://$external_host"

tmpdir="$(mktemp -d)"
config_path="$tmpdir/regstair-docker-client.yaml"
docker_config="$tmpdir/docker-config"
build_context="$tmpdir/build"
mkdir -p "$docker_config" "$build_context"

cleanup() {
  docker logout "$regstair_host" >/dev/null 2>&1 || true
  docker image rm \
    "$external_host/library/docker-client-smoke:1.0" \
    "$regstair_host/library/docker-client-smoke:1.0" \
    "$regstair_host/team-a/docker-client-smoke:1.0" \
    >/dev/null 2>&1 || true
  rm -rf "$tmpdir"
}
trap cleanup EXIT

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

wait_http() {
  local url="$1"
  local name="$2"

  for _ in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "timed out waiting for $name at $url" >&2
  exit 1
}

require docker
require curl
require jq
require python3

cat >"$config_path" <<'YAML'
version: 1

clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
    allowed:
      pull:
        - curated-library
      push:
        - team-a-publish

sources:
  - id: internal-curated
    name: Internal Curated Registry
    endpoint: http://internal-registry:5000
    type: internal
    enabled: true

  - id: external-registry
    name: External Registry Stand-In
    endpoint: http://external-registry:5000
    type: external
    enabled: true

  - id: harbor-team-a
    name: Team A Destination Registry
    endpoint: http://destination-registry:5000
    type: internal
    enabled: true

routes:
  - name: curated-library
    match: library/**
    precedence: 10
    pull:
      sources:
        - internal-curated
        - external-registry
      authoritative: internal-curated
      external_fallback: true
    push:
      destination: internal-curated
    rewrite:
      strip_prefix: library/
      add_prefix: library/

  - name: protected-platform
    match: platform/**
    precedence: 20
    pull:
      sources:
        - internal-curated
      authoritative: internal-curated
      external_fallback: false
    push:
      destination: internal-curated

  - name: team-a-publish
    match: team-a/**
    precedence: 30
    pull:
      sources:
        - harbor-team-a
      authoritative: harbor-team-a
      external_fallback: false
    push:
      destination: harbor-team-a
    rewrite:
      strip_prefix: team-a/
      add_prefix: production-team-a/
YAML

cat >"$build_context/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY hello.txt /hello.txt
DOCKERFILE
printf 'hello from docker client smoke\n' >"$build_context/hello.txt"

echo "Starting Docker client compatibility environment..."
docker compose -p "$project" down -v >/dev/null 2>&1 || true
REGSTAIR_CONFIG="$config_path" \
REGSTAIR_PORT="$regstair_port" \
INTERNAL_REGISTRY_PORT="$internal_port" \
EXTERNAL_REGISTRY_PORT="$external_port" \
DESTINATION_REGISTRY_PORT="$destination_port" \
REGSTAIR_CLIENT_CI_USERNAME="$client_username" \
REGSTAIR_CLIENT_CI_PASSWORD="$client_password" \
  docker compose -p "$project" up -d --build

wait_http "$internal_url/v2/" "internal registry"
wait_http "$external_url/v2/" "external registry"
wait_http "$destination_url/v2/" "destination registry"
wait_http "$regstair_url/healthz" "regstair"
wait_http "$regstair_url/admin/" "regstair admin UI"

echo "Building and seeding a real Docker image into the external registry..."
docker build -t "$external_host/library/docker-client-smoke:1.0" "$build_context"
docker push "$external_host/library/docker-client-smoke:1.0"

docker image rm "$external_host/library/docker-client-smoke:1.0" >/dev/null

echo "Verifying Docker pull requires Regstair login..."
if DOCKER_CONFIG="$docker_config" docker pull "$regstair_host/library/docker-client-smoke:1.0" >/dev/null 2>&1; then
  echo "docker pull unexpectedly succeeded without login" >&2
  exit 1
fi

echo "Logging Docker client into Regstair..."
printf '%s\n' "$client_password" |
  DOCKER_CONFIG="$docker_config" docker login "$regstair_host" --username "$client_username" --password-stdin >/dev/null

echo "Pulling through Regstair with Docker..."
DOCKER_CONFIG="$docker_config" docker pull "$regstair_host/library/docker-client-smoke:1.0"

echo "Verifying unauthorized Docker pull is denied..."
if DOCKER_CONFIG="$docker_config" docker pull "$regstair_host/platform/api:1.0.0" >/dev/null 2>&1; then
  echo "docker pull unexpectedly succeeded for unauthorized protected route" >&2
  exit 1
fi

echo "Pushing through Regstair with Docker..."
docker tag "$regstair_host/library/docker-client-smoke:1.0" "$regstair_host/team-a/docker-client-smoke:1.0"
DOCKER_CONFIG="$docker_config" docker push "$regstair_host/team-a/docker-client-smoke:1.0"

echo "Verifying pushed image landed at rewritten destination..."
curl -fsSI \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
  "$destination_url/v2/production-team-a/docker-client-smoke/manifests/1.0" >/dev/null

echo "Verifying Docker client activity is visible in admin request history..."
bootstrap_admin_session "$regstair_url"
admin_get "$regstair_url" "/admin/api/requests?limit=30" |
  jq -e '
    any(.requests[]; .logical_reference == "library/docker-client-smoke:1.0" and .client_identity == "ci-builder" and .operation == "pull" and .status == "success") and
    any(.requests[]; .logical_reference == "platform/api:1.0.0" and .client_identity == "ci-builder" and .status == "denied" and .error_classification == "authorization_denied") and
    any(.requests[]; .logical_reference == "team-a/docker-client-smoke:1.0" and .client_identity == "ci-builder" and .operation == "push" and .status == "success")
  ' >/dev/null

echo "Docker client compatibility smoke test passed."
