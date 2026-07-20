# Regstair Admin Control Plane

Status: approved design; read-only interactive slice is next

## Purpose

The Regstair admin surface should make routing, authentication, cache state, and provenance understandable and operable without becoming a replacement for Harbor or another upstream registry UI.

This document defines the durable boundary between observation, diagnosis, runtime operations, and configuration. It also defines the security gates that must be complete before the read-only MVP gains mutation controls.

## Product Boundary

Regstair owns controls for:

- logical pull and push routing;
- route authorization by Regstair client identity;
- logical-to-physical namespace rewriting;
- upstream source selection and credential status;
- cache state and invalidation;
- request history, resolution explanations, and provenance;
- Regstair configuration revisions and activation.

Regstair does not own controls for:

- Harbor projects, replication, scanning, retention, or users;
- upstream registry lifecycle administration;
- image inspection features already provided by a registry platform;
- arbitrary secret browsing or secret-value recovery;
- production routing decisions made by nondeterministic or AI systems.

The default admin experience should remain quiet, dense, and operational. It should optimize for repeated scanning, comparison, filtering, and investigation rather than marketing or dashboard decoration.

Help belongs in the admin document beside the concepts and controls it explains. Regstair must not require a separate help site for routine operation. Guidance should use concise field descriptions, semantic table captions, and collapsible page-level context so it remains available to new operators without slowing repeated expert workflows.

The admin surface targets WCAG 2.2 Level AA. It must support keyboard-only operation, visible focus, semantic landmarks and controls, programmatic labels and descriptions, text alternatives where applicable, status text that does not depend on color, 44px control targets, 200% text zoom, responsive reflow, reduced motion, forced colors, and user-selectable light, dark, and operating-system themes. Accessibility is a release criterion and must be rechecked as interactive controls are added.

## Current State

The current server-rendered admin UI and JSON API are read-only. They expose:

- health;
- configured sources and redacted auth status;
- routing rules;
- recent request events;
- provenance lookup;
- artifact and tag mappings;
- cache inventory.

The admin surface is not authenticated. This is acceptable only for local development and the current read-only demo. No mutating admin endpoint may be added until the security gate in this document is satisfied.

## Capability Phases

### Phase 1: Observe

Goal: make current behavior searchable and explainable without changing runtime state.

Controls:

- filter requests by client identity, route, operation, source or destination, status, error classification, and time;
- search by logical reference or digest;
- open a request detail view with the full deterministic explanation;
- distinguish client authentication, route authorization, upstream authentication, source availability, fallback blocking, and not-found outcomes;
- show source auth mode, credential reference, configured status, health, and last successful contact;
- show route precedence, source order, authoritative source, fallback policy, and namespace rewrite;
- search provenance, tag mappings, manifests, and blobs;
- copy references and digests;
- manually refresh and optionally enable bounded auto-refresh.

Phase 1 is read-only and may proceed before admin authentication is implemented.

### Phase 2: Diagnose

Goal: perform side-effect-free probes and policy simulations.

Controls:

- test source connectivity and upstream authentication without revealing credentials;
- simulate a pull or push for a client identity and logical reference;
- preview the matched route, authorization result, physical reference, candidate sources, and destination;
- resolve manifest metadata without caching blobs;
- determine whether an artifact can be served entirely from cache;
- expose readiness detail for SQLite, content storage, and configured sources.

Diagnostic probes may contact upstream systems and must be rate-limited and audited, but they must not modify routing, cache, registry content, or configuration.

### Phase 3: Operate

Goal: permit narrow, reversible runtime operations.

Candidate controls:

- invalidate a tag mapping;
- evict an unreferenced cache object;
- retry a failed resolution;
- enable maintenance mode for a source;
- reload an already validated configuration revision;
- revoke or temporarily disable a Regstair client;
- cancel an incomplete local upload;
- export filtered requests or provenance.

Phase 3 is blocked on admin authentication, authorization, CSRF protection, audit events, concurrency rules, and confirmation patterns.

### Phase 4: Configure

Goal: manage durable policy through versioned, atomic revisions.

Configuration flow:

```text
create draft
    -> validate schema and policy
    -> simulate affected references and clients
    -> review structured diff
    -> activate atomically
    -> monitor
    -> roll back to a known revision if needed
```

Candidate controls:

- create or edit routes;
- change precedence and fallback behavior;
- edit namespace rewrites;
- assign pull and push routes to clients;
- add source metadata, endpoints, and auth modes;
- change credential references without displaying secret values;
- activate or roll back a complete revision.

The UI must never partially update live policy. Activation succeeds for the entire validated revision or changes nothing.

## Roles

Admin identity is separate from OCI client identity. A client allowed to pull or push through `/v2/` receives no admin privileges from that fact alone.

