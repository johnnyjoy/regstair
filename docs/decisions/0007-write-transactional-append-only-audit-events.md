# ADR-0007: Write Transactional Append-Only Audit Events

Status: accepted
Date: 2026-07-19

## Context

User, session, token, and credential mutations require durable attribution. Request events describe OCI traffic but are not an administrative audit log and do not guarantee transaction coupling to state changes.

## Decision

Create a separate append-only audit-event table. Successful protected mutations write their audit event in the same SQLite transaction as the state change. Failed and denied attempts write a standalone event when the database is available.

Each event records an ID, UTC timestamp, actor user ID or system actor, actor role, action, target type and ID, outcome, request/correlation ID, remote address where available, and a small versioned redacted details object. It never records passwords, tokens, authorization headers, encryption material, ciphertext, session tokens, CSRF tokens, or submitted secret fields.

Application APIs expose bounded, filtered reads to admins. The application does not update audit rows. Deletion/retention is not implemented in this phase; backup and external retention are operational responsibilities until a separately designed retention policy exists.

## Consequences

- Mutation services, not handlers, own transaction and audit coupling.
- Failed-audit-write behavior for successful mutations is fail-closed because they share a transaction.
- Audit storage growth must be observed and bounded pagination is mandatory.

## Implementation Plan

- Add audit schema, typed event model, transactional writer, and bounded query API under `internal/metadata/`.
- Pass actor and correlation context into mutation services.
- Centralize allowlisted detail construction and redaction tests.
- Add admin read API/UI after authentication and role enforcement.
- Reference ADR-0007 at the mutation transaction helper.

## Verification

- [ ] A successful mutation and audit event commit or roll back together.
- [ ] Denied and failed attempts produce redacted events when storage is available.
- [ ] Audit rows cannot be changed through application repositories.
- [ ] Secret-fixture scans cover details JSON, APIs, logs, and database text.
- [ ] Non-admin users cannot read audit events.
- [ ] Pagination is stable and bounded.

