# ADR-0006: Bootstrap and Recover Administrators with a Local CLI

Status: superseded by ADR-0008
Date: 2026-07-19

## Context

Admin-only user creation cannot create the first administrator. Default credentials, environment-supplied long-lived passwords, and passwords printed to logs are unsafe for a network service.

## Decision

This decision originally selected one-shot local commands against the configured SQLite database:

```text
regstair admin bootstrap
regstair admin reset-password --username <name>
```

`bootstrap` succeeds only when no user exists and transactionally creates the first enabled admin. It reads and confirms the password from an attached terminal without echo; a non-interactive password may be supplied only through a root-readable mounted file descriptor/file option intended for deployment automation, never a command argument or normal environment variable.

`reset-password` requires local host/container and database access, records actor `system:local-recovery`, revokes the user's web sessions and Docker tokens, and updates the password hash transactionally. Operators must stop the serving process or use an application-mediated exclusive maintenance path; concurrent direct database mutation is not supported.

## Consequences

- Docker operators bootstrap with `docker compose exec` or a one-shot container sharing the data volume and secret inputs.
- Possession of database-volume and container execution access is an administrative trust boundary.
- There is no default administrator and no web-exposed bootstrap endpoint.

## Implementation Plan

- Add subcommands under `cmd/regstair/` using shared config, repository, password, and audit services.
- Add secure terminal input with a narrow dependency or supported terminal package.
- Document Compose bootstrap, backup-before-recovery, and exclusive-access requirements.
- Reference ADR-0006 at command entry points.

## Verification

- [ ] Bootstrap works only on an empty user store.
- [ ] Passwords do not appear in arguments, environment guidance, stdout, or logs.
- [ ] Concurrent/double bootstrap creates exactly one administrator.
- [ ] Reset revokes sessions and Docker tokens in the same transaction.
- [ ] Recovery emits a redacted audit event.
