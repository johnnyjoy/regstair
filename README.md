# Regstair

Regstair is a policy-driven OCI registry gateway. It gives developers and CI systems one stable registry endpoint while administrators decide where pulls and pushes actually go.

The Build Week MVP proves four core behaviors:

- pull through Regstair with internal-first routing and approved external fallback;
- replay a cached pull when the external source is unavailable;
- block fallback for protected internal namespaces;
- push through Regstair to a policy-selected destination with namespace rewriting;
- deduplicate shared blobs in the local content-addressed store.

## Current Status

This repository contains a narrow, testable MVP implementation:

- minimal OCI Distribution HTTP gateway in Go;
- YAML policy configuration;
- HTTP connectors for local `registry:2` sources and destinations;
- filesystem content-addressed blob store;
- SQLite metadata repository for request events, provenance, and tag mappings;
- read-only admin UI and JSON API;
- Docker Compose demo environment;
- Basic client auth for the Regstair `/v2/` endpoint;
- auth config scaffolding for proxy-owned upstream Basic credentials.

Live Docker Hub, GHCR, Harbor auth, UI editing, cache eviction, TLS termination, and broader OCI artifact support are intentionally deferred.

## Product Design

The authoritative interface direction, information architecture, accessibility standard, and implementation slices are defined in [docs/UI_UX_DESIGN.md](docs/UI_UX_DESIGN.md). The supporting [hostile UI/UX audit](docs/UI_UX_HOSTILE_AUDIT.md) records the usability and security findings that motivated it.

## Quick Start

Requirements:

- Docker with Docker Compose;
- `curl`;
- `jq`;
- `sha256sum`;
- `awk`.

Run the automated demo smoke test:

```bash
./scripts/compose-smoke.sh
```

The script builds Regstair, starts the Compose environment, seeds local registries, runs the MVP scenarios, verifies the admin API, and leaves the stack running for inspection.

For the scenario-by-scenario walkthrough, see [docs/DEMO.md](docs/DEMO.md).

Run the authenticated smoke test:

```bash
./scripts/compose-auth-smoke.sh
```

Prove Regstair-owned credentials against an authenticated upstream registry:

```bash
./scripts/compose-proxy-auth-smoke.sh
```

Run the automated Harbor integration:

```bash
./scripts/harbor-smoke.sh
```

The Harbor harness pins the official online installer, generates a local Compose deployment under `.runtime/harbor-smoke`, creates a private project and least-privilege robot account, and proves authenticated pull and push through Regstair. The first run downloads Harbor's component images.

Run the same fixture as a clean next-level release flow:

```bash
REGSTAIR_NEXT_LEVEL_SMOKE=1 ./scripts/harbor-smoke.sh
```

This mode starts with a fresh Regstair volume and credential key, bootstraps the first local administrator, verifies and encrypts that user's Harbor credential, issues a time-limited Docker token, and proves pull and push use the authenticated user's credential.

That script generates a temporary auth-enabled config, starts an isolated Compose project on dynamically selected host ports, verifies `/v2/` Basic auth, proves an allowed pull and push succeed, and checks that a denied route is audited with `authorization_denied`.

Run the real Docker client smoke test:

```bash
./scripts/docker-client-smoke.sh
```

That script uses `docker build`, `docker push`, `docker login`, `docker pull`, and routed `docker push` against Regstair using a temporary Docker config directory.

Open the admin UI:

```text
http://127.0.0.1:8080/
```

Stop the demo stack:

```bash
docker compose -p regstair-smoke down
```

Remove the persistent demo volume as well:

```bash
docker compose -p regstair-smoke down -v
```

## Demo Topology

The default Compose environment uses local `registry:2` containers so the demo is reproducible without private infrastructure.

```text
Docker / curl / CI
   |
   v
Regstair :8080
   |
   +--> internal-curated     http://internal-registry:5000
   +--> external-registry    http://external-registry:5000
   +--> harbor-team-a        http://destination-registry:5000
```

Published host ports:

- Regstair: `127.0.0.1:8080`
- internal curated registry: `127.0.0.1:5001`
- external registry stand-in: `127.0.0.1:5002`
- destination registry stand-in: `127.0.0.1:5003`

Regstair stores blobs and SQLite metadata under the container content root:

```text
/var/lib/regstair/content
```

Compose mounts that path through the named volume:

```text
regstair-data
```

The SQLite database defaults to:

```text
/var/lib/regstair/content/metadata/regstair.db
```

## Routing Policy

The demo policy lives in [config/regstair.example.yaml](config/regstair.example.yaml).

The important routes are:

- `curated-library`: `library/**` pulls check `internal-curated` first, then `external-registry`; fallback is allowed.
- `protected-platform`: `platform/**` pulls check only `internal-curated`; external fallback is blocked.
- `team-a-publish`: `team-a/**` pushes publish to `harbor-team-a` and rewrite `team-a/` to `production-team-a/`.

This means a request for:

```text
library/nginx:1.27
```

checks the internal registry first, then the approved external source, and then caches the content locally by digest.

