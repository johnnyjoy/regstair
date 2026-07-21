# Regstair UI Product Polish Plan

Status: active
Owner: Regstair project
Last updated: 2026-07-21

## Objective

Turn the working Regstair control plane into a visually distinctive, screenshot-ready product without weakening its routing, caching, authentication, security, or accessibility behavior.

The target combines Proxmox's structural confidence, Pi-hole's operational immediacy, Sentry's investigation workflow, and the interaction polish expected from current first-class web applications.

## Architecture Decision

The authenticated control plane will migrate incrementally to:

- React and TypeScript;
- Vite for deterministic production builds;
- Material UI as the accessible component foundation;
- TanStack Query for API state;
- React Router for application navigation;
- Vitest, Testing Library, and user-event for component and interaction tests;
- Playwright for browser, responsive, accessibility, and screenshot qualification.

The Go service remains authoritative for authentication, authorization, APIs, OCI traffic, and static asset delivery. The production frontend is compiled to static assets, embedded in the Go binary, and shipped in the existing single container.

The migration is now a completion program, not a compatibility program. No authenticated navigation item may cross into the old templates. Setup and sign-in are the final public templates to replace because their security behavior must remain independently qualified. The old authenticated dashboard template and script are removed as soon as Cache, Account, Users, and Audit are complete.

### Pretext Decision

Pretext is not part of the foundation. It solves DOM-free text measurement rather than general visual polish. It may be introduced only if measured evidence shows that variable-height virtualization or route-flow label layout cannot meet performance and stability requirements with normal browser layout. This keeps the dependency purposeful and testable.

## Product Principles

1. The first viewport explains Regstair through real operational state.
2. Routing, caching, authentication, and failures are visible and explainable.
3. Summary comes before technical evidence; diagnostics remain available on demand.
4. The interface uses registry-domain language rather than implementation terminology.
5. Visual quality, light and dark themes, responsive behavior, and WCAG 2.2 AA are release requirements.
6. Controls remain efficient for experts while visible guidance supports new operators.
7. No secret appears in UI history, URLs, screenshots, logs, or client-side persisted state.
8. Empty, loading, degraded, denied, and failed states are designed as distinct states.
9. Working behavior is preserved throughout migration.

## Testing Strategy

Frontend work follows TDD where behavior can be expressed before implementation.

### Unit and Component Tests

- theme resolution and persistence;
- navigation and role-based visibility;
- API response parsing and error classification;
- loading, empty, success, degraded, and failure states;
- filters, sorting, pagination, and URL synchronization;
- dialogs, focus restoration, confirmation, and one-time-secret handling;
- responsive component decisions that do not require a browser layout engine.

Tests query by role, label, and accessible name. Snapshot tests are not used as the primary assertion mechanism.

### Go Contract Tests

- authenticated application routes serve the correct shell;
- API authorization remains server-enforced;
- static assets are content-typed, cacheable, and versioned;
- the CSP permits only the assets required by the application;
- no frontend route bypasses bootstrap, session, or role enforcement.

### Browser Tests

- critical workflows with a seeded backend;
- keyboard-only operation and focus management;
- light, dark, and system theme behavior;
- desktop, laptop, tablet, and mobile viewports;
- automated accessibility checks plus manual screen-reader review;
- approved visual screenshots and regression comparisons.

## Migration Slices

### Slice 1: Foundation and Shell

Status: complete

- [x] Create the React/TypeScript/Vite workspace.
- [x] Add Vitest and Testing Library configuration.
- [x] Define Regstair design tokens and MUI theme augmentation.
- [x] Test and implement system/light/dark theme selection.
- [x] Test and implement the responsive application shell.
- [x] Test role-aware primary navigation.
- [x] Add API client and query-provider boundaries.
- [x] Build frontend assets deterministically in Docker and CI.
- [x] Embed and serve production assets from Go.
- [x] Preserve all existing routes until a migrated page is complete.

Acceptance gate:

- component tests pass;
- Go tests pass;
- production bundle builds without network access after dependency installation;
- no existing UI route changes behavior;
- shell is keyboard-operable at 360 px and 1440 px;
- light and dark palettes meet contrast targets.

