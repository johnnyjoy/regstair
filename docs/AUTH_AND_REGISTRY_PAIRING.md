# Auth and Registry Pairing

## Current Direction

Regstair supports optional Basic client auth for the `/v2/` gateway and should continue maturing authentication before live upstream auth-provider integration becomes part of the default smoke test.

The near-term goal is to make auth explicit in:

- configuration schema;
- source and destination connector setup;
- request context;
- admin API redaction;
- request-event error classification.
- client identity in request events.

The baseline Docker Compose smoke test remains unauthenticated for now. That keeps the main Build Week path deterministic while route-level authorization and upstream registry auth are still being shaped.

## Auth Modes

Regstair should eventually support these source and destination auth modes:

- `none`: no upstream authentication.
- `proxy`: Regstair uses a stored upstream credential.
- `client_passthrough`: Regstair forwards explicitly allowed client auth material upstream.
- `identity_mapped`: Regstair maps the authenticated client identity to an upstream credential or token.

The first implementation target should be `none` and `proxy`.

`client_passthrough` and `identity_mapped` should be deferred until authorization and token redaction rules are more mature.

## Proxy Credentials

Proxy-stored credentials are the preferred first auth implementation.

Example shape:

```yaml
credentials:
  - id: harbor-robot
    type: basic
    username_env: REGSTAIR_HARBOR_USERNAME
    password_env: REGSTAIR_HARBOR_PASSWORD

sources:
  - id: harbor-team-a
    name: Harbor Team A
    endpoint: http://harbor:8080
    type: internal
    enabled: true
    auth:
      mode: proxy
      credential_ref: harbor-robot
```

Secrets should come from environment variables or Docker secrets, not literal config values.

Admin responses may expose:

```json
{
  "mode": "proxy",
  "credential_ref": "harbor-robot",
  "configured": true
}
```

Admin responses must not expose usernames, passwords, tokens, raw `Authorization` headers, or secret environment variable values.

## Connector Behavior

For simple internal registries and Harbor robot credentials, the first connector behavior can be Basic auth:

1. Source config selects `auth.mode: proxy`.
2. Source config references a credential.
3. Credential provider resolves secret values at startup.
4. HTTP connector attaches upstream auth on registry requests.

Docker Hub and GHCR need Bearer challenge support after Basic auth:

1. Registry responds `401 WWW-Authenticate: Bearer ...`.
2. Regstair parses `realm`, `service`, and `scope`.
3. Regstair requests a token using the configured proxy credential.
4. Regstair retries the original registry request with `Authorization: Bearer ...`.
5. Tokens are cached in memory with TTL.

Live tests for Docker Hub, GHCR, and Harbor auth can be added after the unauthenticated connector path remains stable.

## Client Passthrough

Client passthrough should be explicit per source or destination.

Regstair must never forward client auth material to an upstream registry unless the route/source configuration allows it.

Passthrough is more complex than proxy credentials because Docker clients normally authenticate to the registry host they are contacting. When that host is Regstair, the upstream registry may not accept the same token audience or scope.

For that reason, passthrough is not part of the first MVP implementation.

## Harbor Pairing

Harbor is a strong fit for Regstair.

Recommended demo topology:

```text
Docker / CI
   |
   v
Regstair
   |
   +--> Harbor: authoritative internal namespaces and push destinations
   +--> Docker Hub or registry:2: approved external fallback
   +--> GHCR: GitHub-hosted or vendor images
```

The default Compose smoke test should continue to use local `registry:2` containers for speed and reproducibility.

A later Harbor profile should be additive:

```bash
docker compose up
docker compose --profile harbor up
```

The Harbor profile should prove:

- internal authoritative pulls from Harbor;
- protected namespace fallback blocking when Harbor misses;
- push routing into a Harbor project;
- robot-account credential use;
- admin API redaction of Harbor credential status.

## Testing Plan

Completed unit coverage:

- config accepts `credentials` and source `auth`;
- config rejects missing credential references for `proxy`;
- credential provider loads env-backed secrets;
- admin API redacts all secret values;
- HTTP connector attaches Basic auth when configured;
- config accepts Basic Regstair clients;
- Basic Regstair client auth validates env-backed secrets;
- gateway returns a Basic challenge when clients are configured;
- pull and push request events record `client_identity`;
- route authorization denies unlisted pull and push routes.

Integration tests to add before live upstream auth:

- local authenticated `/v2/` smoke with one allowed pull and one denied route;
- local authenticated push smoke with one allowed destination and one denied route;
- auth failures classified separately from not found and unavailable in the end-to-end admin request log.

Integration tests to defer:

- Harbor robot-account pull and push;
- Docker Hub Bearer challenge flow;
- GHCR Bearer challenge flow;
- client passthrough behavior.
