# Runtime Route Management Implementation Plan

Status: paused until the React UI migration and browser qualification are complete
Authority: [ADR-0009](decisions/0009-use-versioned-database-owned-runtime-route-sets.md)
Last updated: 2026-07-21

## Gate 0: Ownership Communication

- [x] Explain current file ownership directly on the Routes page.
- [x] Record database-owned route-set authority in ADR-0009.
- [x] Supersede ADR-0002 for routes without changing registry/source ownership.
- [x] Record API, storage, concurrency, audit, import/export, and rollback contracts.

## Gate 1: Versioned Storage

- [ ] Write repository contract tests for initial import, draft creation, immutable history, and active lookup.
- [ ] Add canonical route-set domain records and closed states.
- [ ] Add SQLite migration, partial unique active index, and memory-repository parity.
- [ ] Add optimistic-concurrency and transactional activation tests.
- [ ] Commit activation and redacted audit event atomically.

## Gate 2: Runtime Snapshots

- [ ] Add an atomic immutable policy snapshot provider.
- [ ] Acquire one snapshot at the start of each pull or push operation.
- [ ] Import YAML revision 1 only when route-set storage is empty.
- [ ] Load the active database revision on subsequent startups.
- [ ] Prove pre-activation requests retain their original snapshot.

## Gate 3: Validation and Simulation

- [ ] Validate complete drafts against configured source IDs and policy ambiguity rules.
- [ ] Report stable validation classifications and route-shadow findings.
- [ ] Simulate pull and push decisions without contacting an upstream.
- [ ] Prove simulation and gateway policy decisions are equivalent.

## Gate 4: Administrator API

- [ ] List revision metadata and retrieve one complete route set.
- [ ] Create drafts with expected-active-revision concurrency.
- [ ] Validate, simulate, activate, and roll back.
- [ ] Import YAML to a draft and export complete YAML.
- [ ] Enforce administrator role, CSRF, safe errors, and audit coverage.

## Gate 5: Route Editor

- [ ] Build add, edit, delete, and reorder workflows against a draft.
- [ ] Add match, precedence, source, fallback, push, and rewrite validation.
- [ ] Add live reference simulation and conflict/shadow findings.
- [ ] Add explicit activation confirmation, revision history, and rollback.
- [ ] Remove the temporary file-managed notice after database activation is live.

## Gate 6: Qualification

- [ ] Pass Go, frontend, Docker, pull, push, anonymous-pull, and rollback smoke tests.
- [ ] Verify backup/restore with active and historical revisions.
- [ ] Verify YAML semantic round-trip and lossless source references.
- [ ] Verify no route body, finding, audit event, or error contains a secret.
- [ ] Document operator migration and GitOps import/export workflows.
