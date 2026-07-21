#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/admin-session.sh"

harbor_version="${HARBOR_VERSION:-2.15.0}"
project="${COMPOSE_PROJECT_NAME:-regstair-harbor-smoke}"
harbor_project="${HARBOR_COMPOSE_PROJECT_NAME:-regstair-harbor}"
admin_password="${HARBOR_ADMIN_PASSWORD:-regstair-harbor-admin}"
harbor_test_username="${HARBOR_TEST_USERNAME:-regstairtestuser}"
harbor_test_password="${HARBOR_TEST_PASSWORD:-Regstair-Harbor-Test-42}"
client_username="smoke-admin"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${REGSTAIR_HARBOR_RUNTIME:-$repo_root/.runtime/harbor-smoke}"
installer_dir="$runtime_root/harbor"
installer_archive="$runtime_root/harbor-online-installer-v$harbor_version.tgz"

free_port() {
  python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()'
}

harbor_port="${HARBOR_PORT:-$(free_port)}"
regstair_port="${REGSTAIR_PORT:-$(free_port)}"
harbor_url="http://127.0.0.1:$harbor_port"
regstair_url="http://127.0.0.1:$regstair_port"
run_root="$runtime_root/runs/$harbor_port"
config_path="$run_root/regstair-harbor.yaml"
robot_file="$run_root/robot.json"
credential_key_file="$run_root/regstair-credential-key"
credential_key_id="harbor-smoke"
credential_key_mount="$credential_key_file"

for command in docker curl jq sha256sum awk python3 tar sed go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing required command: $command" >&2
    exit 1
  fi
done

mkdir -p "$runtime_root" "$run_root/data" "$run_root/log"

