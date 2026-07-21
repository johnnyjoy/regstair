# ADR-0002: Keep Routes and Registries File-Owned

Status: superseded in part by ADR-0009 for route ownership; registry/source ownership remains accepted
Date: 2026-07-19

## Context

Current Regstair routes and sources are loaded from YAML. The submitted next-level PRD introduced mutable database registry records without defining whether YAML or SQLite controlled runtime configuration. Two mutable authorities could disagree about hosts, capabilities, or credential use and create unsafe routing behavior.

The current phase needs configured registries for per-user credentials, but it does not need a complete registry configuration revision, activation, and rollback system.

## Decision

For this phase, YAML remains authoritative for both global routes and registry/source definitions.

Every enabled configured source is an allowed registry credential target. The source configuration has file-owned fields that specify:

- whether those credentials may be used for private pulls;
- whether those credentials may be used for pushes;
- the verification repository or namespace;
- provider-specific display guidance when needed.

SQLite stores local users and encrypted per-user credential records keyed to stable configured source IDs. It does not store an independently mutable registry host or route definition.

The UI and APIs expose configured registry definitions read-only in this phase. Users cannot add arbitrary registry hosts. Administrators edit the deployment configuration and restart or use a future validated configuration-revision mechanism. There is no independent approval boolean that can contradict enabled configuration.

## Alternatives

### YAML sources with a mutable database overlay

Rejected because it splits one registry across two authorities and makes effective configuration difficult to reason about.

### Database-owned registries with YAML routes

Deferred because safe implementation requires import, runtime connector replacement, referential integrity, backup/restore, and rollback semantics beyond the immediate phase.

### Database-owned registries and routes

Deferred to a future versioned configuration-revision system. Direct field mutation is not an acceptable route-management model.

## Consequences

- The phase avoids a configuration migration and runtime connector hot-swap system.
- Registry administration is read-only in the initial UI despite the submitted PRD's broader CRUD language.
- Deployment operators continue to use YAML for registry and route changes.
- Per-user credentials remain durable in SQLite and can be validated against configured source IDs at startup and use time.
- Future mutable configuration must supersede this ADR with an atomic revision and rollback design.

## Implementation Plan

- Extend `internal/config/` source schema and validation with user-credential policy and verification fields.
- Project all enabled configured sources through account and admin read APIs.
- Add SQLite foreign-reference validation at the repository/service boundary using stable source IDs; do not create registry CRUD tables or endpoints.
- Keep connector construction in `internal/app/` based on validated file configuration.
- Render registry configuration read-only in user/admin UI.
- Reference ADR-0002 near source-policy validation and configured-registry projection.

## Verification

- [x] All configured, enabled sources appear to users without a separate approval gate.
- [ ] Invalid capability combinations and verification settings fail configuration validation.
- [x] Per-user credential operations reject unknown or disabled source IDs.
- [x] Restart reproduces the same configured registry projection from YAML.
- [ ] No registry CRUD table or mutating endpoint is introduced in this phase.
- [ ] Anonymous pull, per-user Harbor credential, and Docker smoke paths remain valid.

## Non-Goals

- Registry or route mutation through the UI.
- YAML-to-database import.
- Runtime connector hot swapping.
- Per-user registry hosts.
- Configuration revisions or activation workflows.
