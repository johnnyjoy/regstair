#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/admin-session.sh"

project="${COMPOSE_PROJECT_NAME:-regstair-smoke}"
regstair_url="${REGSTAIR_URL:-http://127.0.0.1:8080}"
internal_url="${INTERNAL_REGISTRY_URL:-http://127.0.0.1:5001}"
external_url="${EXTERNAL_REGISTRY_URL:-http://127.0.0.1:5002}"
destination_url="${DESTINATION_REGISTRY_URL:-http://127.0.0.1:5003}"

cleanup() {
  docker compose -p "$project" up -d external-registry >/dev/null 2>&1 || true
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

  local headers location upload_url patch_location
  local auth_args=()
  if [[ "$base_url" == "$regstair_url" && -n "${regstair_client_password:-}" ]]; then
    auth_args=(-H "Authorization: Bearer $(regstair_access_token "$repository" "push")")
  fi
  headers="$(mktemp)"
  curl -fsS "${auth_args[@]}" -D "$headers" -o /dev/null -X POST "$base_url/v2/$repository/blobs/uploads/"
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
    curl -fsS "${auth_args[@]}" -D "$headers" -o /dev/null \
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

  curl -fsS "${auth_args[@]}" -o /dev/null -X PUT "$upload_url"
}

registry_put_manifest() {
  local base_url="$1"
  local repository="$2"
  local reference="$3"
  local manifest="$4"

  local auth_args=()
  if [[ "$base_url" == "$regstair_url" && -n "${regstair_client_password:-}" ]]; then
    auth_args=(-H "Authorization: Bearer $(regstair_access_token "$repository" "push")")
  fi
  printf '%s' "$manifest" |
    curl -fsS "${auth_args[@]}" -o /dev/null \
      -X PUT \
      -H "Content-Type: application/vnd.oci.image.manifest.v1+json" \
      --data-binary @- \
      "$base_url/v2/$repository/manifests/$reference"
}

regstair_upload_blob() {
  registry_upload_blob "$regstair_url" "$1" "$2" "$3"
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

regstair_access_token() {
  local repository="${1:-}"
  local actions="${2:-}"
  local query="service=regstair"
  local auth_args=()
  if [[ -n "$repository" ]]; then
    query="$query&scope=repository:$repository:$actions"
  fi
  if [[ -n "${regstair_client_password:-}" ]]; then
    auth_args=(-u "$regstair_client_username:$regstair_client_password")
  fi
  curl -fsS "${auth_args[@]}" "$regstair_url/auth/token?$query" | jq -er '.token'
}

assert_http_status() {
  local expected="$1"
  local url="$2"
  local status

  local auth_args=()
  if [[ "$url" == "$regstair_url/v2/"* ]]; then
    local repository="${url#"$regstair_url/v2/"}"
    repository="${repository%%/manifests/*}"
    repository="${repository%%/blobs/*}"
    auth_args=(-H "Authorization: Bearer $(regstair_access_token "$repository" "pull")")
  fi
  status="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_args[@]}" -H "$manifest_accept" "$url")"
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

echo "Starting Compose environment..."
docker compose -p "$project" down -v >/dev/null 2>&1 || true
docker compose -p "$project" up -d --build

wait_http "$internal_url/v2/" "internal registry"
wait_http "$external_url/v2/" "external registry"
wait_http "$destination_url/v2/" "destination registry"
wait_http "$regstair_url/healthz" "regstair"
for _ in $(seq 1 60); do
  if ping_token="$(regstair_access_token)" && curl -fsS -H "Authorization: Bearer $ping_token" "$regstair_url/v2/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if [[ -z "${ping_token:-}" ]]; then
  echo "timed out waiting for regstair OCI endpoint" >&2
  exit 1
fi
wait_http "$regstair_url/" "regstair UI"

config_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{}}'
layer_body='hello regstair'
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
          size: 14
        }
      ]
    }'
)"
external_manifest_digest="$(sha256_digest "$external_manifest")"

echo "Seeding external registry with library/nginx:1.27..."
registry_upload_blob "$external_url" "library/nginx" "$config_digest" "$config_body"
registry_upload_blob "$external_url" "library/nginx" "$layer_digest" "$layer_body"
registry_put_manifest "$external_url" "library/nginx" "1.27" "$external_manifest"

echo "Seeding external registry with library/alpine:edge using shared blobs..."
registry_upload_blob "$external_url" "library/alpine" "$config_digest" "$config_body"
registry_upload_blob "$external_url" "library/alpine" "$layer_digest" "$layer_body"
registry_put_manifest "$external_url" "library/alpine" "edge" "$external_manifest"

echo "Seeding external registry with platform/api:1.0.0 to prove protected fallback is blocked..."
registry_upload_blob "$external_url" "platform/api" "$config_digest" "$config_body"
registry_upload_blob "$external_url" "platform/api" "$layer_digest" "$layer_body"
registry_put_manifest "$external_url" "platform/api" "1.0.0" "$external_manifest"