| Role | Capabilities |
| --- | --- |
| Viewer | Read requests, routes, sources, auth status, provenance, and cache inventory |
| Operator | Viewer capabilities plus approved diagnostics, retries, maintenance mode, and safe cache invalidation |
| Administrator | Operator capabilities plus client authorization and configuration revision activation or rollback |

Permissions should be checked on the server for every endpoint. Hiding a control in the browser is not authorization.

## Security Gate For Mutations

Before the first mutating admin endpoint is merged, all of the following are required:

- admin authentication separate from OCI client Basic auth;
- server-side viewer, operator, and administrator authorization;
- short-lived sessions using `HttpOnly`, `SameSite`, and production-only `Secure` cookies;
- CSRF protection for browser-originated mutations;
- an audit record for every attempted administrative action, including denied attempts;
- secret redaction in HTML, JSON, errors, logs, and audit records;
- optimistic concurrency or revision preconditions for state changes;
- explicit confirmation for destructive or availability-affecting actions;
- a separately configurable admin listener or documented network restriction;
- tests proving `/v2/` credentials do not grant admin access.

Upstream passwords, bearer tokens, raw authorization headers, Docker config JSON, and secret environment values must never be returned by an admin endpoint or persisted in SQLite.

## Interaction Model

### Navigation

The primary views should be:

- Overview;
- Requests;
- Routes;
- Sources;
- Artifacts;
- Cache.

Provenance is reached through artifact/reference search and request details rather than requiring a separate top-level destination. A future Configuration view appears only when revision management exists.

### Tables And Filters

Operational data should use semantic tables with stable column widths, sortable headers where sorting is supported, and a compact filter bar. Filters must be represented in the URL query string so investigations can be refreshed and shared.

Use the control that matches the value:

- text input for reference, digest, and identity search;
- select menus for route, source, status, and error class;
- segmented control for pull, push, or all operations;
- checkbox or toggle for auto-refresh;
- icon buttons with accessible labels for copy, refresh, and close;
- text buttons only for explicit commands such as Simulate or Test connection.

Empty, loading, error, and stale-data states must preserve layout and explain the state without hiding active filters.

### Request Details

Selecting a request opens a responsive detail surface. Use a side drawer on wide screens and a full-screen dialog on narrow screens. It must support:

- complete keyboard operation;
- focus placement on open and focus restoration on close;
- Escape to close;
- semantic dialog labeling;
- a stable summary followed by routing, auth, cache, provenance, and explanation sections;
- copy controls for reference and digest values.

Status must never be communicated by color alone. Every state requires visible text and, where useful, an icon.

### Route Simulator

The simulator accepts:

- operation: pull or push;
- client identity;
- logical repository;
- tag or digest reference.

It returns:

- matched route and precedence;
- authorization result;
- rewritten physical reference;
- ordered pull candidates or push destination;
- authoritative source and fallback behavior;
- source auth mode and whether its credential is configured;
- deterministic explanation steps.

Simulation is read-only. It must call the same policy and authorization code used by live requests so UI and runtime decisions cannot drift.

## API Plan

Existing endpoints remain compatible:

- `GET /admin/api/health`
- `GET /admin/api/sources`
- `GET /admin/api/routes`
- `GET /admin/api/requests`
- `GET /admin/api/provenance`
- `GET /admin/api/artifacts`
- `GET /admin/api/cache`

Phase 1 extends requests with optional query parameters:

- `client_identity`;
- `route`;
- `operation`;
- `source`;
- `status`;
- `error_classification`;
- `reference`;
- `before` and `after` timestamps;
- `limit` and an opaque pagination cursor.

New read-only endpoints:

- `GET /admin/api/requests/{id}` for request detail;
- `GET /admin/api/search?q=...` for references and digests;
- `POST /admin/api/simulations/routes` for a side-effect-free route simulation.

The simulation uses `POST` because its structured input may grow, but it performs no mutation. Responses should include a versioned shape and machine-readable status or error codes.

Source health can initially be returned from `GET /admin/api/sources`; active connection tests belong to Phase 2 and require a separate audited endpoint.

## Accessibility And Responsive Requirements

WCAG 2.1 AA is the minimum acceptance target.

Required behavior:

- semantic landmarks, headings, forms, tables, buttons, and dialogs;
- a skip link to main content;
- all controls usable by keyboard alone;
- visible `:focus-visible` treatment;
- explicit labels and associated help or errors for inputs;
- at least 44 by 44 CSS pixel touch targets for primary interactive controls;
- sufficient text and non-text contrast;
- no information conveyed only through color;
- live updates announced without repeatedly interrupting screen-reader users;
- reduced-motion preferences respected;
- responsive behavior from 320 CSS pixels through wide desktop layouts;
- no horizontal page overflow; wide tables may use a labeled internal scroll region or responsive row presentation.

