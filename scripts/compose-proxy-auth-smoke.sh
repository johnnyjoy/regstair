#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/admin-session.sh"

project="${COMPOSE_PROJECT_NAME:-regstair-proxy-auth-smoke}"
client_username="${REGSTAIR_CLIENT_CI_USERNAME:-ci}"
client_password="${REGSTAIR_CLIENT_CI_PASSWORD:-secret}"
upstream_username="${REGSTAIR_UPSTREAM_USERNAME:-upstream}"
upstream_password="${REGSTAIR_UPSTREAM_PASSWORD:-upstream-secret}"

free_port() {
  python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()'
}

regstair_port="${REGSTAIR_PORT:-$(free_port)}"
authenticated_registry_port="${AUTHENTICATED_REGISTRY_PORT:-$(free_port)}"
regstair_url="http://127.0.0.1:$regstair_port"
upstream_url="http://127.0.0.1:$authenticated_registry_port"
tmpdir="$(mktemp -d)"
config_path="$tmpdir/regstair-proxy-auth.yaml"

cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

for command in docker curl jq sha256sum awk python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing required command: $command" >&2
    exit 1
  fi
done

wait_http() {
  local url="$1"
  local name="$2"
  shift 2
  for _ in $(seq 1 60); do
    if curl -fsS "$@" "$url" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  echo "timed out waiting for $name at $url" >&2
  exit 1
}

digest() {
  printf '%s' "$1" | sha256sum | awk '{print "sha256:" $1}'
}

