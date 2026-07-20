# Regstair Auth Design

Status: phase 1 partially implemented

## Purpose

Regstair should let clients authenticate once to Regstair while Regstair handles upstream registry authentication according to policy.

The goal is not to forward arbitrary user credentials everywhere. The safer model is:

```text
Docker / CI authenticates to Regstair
             |
             v
Regstair authorizes the logical operation
             |
             v
Regstair uses configured upstream credentials for the selected source or destination
```

This keeps registry-specific credentials out of developer machines and CI jobs while preserving route-level control and auditability.

## Current Implementation

Already implemented:

- optional Basic client auth for the Regstair `/v2/` gateway;
- request-event `client_identity` recording for authenticated pull and push requests;
- route-level pull/push authorization by client identity;
- source auth config shape;
- top-level Basic credential config shape;
- environment-backed credential loading;
- HTTP connector Basic auth option;
- admin redaction for source auth details;
- rejection of deferred modes such as `client_passthrough` and `identity_mapped`.

Not implemented yet:

- upstream Bearer challenge handling for Docker Hub, GHCR, and similar registries;
- token caching;
- Harbor-specific auth smoke profile;
- passthrough auth.

## Design Principles

### Authenticate Clients To Regstair

Clients should authenticate to the Regstair endpoint, not to every upstream registry.

Regstair should then decide:

- who the client is;
- what logical repositories the client may pull or push;
- what route applies;
- which upstream credential, if any, should be used.

### Do Not Forward Client Credentials By Default

Regstair must not forward the client's `Authorization` header to upstream registries unless a future route/source explicitly enables that behavior.

Default behavior should be:

```text
client auth material stops at Regstair
```

This avoids accidental credential leakage to Docker Hub, GHCR, vendor registries, or internal registries.

### Use Proxy-Owned Upstream Credentials First

The first real auth mode should remain:

```yaml
auth:
  mode: proxy
  credential_ref: harbor-robot
```

In this mode, Regstair owns the upstream credential and uses it only for the configured source or destination.

### Audit Every Decision

Request events should eventually capture:

- authenticated client identity;
- operation;
- logical reference;
- matched route;
- upstream source or destination;
- auth mode used;
- credential reference used, never the secret;
- authorization result;
- cache result;
- explanation.

Credential reference names are acceptable in audit records if they are not secrets. Usernames, passwords, bearer tokens, and raw auth headers are not acceptable in admin/API responses.

## Auth Modes

### `none`

No upstream auth. Current default.

Use for:

- local `registry:2` smoke tests;
- intentionally open internal registries;
- unauthenticated development fixtures.

### `proxy`

Regstair uses a stored upstream credential.

Use for:

- Harbor robot accounts;
- internal registry service accounts;
- Docker Hub/GHCR machine credentials after Bearer challenge support exists.

This should be the first production-like auth mode.

### `client_passthrough`

Regstair forwards allowed client auth material upstream.

This is deferred because the Docker client authenticates to the registry host it contacts. If that host is Regstair, the token audience and scopes may not be accepted by the upstream registry.

Passthrough should require explicit source or route opt-in and strong redaction rules.

### `identity_mapped`

Regstair maps the authenticated client identity to an upstream credential or token.

This is useful long term, but it requires a mature identity and authorization model first.

## Proposed Client Authentication Phases

### Phase 1: Local Basic Auth For Regstair

Purpose: prove that clients authenticate to Regstair once.

Implementation status: done for Basic auth on `/v2/`; admin, health, and readiness endpoints remain unauthenticated.

Config sketch:

```yaml
clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
```

Behavior:

- Docker client logs into Regstair.
- Regstair validates Basic auth.
- Request events record `client_identity: ci-builder`.
- Authorization may initially be coarse: authenticated clients can use configured routes.

Pros:

- easy to test locally;
- works with Docker login;
- establishes the request identity pipeline.

Cons:

- not ideal as a long-term enterprise identity model;
- needs careful secret handling.

### Phase 2: Route-Level Authorization

Purpose: prevent authenticated clients from accessing every route.

Implementation status: done for explicit `allowed.pull` and `allowed.push` route names. Configured clients are denied by default when a routed operation is not listed.

Config sketch:

```yaml
clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
    allowed_routes:
      - curated-library
      - team-a-publish
```

Behavior:

