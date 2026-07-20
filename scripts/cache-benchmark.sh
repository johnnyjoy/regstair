#!/usr/bin/env bash
set -euo pipefail

regstair_url="${REGSTAIR_URL:-http://127.0.0.1:8080}"
upstream_url="${UPSTREAM_REGISTRY_URL:-http://127.0.0.1:5002}"
size_bytes="${CACHE_BENCHMARK_SIZE:-67108864}"
repository="library/cache-benchmark"
second_repository="library/cache-benchmark-dedup"
reference="perf"
tmpdir="$(mktemp -d)"
blob="$tmpdir/blob"

cleanup() { rm -rf "$tmpdir"; }
trap cleanup EXIT

for command in curl jq sha256sum awk truncate; do
  command -v "$command" >/dev/null 2>&1 || { echo "missing required command: $command" >&2; exit 1; }
done

upload_blob() {
  local repository_name="$1"
  local digest="$2"
  local headers location upload_url separator
  headers="$tmpdir/headers"
  curl -fsS -D "$headers" -o /dev/null -X POST "$upstream_url/v2/$repository_name/blobs/uploads/"
  location="$(awk 'tolower($1) == "location:" {print $2}' "$headers" | tr -d '\r' | tail -n 1)"
  if [[ "$location" == http://* || "$location" == https://* ]]; then
    upload_url="$location"
  elif [[ "$location" == /* ]]; then
    upload_url="$upstream_url$location"
  else
    upload_url="$upstream_url/$location"
  fi
  [[ "$upload_url" == *\?* ]] && separator='&' || separator='?'
  curl -fsS -o /dev/null -X PUT -H 'Content-Type: application/octet-stream' \
    --data-binary @"$blob" "$upload_url${separator}digest=$digest"
}

put_manifest() {
  local repository_name="$1"
  printf '%s' "$manifest" | curl -fsS -o /dev/null -X PUT \
    -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data-binary @- \
    "$upstream_url/v2/$repository_name/manifests/$reference"
}

truncate -s "$size_bytes" "$blob"
blob_digest="sha256:$(sha256sum "$blob" | awk '{print $1}')"
manifest="$(jq -nc --arg digest "$blob_digest" --argjson size "$size_bytes" \
  '{schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$digest,size:$size},layers:[]}')"

echo "Seeding $size_bytes bytes as $blob_digest..."
upload_blob "$repository" "$blob_digest"
put_manifest "$repository"

echo "Manifest resolution and cache population:"
for run in cold hot-1 hot-2 hot-3 hot-4 hot-5; do
  curl -fsS -o /dev/null \
    -w "$run total=%{time_total}s start=%{time_starttransfer}s response_bytes=%{size_download}\n" \
    -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
    "$regstair_url/v2/$repository/manifests/$reference"
done

echo "Hot blob transfer:"
for run in hot-blob-1 hot-blob-2 hot-blob-3; do
  curl -fsS -o /dev/null \
    -w "$run total=%{time_total}s speed=%{speed_download}B/s bytes=%{size_download}\n" \
    "$regstair_url/v2/$repository/blobs/$blob_digest"
done

echo "Cross-repository deduplication:"
upload_blob "$second_repository" "$blob_digest"
put_manifest "$second_repository"
curl -fsS -o /dev/null -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  "$regstair_url/v2/$second_repository/manifests/$reference"
printf 'shared_digest=%s size=%s\n' "$blob_digest" "$size_bytes"