The UI should use shared tokens for color, spacing, typography, borders, focus, and status states. Components should be reused across views rather than restyled per page.

## Performance And Delivery Constraints

The current server-rendered Go implementation remains the default architecture for the first interactive slice. Add minimal progressive JavaScript for filtering, detail dialogs, copying, and refresh behavior. A frontend framework is not justified until state or component complexity demonstrates a concrete need.

Requirements:

- the initial page remains useful without JavaScript;
- filters submit as normal URL-backed forms;
- enhanced interactions do not duplicate policy logic in JavaScript;
- request lists use bounded pagination rather than loading all history;
- auto-refresh pauses when the page is hidden and never refreshes faster than a configured minimum;
- no third-party analytics or external asset dependency is introduced.

## Cache Safety Rules

Future cache mutation must respect content-addressed sharing:

- invalidating a tag mapping is distinct from deleting manifest or blob content;
- deleting content requires a reference check across all tag mappings and manifests;
- referenced blobs cannot be deleted by the normal operation path;
- force deletion is an administrator-only future capability with impact preview and explicit confirmation;
- every invalidation or deletion records actor, target, reason, result, and affected references;
- interrupted operations must leave metadata and content in a recoverable state.

## Configuration Concurrency

Every configuration revision receives an immutable identifier and content digest. Draft validation and activation operate against that identifier.

Activation requires the caller's expected active revision. If another administrator activates a different revision first, the stale activation fails with a conflict and requires a new diff review.

The active configuration, its source, validation result, activation actor, and activation time are auditable. Secret values are never part of a displayed diff.

## First Implementation Slice

The first interactive release remains read-only and is delivered test-first in this order:

1. Add repository/API filtering and pagination for request history.
2. Add request IDs and a request-detail API.
3. Add source health snapshots and clearer auth-status fields.
4. Add the route-simulation service and API using the runtime policy and authorizer.
5. Add URL-backed filters, status presentation, and responsive request details to the server-rendered UI.
6. Add artifact and provenance search.
7. Add focused unit, handler, accessibility, responsive, and Compose smoke coverage.

The slice does not add source tests, cache invalidation, config reload, client changes, credential changes, or any other mutation.

## First-Slice Acceptance Criteria

- A user can find a request by reference, digest, identity, route, status, source, operation, or error class.
- Active filters are visible, survive refresh, and can be cleared individually or together.
- Request details show client identity, route decision, source or destination, cache result, error classification, explanation, and provenance where available.
- `authorization_denied` and `upstream_authentication_failed` are visibly distinct.
- Source rows show `none` or `proxy`, credential reference, configured state, health state, and observation time without exposing secrets.
- Route simulation matches live policy tests for pull, push, fallback, rewrite, and authorization outcomes.
- Artifact/reference search links to provenance and relevant request history.
- All controls and the detail surface work by keyboard and retain visible focus.
- The UI remains coherent at 320, 768, 1024, and 1440 CSS pixel widths.
- Existing admin API consumers and all Compose smoke scripts remain compatible.
- No endpoint in the slice changes configuration, cache state, credentials, clients, or upstream content.

## Test Strategy

### Unit And Repository Tests

- request filter combinations and pagination boundaries;
- stable ordering and cursor behavior;
- route simulation parity with policy and authorization decisions;
- source health snapshot state transitions;
- secret redaction helpers.

### Handler Tests

- query parsing and validation;
- request-detail not-found behavior;
- simulation response shapes and error codes;
- HTML rendering for empty, loading-independent, success, denied, and failed states;
- rejection of unsupported methods;
- absence of credential values, tokens, and authorization headers in responses.

### Browser Tests

- keyboard navigation and focus restoration;
- filter URL persistence and clear behavior;
- responsive detail drawer/dialog behavior;
- table overflow and text containment at target widths;
- status communication independent of color;
- auto-refresh pause and resume behavior if auto-refresh ships in the slice.

### Docker Smoke Coverage

- authenticated Harbor pull appears with the expected client and source;
- denied route authorization is filterable and inspectable;
- invalid upstream credentials appear as `upstream_authentication_failed`;
- pushed Harbor artifact is searchable and linked to provenance;
- admin responses never contain configured client or upstream secret values.

## Decision Log

- Keep the first interactive slice server-rendered with progressive enhancement.
- Keep Phase 1 read-only while the admin surface remains unauthenticated.
- Require a separate admin identity boundary before mutations.
- Reuse runtime policy and authorization services for simulation.
- Treat configuration as atomic, versioned revisions rather than direct field mutations.
- Treat tag invalidation and content deletion as separate cache operations.
- Use WCAG 2.1 AA, keyboard operation, responsive behavior, and secret redaction as release criteria rather than later polish.
