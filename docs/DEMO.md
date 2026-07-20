# Regstair Demo Guide

This guide maps the automated smoke test to the Build Week demo story.

## Goal

Show that container users can talk to one OCI endpoint while Regstair applies policy for pull routing, push routing, caching, provenance, and namespace protection.

Suggested opening:

```text
Container users should not need to know where every image physically lives. Regstair provides one OCI endpoint and routes pulls and pushes according to organizational policy.
```

## Start

Run:

```bash
./scripts/compose-smoke.sh
```

The script starts a Compose project named `regstair-smoke` unless `COMPOSE_PROJECT_NAME` is set.

When it passes, leave the stack running and open:

```text
http://127.0.0.1:8080/
```

For the authenticated path, run:

```bash
./scripts/compose-auth-smoke.sh
```

That script starts a separate Compose project named `regstair-auth-smoke` by default and chooses available host ports dynamically unless the port environment variables are set.

To prove that clients authenticate once to Regstair while Regstair owns the upstream credential:

```bash
./scripts/compose-proxy-auth-smoke.sh
```

This starts the optional `proxy-auth` Compose profile, seeds a registry protected by Basic auth, verifies pull and push through Regstair, checks redacted admin auth status, and confirms a bad stored upstream credential fails.

To run the real Harbor integration:

```bash
./scripts/harbor-smoke.sh
```

The script downloads the pinned official Harbor installer on first use, generates its Compose deployment under `.runtime/harbor-smoke`, creates a private `regstair` project and a least-privilege project robot, then proves Harbor pull and push through Regstair. It prints both local URLs and exact shutdown commands when complete.

For real Docker CLI compatibility, run:

```bash
./scripts/docker-client-smoke.sh
```

That script starts a separate Compose project named `regstair-docker-client-smoke`, builds a scratch image with Docker, pushes it to the external stand-in registry, logs into Regstair, pulls through Regstair, verifies a denied route, and pushes through Regstair to the rewritten destination.

## What The Script Proves

### Scenario A: External Pull And Cache

The script seeds the external stand-in registry with:

```text
library/nginx:1.27
```

Then it requests the manifest and blob through Regstair:

```text
GET /v2/library/nginx/manifests/1.27
GET /v2/library/nginx/blobs/<digest>
```

Expected result:

- Regstair checks `internal-curated` first.
- The image is not present there.
- Regstair falls back to `external-registry`.
- Regstair caches the manifest and blobs by digest.
- Admin request history shows a cache miss.
- Provenance shows `source: external-registry` and `fallback_used: true`.

Admin checks:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/provenance?reference=library/nginx:1.27 | jq
curl -fsS http://127.0.0.1:8080/admin/api/requests?limit=20 | jq
```

### Scenario B: Protected Internal Namespace

The script seeds the external stand-in registry with:

```text
platform/api:1.0.0
```

Then it requests the same reference through Regstair:

```text
GET /v2/platform/api/manifests/1.0.0
```

Expected result:

- Regstair matches the `protected-platform` route.
- The authoritative source is `internal-curated`.
- External fallback is blocked.
- The request returns `404`.
- Admin request history records a denied request.

Admin check:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/requests?limit=20 |
  jq '.requests[] | select(.logical_reference == "platform/api:1.0.0")'
```

### Scenario C: Push Routing

The script uploads blobs and a manifest through Regstair at:

```text
team-a/service:4.1
```

The route rewrites the namespace and publishes to the destination registry:

```text
production-team-a/service:4.1
```

Expected result:

- Regstair stages uploaded blobs locally.
- Regstair verifies the manifest digest.
- Regstair selects `harbor-team-a` as the destination.
- Regstair publishes blobs and manifest to the destination registry.
- Admin provenance records the push destination and digest.

Direct destination check:

```bash
curl -fsSI \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
  http://127.0.0.1:5003/v2/production-team-a/service/manifests/4.1
```

### Scenario D: Digest Deduplication

The script seeds two external repositories using the same config and layer blobs:

```text
library/nginx:1.27
library/alpine:edge
```

Expected result:

- Both tag mappings reference the shared blob digest.
- The local content store keeps one blob object for that digest.
- Admin cache API reports the shared digest once.

Admin checks:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/artifacts | jq
curl -fsS http://127.0.0.1:8080/admin/api/cache | jq
```

## Manual Inspection

List sources:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/sources | jq
```

List routes:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/routes | jq
```

List recent requests:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/requests?limit=20 | jq
```

List cached tag mappings:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/artifacts | jq
```

List cached blobs:

```bash
curl -fsS http://127.0.0.1:8080/admin/api/cache | jq
```

## Reset

Stop containers but keep cached content and SQLite metadata:

```bash
docker compose -p regstair-smoke down
```

Stop containers and remove the persistent demo volume:

```bash
docker compose -p regstair-smoke down -v
```

Stop the authenticated smoke stack:

```bash
docker compose -p regstair-auth-smoke down
```

Remove its persistent volume:

```bash
docker compose -p regstair-auth-smoke down -v
```

Stop and remove the proxy-auth smoke stack:

```bash
docker compose -p regstair-proxy-auth-smoke --profile proxy-auth down -v
```

Stop the Docker client smoke stack:

```bash
docker compose -p regstair-docker-client-smoke down
```

Remove its persistent volume:

```bash
docker compose -p regstair-docker-client-smoke down -v
```

## Demo Close

Suggested closing:

```text
Regstair separates logical image identity from physical registry location, providing routing, publication, caching, provenance, and policy through one stable interface.
```