echo "Pulling library/nginx:1.27 through Regstair..."
assert_http_status "200" "$regstair_url/v2/library/nginx/manifests/1.27"
assert_http_status "200" "$regstair_url/v2/library/nginx/blobs/$layer_digest"

echo "Pulling library/alpine:edge through Regstair to prove shared-blob deduplication..."
assert_http_status "200" "$regstair_url/v2/library/alpine/manifests/edge"
assert_http_status "200" "$regstair_url/v2/library/alpine/blobs/$layer_digest"

echo "Stopping external registry and replaying pull from Regstair cache..."
docker compose -p "$project" stop external-registry >/dev/null
assert_http_status "200" "$regstair_url/v2/library/nginx/manifests/1.27"
assert_http_status "200" "$regstair_url/v2/library/nginx/blobs/$layer_digest"

echo "Verifying protected namespace blocks external fallback..."
assert_http_status "404" "$regstair_url/v2/platform/api/manifests/1.0.0"

echo "Restarting external registry for cleanup-friendly final state..."
docker compose -p "$project" up -d external-registry >/dev/null
wait_http "$external_url/v2/" "external registry"

echo "Creating the local administrator and Docker token for the push phase..."
regstair_client_username="smoke-admin"
bootstrap_admin_session "$regstair_url" "$regstair_client_username" "smoke administrator password"
regstair_client_password="$(admin_post "$regstair_url" "/admin/api/account/docker-tokens" \
  '{"label":"compose-smoke","expires_in_days":1}' | jq -er '.secret')"

push_config_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"name":"team-a"}}'
push_layer_body='pushed through regstair'
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
        size: 98
      },
      layers: [
        {
          mediaType: "application/vnd.oci.image.layer.v1.tar",
          digest: $layer_digest,
          size: 22
        }
      ]
    }'
)"
push_manifest_digest="$(sha256_digest "$push_manifest")"

echo "Pushing team-a/service:4.1 through Regstair..."
regstair_upload_blob "team-a/service" "$push_config_digest" "$push_config_body"
regstair_upload_blob "team-a/service" "$push_layer_digest" "$push_layer_body"
registry_put_manifest "$regstair_url" "team-a/service" "4.1" "$push_manifest"

echo "Verifying push landed in destination registry under rewritten namespace..."
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

echo "Verifying admin API exposes routes, requests, artifacts, and provenance..."
admin_body="$(admin_get "$regstair_url" "/")"
if ! grep -q 'aria-label="System health"' <<<"$admin_body"; then
  echo "authenticated overview did not render the system health region" >&2
  exit 1
fi
admin_get "$regstair_url" "/admin/api/sources" | jq -e '.sources | length == 3' >/dev/null
admin_get "$regstair_url" "/admin/api/routes" | jq -e '.routes | length == 3' >/dev/null
admin_get "$regstair_url" "/admin/api/artifacts" |
  jq -e '.artifacts[] | select(.logical_reference == "library/nginx:1.27" and .mapping.digest == "'"$external_manifest_digest"'")' >/dev/null
admin_get "$regstair_url" "/admin/api/artifacts" |
  jq -e '.artifacts[] | select(.logical_reference == "library/alpine:edge" and .mapping.digest == "'"$external_manifest_digest"'")' >/dev/null
admin_get "$regstair_url" "/admin/api/artifacts" |
  jq -e '[.artifacts[] | select((.logical_reference == "library/nginx:1.27" or .logical_reference == "library/alpine:edge") and (.mapping.blob_digests | index("'"$layer_digest"'")))] | length == 2' >/dev/null
admin_get "$regstair_url" "/admin/api/cache" |
  jq -e '[.blobs[] | select(.digest == "'"$layer_digest"'")] | length == 1' >/dev/null
admin_get "$regstair_url" "/admin/api/cache" |
  jq -e '[.blobs[] | select(.digest == "'"$config_digest"'")] | length == 1' >/dev/null
admin_get "$regstair_url" "/admin/api/provenance?reference=library/nginx:1.27" |
  jq -e '.provenance.source == "external-registry" and .provenance.fallback_used == true' >/dev/null
admin_get "$regstair_url" "/admin/api/provenance?reference=team-a/service:4.1" |
  jq -e '.provenance.source == "harbor-team-a" and .provenance.resolved_digest == "'"$push_manifest_digest"'"' >/dev/null
admin_get "$regstair_url" "/admin/api/requests?limit=20" |
  jq -e '
    any(.requests[]; .logical_reference == "library/nginx:1.27" and .cache_result == "miss") and
    any(.requests[]; .logical_reference == "library/nginx:1.27" and .cache_result == "hit") and
    any(.requests[]; .logical_reference == "platform/api:1.0.0" and .status == "denied") and
    any(.requests[]; .logical_reference == "team-a/service:4.1" and .operation == "push" and .status == "success")
  ' >/dev/null

echo "Compose smoke test passed."
echo "External cached digest: $external_manifest_digest"
echo "Pushed digest: $push_manifest_digest"