upload_blob() {
  local base_url="$1" repository="$2" blob_digest="$3" body="$4"
  shift 4
  local headers location upload_url
  headers="$(mktemp)"
  curl -fsS "$@" -D "$headers" -o /dev/null -X POST "$base_url/v2/$repository/blobs/uploads/"
  location="$(awk 'tolower($1) == "location:" {print $2}' "$headers" | tr -d '\r' | tail -n 1)"
  rm -f "$headers"
  [[ "$location" == http://* || "$location" == https://* ]] && upload_url="$location" || upload_url="$base_url$location"
  if [[ "$upload_url" == *\?* ]]; then
    upload_url="$upload_url&digest=$blob_digest"
  else
    upload_url="$upload_url?digest=$blob_digest"
  fi
  printf '%s' "$body" | curl -fsS "$@" -o /dev/null -X PUT -H 'Content-Type: application/octet-stream' --data-binary @- "$upload_url"
}

put_manifest() {
  local base_url="$1" repository="$2" reference="$3" manifest="$4"
  shift 4
  printf '%s' "$manifest" | curl -fsS "$@" -o /dev/null -X PUT \
    -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data-binary @- \
    "$base_url/v2/$repository/manifests/$reference"
}

cat >"$config_path" <<'YAML'
version: 1

clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
    allowed:
      pull: [protected-upstream]
      push: [protected-upstream]

credentials:
  - id: protected-registry
    type: basic
    username_env: REGSTAIR_UPSTREAM_USERNAME
    password_env: REGSTAIR_UPSTREAM_PASSWORD

sources:
  - id: protected-registry
    name: Authenticated registry fixture
    endpoint: http://authenticated-registry:5000
    type: internal
    enabled: true
    auth:
      mode: proxy
      credential_ref: protected-registry

routes:
  - name: protected-upstream
    match: secure/**
    precedence: 10
    pull:
      sources: [protected-registry]
      authoritative: protected-registry
      external_fallback: false
    push:
      destination: protected-registry
YAML

compose_up() {
  local proxy_password="$1"
  REGSTAIR_CONFIG="$config_path" \
  REGSTAIR_PORT="$regstair_port" \
  AUTHENTICATED_REGISTRY_PORT="$authenticated_registry_port" \
  REGSTAIR_CLIENT_CI_USERNAME="$client_username" \
  REGSTAIR_CLIENT_CI_PASSWORD="$client_password" \
  REGSTAIR_UPSTREAM_USERNAME="$upstream_username" \
  REGSTAIR_UPSTREAM_PASSWORD="$proxy_password" \
    docker compose -p "$project" --profile proxy-auth up -d --build --force-recreate regstair authenticated-registry
}

echo "Starting proxy-auth fixture..."
docker compose -p "$project" --profile proxy-auth down -v >/dev/null 2>&1 || true
compose_up "$upstream_password"
wait_http "$upstream_url/v2/" "authenticated registry" -u "$upstream_username:$upstream_password"
wait_http "$regstair_url/healthz" "regstair"

anonymous_status="$(curl -sS -o /dev/null -w '%{http_code}' "$upstream_url/v2/")"
if [[ "$anonymous_status" != "401" ]]; then
  echo "authenticated registry accepted anonymous access: status $anonymous_status" >&2
  exit 1
fi

config_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{}}'
layer_body='proxy auth fixture layer'
config_digest="$(digest "$config_body")"
layer_digest="$(digest "$layer_body")"
manifest="$(jq -nc --arg config "$config_digest" --arg layer "$layer_digest" '{schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$config,size:82},layers:[{mediaType:"application/vnd.oci.image.layer.v1.tar",digest:$layer,size:24}]}')"
upstream_auth=(-u "$upstream_username:$upstream_password")
client_auth=(-u "$client_username:$client_password")

echo "Seeding the protected upstream directly..."
upload_blob "$upstream_url" secure/base "$config_digest" "$config_body" "${upstream_auth[@]}"
upload_blob "$upstream_url" secure/base "$layer_digest" "$layer_body" "${upstream_auth[@]}"
put_manifest "$upstream_url" secure/base 1.0 "$manifest" "${upstream_auth[@]}"

echo "Proving pull and push use Regstair-owned upstream credentials..."
curl -fsS "${client_auth[@]}" -o /dev/null "$regstair_url/v2/secure/base/manifests/1.0"
push_layer='pushed through proxy auth'
push_digest="$(digest "$push_layer")"
upload_blob "$regstair_url" secure/published "$push_digest" "$push_layer" "${client_auth[@]}"
push_manifest="$(jq -nc --arg digest "$push_digest" '{schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$digest,size:25},layers:[]}')"
put_manifest "$regstair_url" secure/published 1.0 "$push_manifest" "${client_auth[@]}"
curl -fsS "${upstream_auth[@]}" -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  -o /dev/null "$upstream_url/v2/secure/published/manifests/1.0"

echo "Proving an invalid stored upstream credential fails..."
compose_up wrong-upstream-password
wait_http "$regstair_url/healthz" "regstair with invalid upstream credential"
bad_status="$(curl -sS "${client_auth[@]}" -o /dev/null -w '%{http_code}' "$regstair_url/v2/secure/missing/manifests/1.0")"
if [[ "$bad_status" != "401" ]]; then
  echo "invalid upstream credential returned status $bad_status, want 401" >&2
  exit 1
fi
echo "Restoring the valid upstream credential for inspection..."
compose_up "$upstream_password"
wait_http "$regstair_url/healthz" "restored regstair"
curl -fsS "${client_auth[@]}" -o /dev/null "$regstair_url/v2/secure/base/manifests/1.0"

echo "Verifying proxy-auth configuration and request classifications in the protected admin API..."
bootstrap_admin_session "$regstair_url"
admin_get "$regstair_url" "/admin/api/sources" | jq -e '
  .sources[] | select(.id == "protected-registry") |
  .auth.mode == "proxy" and .auth.credential_ref == "protected-registry" and .auth.configured == true
' >/dev/null
admin_get "$regstair_url" "/admin/api/requests?limit=20" | jq -e '
  any(.requests[]; .client_identity == "ci-builder" and .operation == "pull" and .status == "success") and
  any(.requests[]; .client_identity == "ci-builder" and .operation == "push" and .status == "success") and
  any(.requests[]; .logical_reference == "secure/missing:1.0" and .error_classification == "upstream_authentication_failed")
' >/dev/null

echo "Proxy upstream auth smoke test passed."
echo "Regstair: $regstair_url"
echo "Authenticated upstream: $upstream_url"