A request for:

```text
platform/api:1.0.0
```

does not fall back externally, even if the same image exists in the external stand-in registry.

A push to:

```text
team-a/service:4.1
```

lands in the destination registry as:

```text
production-team-a/service:4.1
```

## Control Plane API

Useful endpoints:

```text
GET /healthz
GET /readyz
GET /v2/
GET /
GET /admin/api/sources
GET /admin/api/routes
GET /admin/api/requests?limit=20
GET /admin/api/artifacts
GET /admin/api/cache
GET /admin/api/provenance?reference=library/nginx:1.27
```

The web application presents operational data read-only while supporting authenticated account, token, credential, and user-management workflows. Request history can be filtered by reference, client identity, route, operation, source or destination, status, error classification, and UTC time range. Filters are stored in the URL and request pages use stable cursor pagination.

Measured cache performance, deduplication, current capacity limits, and recommended hardening are recorded in [Cache Speed and Capacity Evaluation](docs/CACHE_EVALUATION.md). Reproduce the local 64 MiB test with `./scripts/cache-benchmark.sh`.

`GET /admin/api/requests` accepts the same filter names plus `limit` (1-100) and an opaque `cursor`. Successful responses include `requests` and, when another page exists, `next_cursor`.

## Local Development

Run unit and package tests:

```bash
GOCACHE=/tmp/regstair-go-cache go test ./...
```

Build the static binary path used by Docker:

```bash
GOCACHE=/tmp/regstair-go-cache CGO_ENABLED=0 go build ./cmd/regstair
```

Run with local stub connectors:

```bash
go run ./cmd/regstair \
  -config=config/regstair.example.yaml \
  -content-root=/tmp/regstair/content \
  -stub-sources \
  -stub-fixtures
```

Run with real HTTP connectors through Compose:

```bash
docker compose up -d --build
```

Stop the default Compose project:

```bash
docker compose down
```

## Auth

### First administrator bootstrap

Regstair has no default administrator and never prints or generates an administrator password. A new deployment exposes a dedicated setup workflow and keeps the operational dashboard and APIs closed until setup completes. Compose binds Regstair to loopback by default.

```bash
docker compose up -d --build
```

Open `http://127.0.0.1:8080/admin/`. Regstair redirects to `/admin/setup`, creates the first enabled administrator transactionally, signs that administrator in, and permanently closes setup.

Headless automation uses the same one-shot JSON API. Fetch the ephemeral setup token, then submit the chosen administrator values from the same trusted host:

```bash
setup_token=$(curl -fsS http://127.0.0.1:8080/admin/api/setup | jq -r .setup_token)
curl -fsS -X POST http://127.0.0.1:8080/admin/api/setup \
  -H 'Content-Type: application/json' \
  -H "X-Regstair-Setup-Token: $setup_token" \
  --data-binary @admin-setup.json
```

`admin-setup.json` contains `username`, `password`, and optional `display_name` and `email` fields. Protect and remove that file as a deployment secret. The endpoint returns `409` after any user exists. To publish Regstair beyond loopback only after setup, set `REGSTAIR_BIND_ADDRESS` explicitly and terminate TLS before the service.

Before disabling or demoting the last enabled administrator, create or promote another enabled administrator. Regstair rejects changes that would leave no enabled administrator. For host-level recovery, stop the serving container, back up the database, and run:

```bash
docker compose run --rm regstair admin reset-password \
  -metadata-path /var/lib/regstair/content/metadata/regstair.db \
  -username admin \
  -password-file /run/secrets/regstair-admin-password
```

The replacement password is read from the mounted file, never a command argument or environment variable. Recovery revokes that administrator's web sessions and Docker tokens and records a `user.password_recovered` audit event.

Authenticated admin sessions use `Secure`, `HttpOnly`, `SameSite=Strict` cookies. Serve `/admin/` through TLS, normally using a reverse proxy in front of the Compose service. Plain HTTP health, readiness, and OCI endpoints do not make the browser session cookie less restrictive.

Admin login and presented Docker credentials are rate-limited by both source address and normalized account after five failures in five minutes, with a fifteen-minute block and generic `429` response. Credential-free requests are not counted, preserving configured anonymous-pull behavior. The limiter is process-local and intentionally ignores forwarding headers; multi-replica or internet-facing deployments should add a trusted reverse-proxy edge limit.

Before the first local user exists, `/admin/` redirects to setup and operational APIs return `428 setup_required`. There is no anonymous legacy dashboard. The first successful setup immediately places the entire control plane behind session authentication.

### Per-user registry credential key

Per-user upstream credentials are available only when Regstair has a mounted 32-byte encryption key. Generate and mount one separately from the SQLite volume:

```bash
openssl rand -out regstair-credential-key 32
sudo chown 65532:65532 regstair-credential-key
sudo chmod 400 regstair-credential-key
REGSTAIR_CREDENTIAL_KEY_ID=primary-2026 \
REGSTAIR_CREDENTIAL_KEY_FILE="$PWD/regstair-credential-key" \
  docker compose up -d --build
```