wait_http() {
  local url="$1" name="$2"
  shift 2
  for _ in $(seq 1 180); do
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

count_uploads() {
  local root="$1"
  if [[ ! -d "$root" ]]; then printf '0\n'; return; fi
  find "$root" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l
}

download_harbor() {
  if [[ ! -x "$installer_dir/prepare" ]]; then
    echo "Downloading Harbor v$harbor_version online installer..."
    curl -fL -o "$installer_archive" \
      "https://github.com/goharbor/harbor/releases/download/v$harbor_version/harbor-online-installer-v$harbor_version.tgz"
    tar -xzf "$installer_archive" -C "$runtime_root"
  fi
}

configure_harbor() {
  awk \
    -v hostname="host.docker.internal" \
    -v port="$harbor_port" \
    -v password="$admin_password" \
    -v data="$run_root/data" \
    -v logs="$run_root/log" '
      /^https:/ { skip_https=1; next }
      skip_https && /^[^[:space:]#]/ { skip_https=0 }
      skip_https { next }
      /^hostname:/ { print "hostname: " hostname; next }
      /^  port: 80$/ { print "  port: " port; next }
      /^harbor_admin_password:/ { print "harbor_admin_password: " password; next }
      /^data_volume:/ { print "data_volume: " data; next }
      /^    location:/ { print "    location: " logs; next }
      { print }
    ' "$installer_dir/harbor.yml.tmpl" >"$installer_dir/harbor.yml"
}

harbor_token() {
  local repository="$1" actions="$2"
  curl -fsS -u "$robot_name:$robot_secret" --get \
    --data-urlencode 'service=harbor-registry' \
    --data-urlencode "scope=repository:$repository:$actions" \
    "$harbor_url/service/token" | jq -er '.token // .access_token'
}

upload_blob() {
  local repository="$1" blob_digest="$2" body="$3" token="$4"
  local headers location upload_url
  headers="$(mktemp)"
  curl -fsS -H "Authorization: Bearer $token" -D "$headers" -o /dev/null \
    -X POST "$harbor_url/v2/$repository/blobs/uploads/"
  location="$(awk 'tolower($1) == "location:" {print $2}' "$headers" | tr -d '\r' | tail -n 1)"
  rm -f "$headers"
  if [[ "$location" == http://* || "$location" == https://* ]]; then
    upload_url="$location"
    upload_url="${upload_url/host.docker.internal/127.0.0.1}"
  else
    upload_url="$harbor_url$location"
  fi
  [[ "$upload_url" == *\?* ]] && upload_url="$upload_url&digest=$blob_digest" || upload_url="$upload_url?digest=$blob_digest"
  printf '%s' "$body" | curl -fsS -H "Authorization: Bearer $token" -o /dev/null \
    -X PUT -H 'Content-Type: application/octet-stream' --data-binary @- "$upload_url"
}

put_harbor_manifest() {
  local repository="$1" reference="$2" manifest="$3" token="$4"
  printf '%s' "$manifest" | curl -fsS -H "Authorization: Bearer $token" -o /dev/null \
    -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data-binary @- \
    "$harbor_url/v2/$repository/manifests/$reference"
}

regstair_upload_blob() {
  local repository="$1" blob_digest="$2" body="$3" token="$4"
  local location upload_url
  location="$(curl -fsS -H "Authorization: Bearer $token" -D - -o /dev/null \
    -X POST "$regstair_url/v2/$repository/blobs/uploads/" |
    awk 'tolower($1) == "location:" {print $2}' | tr -d '\r' | tail -n 1)"
  upload_url="$regstair_url$location"
  [[ "$upload_url" == *\?* ]] && upload_url="$upload_url&digest=$blob_digest" || upload_url="$upload_url?digest=$blob_digest"
  printf '%s' "$body" | curl -fsS -H "Authorization: Bearer $token" -o /dev/null \
    -X PUT -H 'Content-Type: application/octet-stream' --data-binary @- "$upload_url"
}

regstair_token() {
  local repository="$1" actions="$2"
  curl -fsS -u "$client_username:$client_password" --get \
    --data-urlencode 'service=regstair' \
    --data-urlencode "scope=repository:$repository:$actions" \
    "$regstair_url/auth/token" | jq -er '.token // .access_token'
}

regstair_anonymous_token() {
  local repository="$1" actions="$2"
  curl -fsS --get \
    --data-urlencode 'service=regstair' \
    --data-urlencode "scope=repository:$repository:$actions" \
    "$regstair_url/auth/token" | jq -er '.token // .access_token'
}

stop_harbor() {
  (cd "$installer_dir" && docker compose -p "$harbor_project" stop >/dev/null)
}

start_harbor() {
  (cd "$installer_dir" && docker compose -p "$harbor_project" up -d >/dev/null)
  wait_http "$harbor_url/api/v2.0/ping" "Harbor API"
}

echo "Preparing Harbor v$harbor_version..."
download_harbor
configure_harbor
docker compose -p "$project" down -v >/dev/null 2>&1 || true
(
  cd "$installer_dir"
  docker compose -p "$harbor_project" down -v >/dev/null 2>&1 || true
  ./prepare
  docker run --rm -v "$installer_dir/common/config:/config" --entrypoint chmod registry:2 -R a+rX /config
  docker compose -p "$harbor_project" up -d
)

wait_http "$harbor_url/api/v2.0/ping" "Harbor API"

echo "Creating Harbor project and least-privilege robot account..."
project_status="$(curl -sS -u "admin:$admin_password" -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' -d '{"project_name":"regstair","public":false}' \
  "$harbor_url/api/v2.0/projects")"
if [[ "$project_status" != "201" && "$project_status" != "409" ]]; then
  echo "Harbor project creation returned status $project_status" >&2
  exit 1
fi

echo "Creating an ordinary Harbor user with Developer access to the private project..."
user_status="$(curl -sS -u "admin:$admin_password" -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg username "$harbor_test_username" --arg password "$harbor_test_password" '{username:$username,password:$password,email:($username+"@example.test"),realname:"Regstair integration user"}')" \
  "$harbor_url/api/v2.0/users")"
if [[ "$user_status" != "201" && "$user_status" != "409" ]]; then
  echo "Harbor user creation returned status $user_status" >&2
  exit 1
fi

member_status="$(curl -sS -u "admin:$admin_password" -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg username "$harbor_test_username" '{role_id:2,member_user:{username:$username}}')" \
  "$harbor_url/api/v2.0/projects/regstair/members")"
if [[ "$member_status" != "201" && "$member_status" != "409" ]]; then
  echo "Harbor project membership returned status $member_status" >&2
  exit 1
fi

robot_payload="$(jq -nc --arg name "gateway-$harbor_port" '{
  name:$name,
  description:"Regstair Harbor integration fixture",
  disable:false,
  duration:-1,
  level:"project",
  permissions:[{
    kind:"project",
    namespace:"regstair",
    access:[
      {resource:"repository",action:"pull"},
      {resource:"repository",action:"push"}
    ]
  }]
}')"
curl -fsS -u "admin:$admin_password" -o "$robot_file" -X POST \
  -H 'Content-Type: application/json' -d "$robot_payload" "$harbor_url/api/v2.0/robots"