### Slice 2: Operational Overview

Status: in progress

- [ ] Add a typed overview API contract if existing endpoints cannot provide one coherent snapshot.
- [x] Implement the first traffic, cache, registry health, recent failures, and recent activity view.
- [x] Add a real client-to-Regstair-to-registry traffic visualization.
- [x] Design useful zero-traffic, degraded, and partial-data states.
- [x] Switch `/` to the React overview after component and delivery qualification.

Acceptance gate: a static screenshot explains what Regstair does and an operator can identify current health and the next required action within ten seconds.

### Slice 3: Request Investigation

Status: in progress

- [x] Implement URL-backed search, filters, sorting, and cursor pagination.
- [x] Add a responsive request list and focused detail view.
- [x] Visualize route, cache, credential, upstream, and outcome decisions.
- [x] Keep technical evidence expandable and secret-safe.
- [ ] Preserve page, selection, and scroll context when returning from detail.

Acceptance gate: a failed request can be explained and acted upon without consulting logs.

### Slice 4: Routes and Registries

Status: complete

- [x] Implement route list, ordered source path, fallback, destination, and rewrite preview.
- [ ] Add reference testing against the active routing policy.
- [x] Implement registry health, route usage, and credential capability.
- [x] Present file-owned configuration honestly as temporarily read-only.

### Slice 5: Cache Experience

Status: complete

- [x] Implement physical storage, logical mapping, blob, and deduplication summaries.
- [x] Add artifact search, freshness, source, route, and tag-to-digest evidence.
- [x] Do not display retention or eviction controls until the backend supports them.

### Slice 6: Identity and Security

Status: complete

- [x] Migrate account, password, Docker token, and registry credential workflows.
- [x] Migrate user and audit administration.
- [x] Preserve one-time-secret, CSRF, immediate invalidation, and last-administrator protections.
- [x] Replace setup and sign-in after their React workflows independently pass security contract tests.

### UI Completion Gate

Status: active; blocks Runtime Route Management

- [x] Migrate Cache without inventing unsupported retention or eviction controls.
- [x] Migrate Account with password, Docker token, and registry credential workflows intact.
- [x] Migrate Users with creation, role/access changes, enablement, password reset, last-administrator protection, and audit behavior intact.
- [x] Add a safe Audit API and migrate filtering and correlation views.
- [x] Route every authenticated navigation item through React without a full-document style change.
- [ ] Remove the old authenticated dashboard template, CSS, JavaScript, and rendering code.
- [x] Migrate Login and Setup with rate-limit, bootstrap-token, session, CSRF, and error behavior intact.
- [ ] Run real-Chrome qualification at every target viewport before presenting the UI as complete.

### Slice 7: Responsive and Accessibility Qualification

Status: pending

- [ ] Verify 360x800, 390x844, 768x1024, 1024x768, 1280x800, and 1440x900.
- [ ] Verify keyboard operation, focus order, focus restoration, zoom, reflow, status announcements, contrast, and reduced motion.
- [ ] Complete manual assistive-technology review of every critical workflow.

### Slice 8: Screenshot and Release Qualification

Status: pending

- [ ] Create deterministic demonstration data for healthy, empty, degraded, denied, private, cache-heavy, and push scenarios.
- [ ] Capture approved light, dark, desktop, and mobile screenshots.
- [ ] Add visual regression coverage for release-critical views.
- [ ] Publish the strongest screenshots in the README and release assets.

## Screenshot Inventory

1. Operational overview in dark theme.
2. Operational overview in light theme.
3. Failed request investigation.
4. Successful cached pull decision path.
5. Route detail and rewrite preview.
6. Registry health and traffic.
7. Cache explorer and provenance.
8. Registry credential workflow.
9. Mobile overview.
10. Mobile request detail.

## Definition of Done

The polish program is complete when all slices pass their acceptance gates, the old authenticated template and script are removed, the single-container build contains the tested frontend bundle, and the approved screenshots communicate a finished product without explanatory scaffolding.
