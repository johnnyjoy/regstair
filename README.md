<p align="center">
  <img src="frontend/public/regstair-logo.png" alt="Regstair" width="280">
</p>

# Regstair

[![CI](https://github.com/johnnyjoy/regstair/actions/workflows/ci.yml/badge.svg)](https://github.com/johnnyjoy/regstair/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/johnnyjoy/regstair)](https://github.com/johnnyjoy/regstair/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Policy-driven OCI registry routing, caching, and credential mediation through one stable endpoint.**

Regstair gives developers and CI systems one registry address while administrators control where images are pulled from, where they are pushed, which credentials are used, and when cached content may be served.

## Why Regstair

Organizations commonly use several OCI registries: an internal registry, Harbor, and one or more hosted services. Without a gateway, every workstation and CI job must understand those locations, namespaces, and credentials.

Regstair moves that knowledge into deterministic organizational policy:

- route pulls across internal and approved external registries;
- route pushes to controlled destinations with namespace rewriting;
- cache manifests and blobs locally by digest;
- protect authoritative namespaces from unsafe external fallback;
- authenticate Docker users with revocable, time-limited tokens;
- use verified per-user credentials only after routing selects an upstream;
- preserve anonymous pulls when the selected registry permits them;
- explain routing decisions, provenance, cache behavior, and security events.

```text
Docker / CI / OCI client
          |
          v
   Regstair endpoint
     |     |     |
     |     |     +--> policy-selected push destination
     |     +--------> approved external registry
     +--------------> authoritative internal registry
          |
          +----------> local content-addressed cache and provenance
```

Routes determine where an operation goes. Authentication strategy then determines whether Regstair should remain anonymous or answer an upstream challenge with the current user's credential.

## Current Release

The current release is `v0.1.0`:

```bash
docker pull ghcr.io/johnnyjoy/regstair:v0.1.0
```

Published Linux platforms:

- `linux/amd64`
- `linux/arm64`
- `linux/arm/v7`
- `linux/ppc64le`
- `linux/s390x`

Release images include OCI source, revision, version, license, provenance, and SBOM metadata. Regstair does not publish a moving `latest` tag; pin a version or image digest.

## Quick Start

The default Compose deployment starts Regstair as a multi-registry fabric for Docker Hub, GitHub Container Registry, Quay.io, Kubernetes, Google Container Registry, Microsoft Container Registry, Amazon ECR Public, GitLab Container Registry, and NVIDIA NGC. Public content can be pulled anonymously through one cache; signed-in users can connect their own upstream accounts where the provider supports credentials.

Requirements:

- Docker with Docker Compose;
- a browser.

Clone the repository:

```bash
git clone https://github.com/johnnyjoy/regstair.git
cd regstair
```

Build and start the current source:

```bash
docker compose up -d --build
```

Compose creates the credential-encryption key once in a dedicated persistent volume. Back up that volume with the Regstair data volume; losing the key makes saved upstream credentials unrecoverable.

Regstair creates a persistent local certificate authority and an HTTPS certificate covering the Linux server's active network addresses. On each client machine, set the DNS name or IP address used to reach the Regstair server and install its public CA for Docker:

```bash
REGSTAIR_HOST=regstair.example.com
curl -fsS "http://$REGSTAIR_HOST/regstair-ca.crt" -o regstair-ca.crt
sudo mkdir -p "/etc/docker/certs.d/$REGSTAIR_HOST"
sudo cp regstair-ca.crt "/etc/docker/certs.d/$REGSTAIR_HOST/ca.crt"
sudo systemctl restart docker
```

Replace `regstair.example.com` with the server's actual DNS name or IP address. Import the same `regstair-ca.crt` into the workstation or browser trust store, then open `https://REGSTAIR_HOST/setup` and create the first administrator. Setup is available only while the user database is empty and closes permanently after the first account is created. Regstair uses standard host ports `80` and `443`; HTTP serves health checks and the public CA certificate and redirects other requests to HTTPS.

### First Docker Operation

Pull public Docker Hub content through Regstair without adding Docker Hub credentials:

```bash
docker pull "$REGSTAIR_HOST/library/alpine:latest"
docker pull "$REGSTAIR_HOST/tigersmile/nginx-micro:latest"
```

The same endpoint routes other configured registry namespaces:

```bash
docker pull "$REGSTAIR_HOST/ghcr/owner/image:tag"
docker pull "$REGSTAIR_HOST/quay/prometheus/prometheus:latest"
docker pull "$REGSTAIR_HOST/k8s/pause:3.10"
docker pull "$REGSTAIR_HOST/gcr/distroless/static-debian12:nonroot"
docker pull "$REGSTAIR_HOST/mcr/dotnet/runtime:8.0"
docker pull "$REGSTAIR_HOST/ecr-public/amazonlinux/amazonlinux:2023"
docker pull "$REGSTAIR_HOST/gitlab/group/project/image:tag"
docker pull "$REGSTAIR_HOST/nvcr/nvidia/cuda:tag"
```

Docker Hub is the lowest-priority default route, so familiar names such as `tigersmile/nginx-micro` work without a provider prefix. The explicit prefixes select other registries and are defined by routes in `config/regstair.example.yaml`; they are not provider shortcuts embedded in the application. An administrator can rename, remove, reorder, or replace them through configuration.

Repeat the pull to exercise the shared content-addressed cache, then open `https://REGSTAIR_HOST/cache` or `https://REGSTAIR_HOST/requests` to inspect the result.

### Private Content And Pushes

In Regstair, open **Account**, create a Docker token, and retain the token when it is shown. It cannot be displayed again.

Authenticate using your local Regstair username and the token as the password:

```bash
docker login "$REGSTAIR_HOST" --username YOUR_USERNAME
```

Open **Registry access** and connect any configured provider with the credentials issued by that provider. Image names are matched by routing rules; the default routes use concise provider namespaces to make selection explicit without exposing upstream endpoints or credentials. Add organization-specific routes before publishing user-owned repositories.

Open `https://REGSTAIR_HOST/requests` to inspect the authenticated user, selected route, rewritten destination, status, and routing explanation.

Stop the evaluation environment:

```bash
docker compose down
```

Remove its persistent Regstair data as well:

```bash
docker compose down -v
```

## Configuration Model

Regstair deliberately has two configuration authorities:

| Data | Authority |
| --- | --- |
| Routes and configured registries | YAML configuration mounted read-only |
| Local users, roles, sessions, Docker tokens, audit events, and per-user credentials | SQLite |
| Cached manifests and blobs | Filesystem content store |
| Credential-encryption key | Dedicated persistent volume by default; operator-supplied key in managed deployments |

The example policy is [config/regstair.example.yaml](config/regstair.example.yaml). A route declares what it matches, its ordered pull sources, fallback policy, push destination, and optional namespace rewrite:

```yaml
routes:
  - name: docker-hub-library
    match: library/**
    precedence: 10
    pull:
      sources:
        - docker-hub
      authoritative: docker-hub
      external_fallback: false
    push:
      destination: docker-hub
```

Upstream authentication follows one built-in rule. Regstair attempts pulls anonymously. If the selected registry challenges the request, Regstair retries with the current user's saved credential when that user has one. A user does not need an account at every upstream registry to pull public content. Pushes require an authenticated Regstair user and use that user's saved credential.

Configured registries may define credential verification and advanced compatibility settings such as allowed token-service hosts. Authentication or authorization failure does not change the selected route or advance pull fallback.

## Compatibility

| Capability | Status |
| --- | --- |
| Docker login, pull, and push | Tested |
| OCI Distribution `registry:2` | Tested |
| Harbor private projects and robot credentials | Tested |
| Anonymous upstream pulls | Tested |
| Scoped Bearer authentication at the Regstair endpoint | Tested with real Docker login, pull, and push |
| Basic and scoped Bearer upstream challenges | Implemented and locally tested |
| Docker Hub anonymous pull and cache replay | Live tested |
| Docker Hub and GHCR per-user credentials | Configured; provider-account qualification remains environment-dependent |
| Multi-replica deployment | Not supported |
| OCI referrers and broader artifact types | Limited |

Regstair currently targets a single Docker-hosted instance for a small or medium enterprise network. It is not a replacement for Harbor project administration, scanning, replication, retention, or registry lifecycle management.

## Production Requirements

The repository Compose file is a usable homelab and evaluation deployment. Professional deployments should additionally:

- install the generated Regstair CA on clients, replace the generated identity with an organizational certificate, or terminate TLS at a trusted reverse proxy;
- pin the Regstair image by version or digest;
- mount an authoritative YAML configuration read-only;
- use persistent storage for the content root and SQLite database;
- generate and protect a separate 32-byte credential-encryption key;
- back up configuration, metadata, content, and required key material;
- add trusted edge rate limiting for internet-facing or multi-process deployments;
- restrict direct network access to upstream registries and fixture ports;
- test restore and administrator recovery before relying on the service.

The browser session cookie is `Secure`, `HttpOnly`, and `SameSite=Strict`. The CSRF cookie is `Secure` and `SameSite=Strict` but intentionally script-readable so the browser client can submit the synchronizer token. Regstair listens and publishes on standard ports `80` and `443`. The container remains non-root and receives only `NET_BIND_SERVICE` for its privileged listeners. The generated CA and private keys persist in the `regstair-tls` volume. On Linux, the Compose `tls-init` service runs once in the host network namespace before Regstair and automatically includes active non-loopback host addresses in the server certificate. Docker bridges and virtual Ethernet interfaces are excluded. `REGSTAIR_TLS_HOSTS` adds deployment DNS names or additional IP addresses; it is not required for ordinary host-address discovery. When the detected or configured identities change, Regstair preserves its local CA and server private key and atomically reissues the server certificate, so existing clients continue trusting the same CA. See [Deployment Modes](docs/DEPLOYMENT.md) for homelab, professional, and local evaluation setups.

Losing every key capable of decrypting stored upstream credentials permanently loses access to those credential values. Users must replace affected credentials; Regstair cannot recover them.

Read [Backup, Restore, and Credential-Key Lifecycle](docs/BACKUP_KEY_LIFECYCLE.md) before enabling stored upstream credentials.

### Administrator Recovery

Regstair prevents disabling or demoting the last enabled administrator. For host-level password recovery, stop the serving container, back up the database, and mount a temporary password file into the one-shot recovery container:

```bash
mkdir -p .runtime
printf '%s\n' 'CHOOSE_A_NEW_PASSWORD' > .runtime/admin-password
sudo chown 65532:65532 .runtime/admin-password
sudo chmod 400 .runtime/admin-password

docker compose stop regstair
docker compose run --rm --no-deps \
  -v "$PWD/.runtime/admin-password:/run/secrets/regstair-admin-password:ro" \
  regstair admin reset-password \
  -metadata-path /var/lib/regstair/content/metadata/regstair.db \
  -username YOUR_ADMIN_USERNAME \
  -password-file /run/secrets/regstair-admin-password

sudo rm -f .runtime/admin-password
docker compose up -d regstair
```

Recovery revokes that administrator's browser sessions and Docker tokens and records a redacted audit event.

## Documentation

| Job | Document |
| --- | --- |
| Evaluate the routing and cache scenarios | [Demo Guide](docs/DEMO.md) |
| Operate backup, restore, and key rotation | [Backup and Key Lifecycle](docs/BACKUP_KEY_LIFECYCLE.md) |
| Understand cache performance and capacity | [Cache Evaluation](docs/CACHE_EVALUATION.md) |
| Review security boundaries and abuse cases | [Threat Model](docs/THREAT_MODEL.md) |
| Review secret-leak qualification | [Secret-Leak Qualification](docs/SECRET_LEAK_QUALIFICATION.md) |
| Understand accepted architecture decisions | [ADR Index](docs/decisions/README.md) |
| Read the authoritative product requirements | [Next-Level Service PRD](docs/NEXT_LEVEL_SERVICE_PRD.md) |
| Review the current interface specification | [UI/UX Design](docs/UI_UX_DESIGN.md) |

The JSON control-plane API remains under `/admin/api/*`. It uses the same setup, session, role, CSRF, redaction, and immediate-invalidation security boundaries as the web application. Treat the API as pre-1.0 until an explicit compatibility policy is published.

## Development

Regstair requires Go 1.26 for local development.

Run the complete Go suite:

```bash
GOCACHE=/tmp/regstair-go-cache go test ./...
```

Build the production image:

```bash
docker build -t regstair:local .
```

Run the reproducible OCI gateway integration suite:

```bash
./scripts/compose-smoke.sh
```

Additional focused suites cover Docker CLI compatibility, per-user Harbor credentials, cache performance, backup/restore, and secret-leak qualification. See [scripts](scripts/) and the [Demo Guide](docs/DEMO.md).

GitHub Actions runs the Go and Compose integration gates on pushes and pull requests. A scheduled and manually dispatchable Harbor workflow deploys a real Harbor instance and verifies per-user credentials, private pull, and push through Regstair. Tags matching `v[0-9]*` run release verification, build five Linux architectures in parallel, and publish the versioned manifest to GHCR.

## Project

Regstair was designed and developed by James Dornan with coding assistance from OpenAI Codex. The project began as a test-first Build Week prototype and has since developed into the current authenticated OCI gateway and control plane.

Regstair is available under the [MIT License](LICENSE).

Copyright (c) 2026 James Dornan.