robot_name="$(jq -er '.name' "$robot_file")"
robot_secret="$(jq -er '.secret' "$robot_file")"

verification_repo_before="$(curl -fsS -u "admin:$admin_password" "$harbor_url/api/v2.0/projects/regstair/repositories?page_size=100" | jq '[.[].name] | index("regstair/credential-check") != null')"
verification_upload_root="$run_root/data/registry/docker/registry/v2/repositories/regstair/credential-check/_uploads"
verification_uploads_before="$(count_uploads "$verification_upload_root")"
echo "Verifying real Harbor credential classification and non-mutating push-scope grant..."
REGSTAIR_HARBOR_VERIFY_TEST=1 \
REGSTAIR_HARBOR_ENDPOINT="$harbor_url" \
REGSTAIR_HARBOR_USERNAME="$harbor_test_username" \
REGSTAIR_HARBOR_SECRET="$harbor_test_password" \
GOCACHE="${GOCACHE:-/tmp/regstair-go-cache}" \
  go test ./internal/auth -run TestHarborCredentialVerifierIntegration -v
verification_repo_after="$(curl -fsS -u "admin:$admin_password" "$harbor_url/api/v2.0/projects/regstair/repositories?page_size=100" | jq '[.[].name] | index("regstair/credential-check") != null')"
verification_uploads_after="$(count_uploads "$verification_upload_root")"
if [[ "$verification_repo_before" != "$verification_repo_after" || "$verification_repo_after" != "false" ]]; then
  echo "Harbor verification unexpectedly created a persistent repository artifact" >&2
  exit 1
fi
if [[ "$verification_uploads_before" != "$verification_uploads_after" ]]; then
  echo "Harbor verification left temporary upload state behind ($verification_uploads_before before, $verification_uploads_after after)" >&2
  exit 1
fi

cat >"$config_path" <<'YAML'
version: 1
sources:
  - id: harbor-team-a
    name: Harbor Team A
    endpoint: http://host.docker.internal:HARBOR_PORT
    type: internal
    enabled: true
    auth:
    user_credentials:
      pull: true
      push: true
      verification_repository: regstair/credential-check
      allow_insecure: true