- route match happens deterministically;
- authorization checks whether the client can use that route and operation;
- denied requests produce OCI `DENIED` responses and request events.

Potential refinement:

```yaml
allowed:
  pull:
    - curated-library
  push:
    - team-a-publish
```

### Phase 3: Proxy Credentials For Harbor (Implemented)

Purpose: prove upstream auth with a real registry platform.

Recommended first target:

- Harbor running in an optional Compose profile;
- one robot account;
- `auth.mode: proxy`;
- Basic auth to Harbor.

This should happen before Docker Hub/GHCR because Harbor robot credentials are easier to reason about and avoid external network dependencies.

### Phase 4: Bearer Challenge For Docker Hub And GHCR (Connector Implemented; External Compatibility Pending)

Purpose: support upstream registries that require token exchange.

Flow:

1. Regstair sends the upstream registry request.
2. Upstream returns `401 WWW-Authenticate: Bearer realm=...,service=...,scope=...`.
3. Regstair requests a token using the configured proxy credential.
4. Regstair retries the original request with the bearer token.
5. Regstair caches the token in memory until expiry.

Rules:

- tokens are never stored in SQLite;
- tokens are never exposed in admin responses;
- token cache keys include source, credential ref, service, and scope;
- token refresh happens before expiry when possible.

## Credential Storage

Near-term:

- config stores credential metadata only;
- secret values come from env vars or Docker secrets mounted as env;
- SQLite must not store secret values.

Later:

- support file-backed secrets;
- support external secret providers;
- support encrypted-at-rest credential storage only if there is a clear operator need.

## Authorization Model

Minimum viable authorization:

```text
authenticated client + operation + matched route -> allow/deny
```

Useful dimensions:

- operation: pull or push;
- route name;
- logical repository pattern;
- source or destination;
- tag vs digest reference;
- protected namespace flag.

The first implementation should avoid per-tag policy unless a real use case appears. Route-level authorization is enough to prove the model.

## Admin And Audit Redaction

Allowed in admin/API responses:

- auth mode;
- credential reference;
- whether a credential is configured;
- client identity;
- route authorization result;
- upstream source or destination id.

Never allowed in admin/API responses:

- passwords;
- bearer tokens;
- raw `Authorization` headers;
- Docker config JSON;
- secret environment variable values.

Environment variable names are not secret values, but they can reveal operational details. Admin source responses should continue avoiding top-level credential config dumps.

## Docker Client Behavior

For local Basic auth, Docker usage should look like:

```bash
docker login 127.0.0.1:8080
docker pull 127.0.0.1:8080/library/nginx:1.27
docker push 127.0.0.1:8080/team-a/service:4.1
```

The client only knows Regstair. It does not need credentials for Harbor, Docker Hub, GHCR, or vendor registries.

## Risks

### Credential Broker Risk

If Regstair can reach every upstream registry with broad credentials, a compromised Regstair client could become too powerful.

Mitigations:

- route-level authorization;
- least-privilege upstream credentials;
- separate credentials per source or destination;
- deny-by-default auth policy;
- audit every request;
- avoid wildcard client permissions.

### Token Scope Confusion

Docker Hub and GHCR token scopes are registry-specific. Regstair must request the narrowest scope needed for the physical repository and operation.

Mitigations:

- derive token scope after route rewrite;
- include operation in scope calculation;
- never reuse tokens across different physical repositories unless the upstream token scope explicitly allows it.

### Secret Leakage

Secrets may leak through logs, admin JSON, errors, or request events.

Mitigations:

- centralize redaction helpers;
- never log auth headers;
- avoid storing secret material in metadata;
- test admin/API responses for forbidden strings.

### Confusing Client Identity With Upstream Identity

The client identity and upstream credential identity are different. Audit records must preserve both without implying that the upstream registry saw the original client.

## Recommended Next Implementation Slice

After the current Basic client-auth and route-authorization slices:

1. Local client-auth and proxy Basic-auth fixtures prove the two identities independently.
2. The automated Harbor harness proves private-project robot authentication, pull, push, routing, and audit history.
3. Next, validate the Bearer implementation against Docker Hub and GHCR before claiming those registries as supported.

Do not start with Docker Hub or GHCR. Their Bearer challenge behavior is important, but Harbor/local Basic auth is the cleaner first proof.
