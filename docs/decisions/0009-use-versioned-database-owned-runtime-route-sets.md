# ADR-0009: Use Versioned Database-Owned Runtime Route Sets

Status: accepted
Date: 2026-07-21
Supersedes: [ADR-0002](0002-keep-routes-and-registries-file-owned.md) for route ownership

## Context

Regstair's primary product function is customizable registry routing, but ADR-0002 deliberately kept routes and registries YAML-owned while authentication, credentials, caching, and Docker compatibility were established. The resulting UI presents routes as first-class operational resources while preventing administrators from creating or changing them. Editing mounted files and restarting a container is not an acceptable normal route-management workflow.

Directly editing YAML from the service is also unacceptable: mounts may be read-only, writes would not be portable across deployments, runtime state could diverge from the file, and safe validation, activation, revision history, and rollback would remain undefined.

## Decision

SQLite owns versioned runtime route sets. Exactly one complete route-set revision is active. Administrators create and validate drafts, simulate references against a draft, then atomically activate a complete validated revision. New requests obtain the active immutable policy snapshot when resolution begins; an in-flight request continues with the snapshot it started with.

Activation and its redacted audit event commit in one SQLite transaction. The in-memory snapshot changes only after that transaction commits. If snapshot publication fails, the service reports degraded readiness and reloads the committed active revision rather than silently continuing with a different policy.

YAML remains a bootstrap, import, and export format:

- when no route-set revision exists, validated YAML routes are imported as revision 1 and activated;
- after bootstrap, YAML routes are not a competing runtime authority;
- explicit import creates a new draft and never activates implicitly;
- export emits a complete valid YAML route document suitable for backup and GitOps review.

Registry/source ownership remains file-owned during this slice. Route revisions refer to stable configured source IDs and cannot introduce registry hosts. A later ADR may move source ownership after connector hot-swap and credential-reference semantics are designed.

## API Contract

- `GET /admin/api/route-sets` lists revision metadata without route bodies by default.
- `POST /admin/api/route-sets` creates a draft from a complete route list and an expected active revision.
- `GET /admin/api/route-sets/{id}` returns one complete revision.
- `POST /admin/api/route-sets/{id}/validate` returns closed validation classifications and shadow/conflict findings.
- `POST /admin/api/route-sets/{id}/simulate` accepts operation, repository, and reference and returns the same policy decision model used by the gateway without contacting an upstream.
- `POST /admin/api/route-sets/{id}/activate` atomically activates a validated revision using optimistic concurrency.
- `POST /admin/api/route-sets/{id}/rollback` creates and activates a new revision copied from the selected historical revision; history is append-only.
- `POST /admin/api/route-sets/import` creates a draft from YAML.
- `GET /admin/api/route-sets/{id}/export` returns YAML.

All mutations require an authenticated administrator and CSRF protection. Route bodies, validation failures, activations, and rollbacks must not contain credentials or registry authorization material.

## Storage Model

`route_sets` stores `id`, monotonically increasing `revision`, `state` (`draft`, `active`, or `superseded`), canonical route JSON, creator, creation time, validator result and time, activation actor and time, and the base active revision used for optimistic concurrency. A partial unique index enforces at most one active row. Route lists are stored as one canonical document so activation cannot expose a partially updated policy.

Historical rows are immutable except for draft validation metadata and the state transition performed during activation. Rollback never reactivates or edits an old row.

## Alternatives

### Continue file ownership

Rejected as the default because normal administrators cannot safely manage the product's central resource through Regstair.

### Mutate YAML from the UI

Rejected because filesystem ownership, container mounts, crash consistency, runtime reload, and configuration authority are deployment-dependent.

### Store each route as an independently active row

Rejected because multi-route edits could become partially visible and precedence validation would not have an atomic configuration boundary.

### Keep YAML routes with a database overlay

Rejected because effective routing would have two authorities and would be difficult to inspect, export, restore, and audit.

## Consequences

- Route changes no longer require a restart.
- Administrators receive drafts, validation, simulation, activation, history, and rollback.
- SQLite backup becomes necessary for active routing recovery; YAML export remains the portable escape hatch.
- Startup must import YAML only when route storage is empty and must log the selected authority and active revision.
- Source deletion or mutation remains impossible while referenced by active or retained route revisions.
- The UI must identify file ownership only during migration and remove that notice after database activation is available.

## Implementation Plan

1. Add route-set domain records and repository methods under `internal/metadata/`, with SQLite migration and memory-repository parity.
2. Add `internal/routingconfig/` service logic for canonicalization, validation, drafts, simulation, activation, rollback, YAML import/export, and transactional audit.
3. Introduce an atomic policy snapshot provider under `internal/policy/`; update pull and push resolvers to acquire one immutable engine per operation.
4. Import validated startup YAML when no route set exists, then construct runtime policy from the active database revision in `internal/app/`.
5. Add administrator APIs under `internal/admin/` with CSRF, role checks, optimistic concurrency, closed errors, and no secret-bearing fields.
6. Replace the temporary read-only Routes notice with draft editor, validation findings, simulation, activation confirmation, revision history, and rollback UI.
7. Add YAML import/export commands and document backup/restore implications.

## Verification

- [ ] First startup imports validated YAML exactly once and activates revision 1.
- [ ] Restart ignores later YAML route changes when an active database revision exists.
- [ ] Invalid, ambiguous, or unknown-source drafts cannot activate.
- [ ] Simulation and live gateway resolution return equivalent route decisions.
- [ ] Concurrent activation with a stale expected revision fails without changing active policy.
- [ ] Activation commits revision state and audit event atomically.
- [ ] Requests started before activation complete with their original snapshot; later requests use the new snapshot.
- [ ] Rollback appends and activates a new revision without mutating history.
- [ ] Backup/restore preserves active revision and history; YAML export/import round-trips route semantics.
- [ ] Non-admin and missing-CSRF mutations are rejected.
- [ ] Docker pull and push smoke tests pass before, during, and after activation.

## Non-Goals

- Mutable registry/source hosts or connector hot swapping in this slice.
- Per-request route selection from a draft.
- Collaborative field-level editing or merging of concurrent drafts.
- Automatically activating imported YAML.
- A general-purpose configuration platform unrelated to routing.
