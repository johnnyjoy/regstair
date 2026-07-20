#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/admin-session.sh"

project="${COMPOSE_PROJECT_NAME:-regstair-auth-smoke}"
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

regstair_port="$(choose_port REGSTAIR_PORT 28080)"
internal_port="$(choose_port INTERNAL_REGISTRY_PORT 25001)"
external_port="$(choose_port EXTERNAL_REGISTRY_PORT 25002)"
destination_port="$(choose_port DESTINATION_REGISTRY_PORT 25003)"

regstair_url="http://127.0.0.1:$regstair_port"
internal_url="http://127.0.0.1:$internal_port"
external_url="http://127.0.0.1:$external_port"
destination_url="http://127.0.0.1:$destination_port"

tmpdir="$(mktemp -d)"
config_path="$tmpdir/regstair-auth.yaml"

cleanup() {
  docker compose -p "$project" up -d external-registry >/dev/null 2>&1 || true
  rm -rf "$tmpdir"
}
trap cleanup EXIT

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

sha256_digest() {
  printf '%s' "$1" | sha256sum | awk '{print "sha256:" $1}'
}

registry_upload_blob() {
  local base_url="$1"
  local repository="$2"
  local digest="$3"
  local body="$4"
  local curl_auth=("${@:5}")

  local headers location upload_url patch_location
  headers="$(mktemp)"
  printf '%s' "$body" >/dev/null
  curl -fsS "${curl_auth[@]}" -D "$headers" -o /dev/null -X POST "$base_url/v2/$repository/blobs/uploads/"
  location="$(awk 'tolower($1) == "location:" {print $2}' "$headers" | tr -d '\r' | tail -n 1)"
  rm -f "$headers"

  if [[ -z "$location" ]]; then
    echo "registry did not return an upload location for $repository" >&2
    exit 1
  fi

  if [[ "$location" == http://* || "$location" == https://* ]]; then
    upload_url="$location"
  elif [[ "$location" == /* ]]; then
    upload_url="$base_url$location"
  else
    upload_url="$base_url/$location"
  fi

  headers="$(mktemp)"
  printf '%s' "$body" |
    curl -fsS "${curl_auth[@]}" -D "$headers" -o /dev/null \
      -X PATCH \
      -H "Content-Type: application/octet-stream" \
      --data-binary @- \
      "$upload_url"
  patch_location="$(awk 'tolower($1) == "location:" {print $2}' "$headers" | tr -d '\r' | tail -n 1)"
  rm -f "$headers"

  if [[ -n "$patch_location" ]]; then
    if [[ "$patch_location" == http://* || "$patch_location" == https://* ]]; then
      upload_url="$patch_location"
    elif [[ "$patch_location" == /* ]]; then
      upload_url="$base_url$patch_location"
    else
      upload_url="$base_url/$patch_location"
    fi
  fi

  if [[ "$upload_url" == *\?* ]]; then
    upload_url="$upload_url&digest=$digest"
  else
    upload_url="$upload_url?digest=$digest"
  fi

  curl -fsS "${curl_auth[@]}" -o /dev/null -X PUT "$upload_url"
}

registry_put_manifest() {
  local base_url="$1"
  local repository="$2"
  local reference="$3"
  local manifest="$4"
  local curl_auth=("${@:5}")

  printf '%s' "$manifest" |
    curl -fsS "${curl_auth[@]}" -o /dev/null \
      -X PUT \
      -H "Content-Type: application/vnd.oci.image.manifest.v1+json" \
      --data-binary @- \
      "$base_url/v2/$repository/manifests/$reference"
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

assert_http_status() {
  local expected="$1"
  local url="$2"
  local status
  local curl_auth=("${@:3}")

  status="$(curl -sS "${curl_auth[@]}" -o /dev/null -w '%{http_code}' -H "$manifest_accept" "$url")"
  if [[ "$status" != "$expected" ]]; then
    echo "unexpected status for $url: got $status, want $expected" >&2
    exit 1
  fi
}

require docker
require curl
require jq
require sha256sum
require awk

manifest_accept='Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json'
client_auth=(-u "$client_username:$client_password")

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

echo "Starting authenticated Compose environment..."
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

echo "Verifying /v2/ requires client auth..."
assert_http_status "401" "$regstair_url/v2/"
assert_http_status "200" "$regstair_url/v2/" "${client_auth[@]}"

config_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{}}'
layer_body='hello authenticated regstair'
config_digest="$(sha256_digest "$config_body")"
layer_digest="$(sha256_digest "$layer_body")"

external_manifest="$(
  jq -nc \
    --arg config_digest "$config_digest" \
    --arg layer_digest "$layer_digest" \
    '{
      schemaVersion: 2,
      mediaType: "application/vnd.oci.image.manifest.v1+json",
      config: {
        mediaType: "application/vnd.oci.image.config.v1+json",
        digest: $config_digest,
        size: 82
      },
      layers: [
        {
          mediaType: "application/vnd.oci.image.layer.v1.tar",
          digest: $layer_digest,
          size: 27
        }
      ]
    }'
)"
external_manifest_digest="$(sha256_digest "$external_manifest")"

echo "Seeding external registry with allowed and denied pull targets..."
registry_upload_blob "$external_url" "library/nginx" "$config_digest" "$config_body"
registry_upload_blob "$external_url" "library/nginx" "$layer_digest" "$layer_body"
registry_put_manifest "$external_url" "library/nginx" "1.27" "$external_manifest"
registry_upload_blob "$external_url" "platform/api" "$config_digest" "$config_body"
registry_upload_blob "$external_url" "platform/api" "$layer_digest" "$layer_body"
registry_put_manifest "$external_url" "platform/api" "1.0.0" "$external_manifest"

echo "Verifying allowed pull succeeds with one Regstair login..."
assert_http_status "200" "$regstair_url/v2/library/nginx/manifests/1.27" "${client_auth[@]}"
assert_http_status "200" "$regstair_url/v2/library/nginx/blobs/$layer_digest" "${client_auth[@]}"

echo "Verifying denied pull route is rejected and audited..."
assert_http_status "403" "$regstair_url/v2/platform/api/manifests/1.0.0" "${client_auth[@]}"

push_config_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"name":"team-a-auth"}}'
push_layer_body='pushed with client auth'
push_config_digest="$(sha256_digest "$push_config_body")"
push_layer_digest="$(sha256_digest "$push_layer_body")"
push_manifest="$(
  jq -nc \
    --arg config_digest "$push_config_digest" \
    --arg layer_digest "$push_layer_digest" \
    '{
      schemaVersion: 2,
      mediaType: "application/vnd.oci.image.manifest.v1+json",
      config: {
        mediaType: "application/vnd.oci.image.config.v1+json",
        digest: $config_digest,
        size: 103
      },
      layers: [
        {
          mediaType: "application/vnd.oci.image.layer.v1.tar",
          digest: $layer_digest,
          size: 23
        }
      ]
    }'
)"
push_manifest_digest="$(sha256_digest "$push_manifest")"

echo "Verifying allowed push succeeds and routes to the destination registry..."
registry_upload_blob "$regstair_url" "team-a/service" "$push_config_digest" "$push_config_body" "${client_auth[@]}"
registry_upload_blob "$regstair_url" "team-a/service" "$push_layer_digest" "$push_layer_body" "${client_auth[@]}"
registry_put_manifest "$regstair_url" "team-a/service" "4.1" "$push_manifest" "${client_auth[@]}"
assert_http_status "200" "$destination_url/v2/production-team-a/service/manifests/4.1"

destination_digest="$(
  curl -fsSI -H "$manifest_accept" "$destination_url/v2/production-team-a/service/manifests/4.1" |
    awk 'tolower($1) == "docker-content-digest:" {print $2}' |
    tr -d '\r' |
    tail -n 1
)"

if [[ "$destination_digest" != "$push_manifest_digest" ]]; then
  echo "destination manifest digest mismatch: got $destination_digest, want $push_manifest_digest" >&2
  exit 1
fi

echo "Verifying auth-aware admin request log..."
bootstrap_admin_session "$regstair_url"
admin_get "$regstair_url" "/admin/api/provenance?reference=library/nginx:1.27" |
  jq -e '.provenance.source == "external-registry" and .provenance.resolved_digest == "'"$external_manifest_digest"'"' >/dev/null
admin_get "$regstair_url" "/admin/api/provenance?reference=team-a/service:4.1" |
  jq -e '.provenance.source == "harbor-team-a" and .provenance.resolved_digest == "'"$push_manifest_digest"'"' >/dev/null
admin_get "$regstair_url" "/admin/api/requests?limit=20" |
  jq -e '
    any(.requests[]; .logical_reference == "library/nginx:1.27" and .client_identity == "ci-builder" and .status == "success") and
    any(.requests[]; .logical_reference == "platform/api:1.0.0" and .client_identity == "ci-builder" and .status == "denied" and .error_classification == "authorization_denied") and
    any(.requests[]; .logical_reference == "team-a/service:4.1" and .client_identity == "ci-builder" and .operation == "push" and .status == "success")
  ' >/dev/null

echo "Authenticated Compose smoke test passed."
echo "Allowed pull digest: $external_manifest_digest"
echo "Allowed push digest: $push_manifest_digest"