routes:
  - name: harbor-team-a
    match: team-a/**
    precedence: 10
    pull:
      sources: [harbor-team-a]
      authoritative: harbor-team-a
      external_fallback: false
    push:
      destination: harbor-team-a
    rewrite:
      strip_prefix: team-a/
      add_prefix: regstair/
YAML
sed -i "s/HARBOR_PORT/$harbor_port/" "$config_path"

umask 077
chmod 0600 "$credential_key_file" 2>/dev/null || true
head -c 32 /dev/urandom >"$credential_key_file"
chmod 0444 "$credential_key_file"

config_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{}}'
layer_body='seeded in Harbor for Regstair'
config_digest="$(digest "$config_body")"
layer_digest="$(digest "$layer_body")"
manifest="$(jq -nc --arg config "$config_digest" --arg layer "$layer_digest" '{schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$config,size:90},layers:[{mediaType:"application/vnd.oci.image.layer.v1.tar",digest:$layer,size:29}]}')"
seed_token="$(harbor_token regstair/base pull,push)"
upload_blob regstair/base "$config_digest" "$config_body" "$seed_token"
upload_blob regstair/base "$layer_digest" "$layer_body" "$seed_token"
put_harbor_manifest regstair/base 1.0 "$manifest" "$seed_token"

echo "Starting Regstair for per-user Harbor credential testing..."
REGSTAIR_CONFIG="$config_path" \
REGSTAIR_PORT="$regstair_port" \
REGSTAIR_CREDENTIAL_KEY_ID="$credential_key_id" \
REGSTAIR_CREDENTIAL_KEY_FILE="$credential_key_mount" \
REGSTAIR_HTTPS_LISTEN= \
REGSTAIR_HTTPS_PORT=0 \
  docker compose -p "$project" up -d --build regstair
wait_http "$regstair_url/healthz" "Regstair"

echo "Bootstrapping the local administrator and saving that user's verified Harbor credential..."
bootstrap_admin_session "$regstair_url" "$client_username" "smoke administrator password"
admin_post "$regstair_url" "/admin/api/account/registry-credentials/harbor-team-a/verify-and-save" \
  "$(jq -nc --arg username "$harbor_test_username" --arg secret "$harbor_test_password" '{username:$username,secret:$secret}')" |
  jq -e --arg username "$harbor_test_username" '.source_id == "harbor-team-a" and .username == $username and (.secret | not)' >/dev/null
client_password="$(admin_post "$regstair_url" "/admin/api/account/docker-tokens" \
  '{"label":"harbor-smoke","expires_in_days":1}' | jq -er '.secret')"

echo "Verifying Harbor pull through Regstair..."
client_pull_token="$(regstair_token team-a/base pull)"
curl -fsS -H "Authorization: Bearer $client_pull_token" \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json' -o /dev/null \
  "$regstair_url/v2/team-a/base/manifests/1.0"

echo "Stopping Harbor and proving authorization-safe private cache replay..."
stop_harbor
curl -fsS -H "Authorization: Bearer $client_pull_token" \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json' -o /dev/null \
  "$regstair_url/v2/team-a/base/manifests/1.0"
for cached_digest in "$config_digest" "$layer_digest"; do
  curl -fsS -H "Authorization: Bearer $client_pull_token" -o /dev/null \
    "$regstair_url/v2/team-a/base/blobs/$cached_digest"
done
anonymous_pull_token="$(regstair_anonymous_token team-a/base pull)"
anonymous_status="$(curl -sS -H "Authorization: Bearer $anonymous_pull_token" -o /dev/null -w '%{http_code}' "$regstair_url/v2/team-a/base/manifests/1.0")"
if [[ "$anonymous_status" != "401" && "$anonymous_status" != "403" ]]; then
  echo "anonymous private cache replay returned $anonymous_status, want 401 or 403" >&2
  exit 1
fi

curl -fsS -X DELETE \
  -H "Cookie: $ADMIN_COOKIE_HEADER" \
  -H "X-CSRF-Token: $ADMIN_CSRF_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary '{"confirm":true}' \
  "$regstair_url/admin/api/account/registry-credentials/harbor-team-a" >/dev/null
removed_status="$(curl -sS -H "Authorization: Bearer $client_pull_token" -o /dev/null -w '%{http_code}' "$regstair_url/v2/team-a/base/manifests/1.0")"
if [[ "$removed_status" != "401" && "$removed_status" != "403" ]]; then
  echo "removed credential private cache replay returned $removed_status, want 401 or 403" >&2
  exit 1
fi

echo "Restarting Harbor, replacing the credential, and proving the same-user grant remains valid..."
start_harbor
admin_post "$regstair_url" "/admin/api/account/registry-credentials/harbor-team-a/verify-and-save" \
  "$(jq -nc --arg username "$harbor_test_username" --arg secret "$harbor_test_password" '{username:$username,secret:$secret}')" >/dev/null
stop_harbor
curl -fsS -H "Authorization: Bearer $client_pull_token" \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json' -o /dev/null \
  "$regstair_url/v2/team-a/base/manifests/1.0"
start_harbor

echo "Verifying Harbor push through Regstair..."
client_push_token="$(regstair_token team-a/published pull,push)"
push_body='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"source":"regstair"}}'
push_digest="$(digest "$push_body")"
regstair_upload_blob team-a/published "$push_digest" "$push_body" "$client_push_token"
push_manifest="$(jq -nc --arg digest "$push_digest" --argjson size "${#push_body}" '{schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$digest,size:$size},layers:[]}')"
printf '%s' "$push_manifest" | curl -fsS -H "Authorization: Bearer $client_push_token" -o /dev/null \
  -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data-binary @- \
  "$regstair_url/v2/team-a/published/manifests/1.0"

pull_token="$(harbor_token regstair/published pull)"
curl -fsS -H "Authorization: Bearer $pull_token" \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json' -o /dev/null \
  "$harbor_url/v2/regstair/published/manifests/1.0"

admin_get "$regstair_url" "/admin/api/requests?limit=20" | jq -e '
  any(.requests[]; .logical_reference == "team-a/base:1.0" and .client_identity != "" and .credential_source == "current_user" and .operation == "pull" and .status == "success") and
  any(.requests[]; .logical_reference == "team-a/published:1.0" and .client_identity != "" and .credential_source == "current_user" and .operation == "push" and .status == "success")
' >/dev/null

echo "Harbor integration smoke test passed."
echo "Harbor: $harbor_url"
echo "Regstair: $regstair_url"
echo "Stop Regstair: docker compose -p $project down"
echo "Stop Harbor: (cd $installer_dir && docker compose -p $harbor_project down)"
