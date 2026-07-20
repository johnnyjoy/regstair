# ADR-0001: Use Time-Limited Docker Access Tokens

Status: accepted
Date: 2026-07-19

## Context

The next-level service must associate a normal Docker pull or push with an enabled local Regstair user before selecting that user's upstream registry credential. Docker-facing authentication is therefore part of the local-account foundation, not a later integration step.

Reusing the interactive web password would expose the same long-lived secret to developer machines, CI environments, shell history risks, and Docker credential configuration. It would also prevent independent expiration and revocation.

## Decision

`docker login` uses the local Regstair username and a separately issued Regstair access token in the password field.

For this phase:

- tokens have an explicit expiration time;
- the raw token is generated with a cryptographically secure random source and shown only once;
- SQLite stores only a secure token hash, token identifier, user ID, creation time, expiration time, revocation time, and last-used time when implemented;
- token validation also checks that the owning user is enabled;
- revocation and user disablement prevent future authenticated OCI operations;
- token authority cannot exceed the owning user's current route authorization;
- web passwords are not accepted as Docker credentials;
- anonymous pulls remain available where global route policy permits them;
- token values and authorization headers are excluded from logs, APIs, audit records, request events, and support output.

This is not a general OAuth or token-exchange platform. The implementation exists only to support ordinary Docker Basic credential submission to Regstair with independently manageable CLI secrets.

## Alternatives

### Use the local web password

Rejected because it couples web and CLI compromise domains, cannot be independently revoked, and encourages long-lived password distribution to CI systems.

### Build a broader OAuth/OIDC token service

Deferred because it exceeds the local-user phase and is not required for normal `docker login` compatibility.

## Consequences

- Local-account work must include token issuance, listing metadata, revocation, validation, and expiration.
- The account UI needs a one-time token display and clear expiration/revocation behavior.
- CI systems can receive a token without receiving the user's web password.
- Token loss requires issuance of a replacement; Regstair cannot recover a raw token.
- Enterprise federation remains a future replacement or extension point.

## Implementation Plan

- Add token records and repository operations under `internal/metadata/` using existing SQLite migration patterns.
- Add generation, hashing, and validation services under `internal/auth/`.
- Replace or extend the current YAML-client authenticator at the gateway boundary without weakening anonymous-route behavior.
- Add account handlers and server-rendered token management after secure web sessions exist.
- Update `scripts/docker-client-smoke.sh` or add a next-level smoke script using a generated token.
- Reference ADR-0001 at the Docker authenticator entry point.

## Verification

- [ ] Normal `docker login` succeeds with an active token.
- [ ] The local web password fails as a Docker credential.
- [ ] Expired and revoked tokens fail.
- [ ] Disabling the user invalidates their tokens.
- [ ] Anonymous public pulls remain unchanged.
- [ ] Database and response inspection cannot recover a raw token.
- [ ] A token cannot grant more route authority than its owner.
- [ ] Existing Docker-client compatibility tests remain green during migration.

## Non-Goals

- OAuth authorization code or device flows.
- Refresh tokens or token exchange.
- Upstream credential passthrough.
- Enterprise OIDC or SAML.
- A general-purpose API token platform.