Compose mounts the file read-only at `/run/secrets/regstair-credential-key`. The key ID is stored in each encryption envelope, but key bytes never enter YAML, environment variables, SQLite, logs, or API responses. Without a configured key ID, existing OCI behavior remains available and registry-credential APIs fail closed with `503`.

Backup, restore, rotation, missing/wrong-key behavior, and permanent key-loss recovery are defined in [Backup, Restore, and Credential-Key Lifecycle](docs/BACKUP_KEY_LIFECYCLE.md). Losing the only key referenced by stored upstream credentials permanently loses access to those credential values; users must replace them.

Existing environment-backed YAML client deployments can migrate without an authentication flag day. Follow [YAML Client Authentication Upgrade](docs/YAML_AUTH_UPGRADE.md) and run `./scripts/upgrade-smoke.sh` to exercise the legacy-client overlap and local-token cutover against one persistent disposable database.

Authenticated credential endpoints are:

```text
GET    /admin/api/account/registry-credentials
POST   /admin/api/account/registry-credentials/{source_id}/verify-and-save
DELETE /admin/api/account/registry-credentials/{source_id}
```

Verify-and-Save accepts `username` and `secret`; responses contain only credential metadata. Verification failures preserve the machine-readable classifications `invalid_credentials`, `insufficient_permission`, `registry_unavailable`, `verification_configuration_invalid`, and `registry_failure`.

Regstair supports optional Basic client auth for the `/v2/` gateway. When `clients` are configured, Docker/curl clients must authenticate to Regstair and request events record the configured client id.

Client auth example:

```yaml
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
```

With that config enabled:

```bash
docker login 127.0.0.1:8080
```

Configured clients are denied by default for routed pull/push operations. Add route names under `allowed.pull` and `allowed.push` to grant access.

The MVP supports proxy-owned upstream Basic auth. Secrets are read from environment variables, not literal YAML values. The client authenticates to Regstair, while Regstair independently authenticates to the selected upstream.

Example shape:

```yaml
credentials:
  - id: harbor-robot
    type: basic
    username_env: REGSTAIR_HARBOR_USERNAME
    password_env: REGSTAIR_HARBOR_PASSWORD

sources:
  - id: harbor-team-a
    endpoint: http://harbor:8080
    auth:
      mode: proxy
      credential_ref: harbor-robot
```

An approved source can instead select the authenticated local user's encrypted credential:

```yaml
sources:
  - id: harbor-team-a
    endpoint: https://harbor.example
    enabled: true
    auth:
      mode: current_user
      strategy: challenge
    user_credentials:
      approved: true
      pull: true
      push: true
      verification_repository: regstair/credential-check
```

`strategy: challenge` is the default for credentialed sources. Regstair first performs the selected OCI operation anonymously and supplies the configured credential only when that same registry challenges it. This preserves public pulls without decrypting or transmitting a stored credential. `strategy: required` sends the selected credential immediately and should be limited to registries known to require preemptive authentication. Authentication or authorization failure never advances pull fallback.

Bearer token services on the registry endpoint's own host are trusted automatically. A split token service must be approved by host, without a scheme or path:

```yaml
auth:
  mode: proxy
  credential_ref: docker-hub
  strategy: challenge
  token_hosts:
    - auth.docker.io
```

Regstair rejects unapproved or HTTPS-downgrade token realms before sending credentials. Credentialed blob-upload continuations cannot change origin.

`current_user` requires a mounted credential encryption key at startup. It accepts only an authenticated local user, looks up the exact `(user, source)` credential, and never borrows another user's credential. `none` never inspects credentials, and `proxy` continues to use only its configured shared credential.

The `proxy-auth` Compose profile provides a protected local registry fixture. `./scripts/compose-proxy-auth-smoke.sh` proves authenticated pull and push plus failure with an invalid stored upstream credential. The HTTP connector also supports scoped Bearer-token challenge exchange with in-memory expiry caching, and `./scripts/harbor-smoke.sh` proves the real Harbor robot-account integration. GHCR and Docker Hub remain future compatibility targets.

The current hosted-auth proposal is in [docs/AUTH_DESIGN.md](docs/AUTH_DESIGN.md).

The phased admin UI and control-plane boundary is defined in [docs/ADMIN_CONTROL_PLANE.md](docs/ADMIN_CONTROL_PLANE.md).

## Build Week Provenance

Regstair started from [PRD.md](PRD.md) and was implemented test-first around the locked scope in [BUILD_WEEK_SCOPE.md](BUILD_WEEK_SCOPE.md).

The most useful validation command is:

```bash
./scripts/compose-smoke.sh
```

That single command exercises external pull/cache, protected fallback blocking, push routing, namespace rewriting, admin visibility, provenance, SQLite metadata startup, and digest deduplication.

For the authenticated path, run:

```bash
./scripts/compose-auth-smoke.sh
```

For real Docker client compatibility, run:

```bash
./scripts/docker-client-smoke.sh
```

## License

Regstair is available under the [MIT License](LICENSE).
