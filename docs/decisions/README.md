# Architecture Decisions

Accepted decisions govern implementation. Proposed decisions are not implementation authority.

| ADR | Status | Decision |
| --- | --- | --- |
| [ADR-0001](0001-use-time-limited-docker-access-tokens.md) | Accepted | Use time-limited Regstair access tokens for `docker login` |
| [ADR-0002](0002-keep-routes-and-registries-file-owned.md) | Superseded in part | Keep registry sources file-owned; route ownership moved to ADR-0009 |
| [ADR-0003](0003-hash-local-passwords-with-argon2id.md) | Accepted | Hash local passwords with parameterized Argon2id |
| [ADR-0004](0004-use-opaque-server-side-web-sessions.md) | Accepted | Use opaque server-side sessions and synchronizer CSRF tokens |
| [ADR-0005](0005-encrypt-user-registry-secrets-with-aes-gcm.md) | Accepted | Encrypt user registry secrets with versioned AES-256-GCM envelopes |
| [ADR-0006](0006-bootstrap-and-recover-admins-with-local-cli.md) | Superseded in part | Retain host-only administrator recovery; bootstrap moved to ADR-0008 |
| [ADR-0007](0007-write-transactional-append-only-audit-events.md) | Accepted | Write redacted append-only audit events with protected mutations |
| [ADR-0008](0008-use-web-first-run-setup-and-headless-api.md) | Accepted | Use web first-run setup and a headless API |
| [ADR-0009](0009-use-versioned-database-owned-runtime-route-sets.md) | Accepted | Use versioned database-owned runtime route sets with atomic activation |
| [ADR-0010](0010-bind-cached-objects-to-logical-repository-authorization.md) | Accepted | Bind cached manifests and blobs to logical repository authorization |
