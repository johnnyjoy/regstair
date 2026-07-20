# Regstair Product UI/UX Design Specification

## Document Status

Status: Approved design direction; implementation in progress  
Version: 1.0  
Audience: product developers, UI developers, UX reviewers, and implementation agents

Implementation progress:

- Slice 1, application routes and shell: complete on 2026-07-20. Dedicated page URLs, route-aware navigation, page titles, authorization, and page-scoped server data loading are implemented. The application is rooted at `/`; `/admin` is reserved for administrator-only work. Stable request-detail URLs remain part of Slice 3 as specified below.
- Slice 2, dashboard: complete on 2026-07-20 for the current-data scope. The dashboard provides subsystem state, exact 24-hour request metrics, actionable exceptions, recent activity, and page-scoped drill-downs. Persisted source-health history, content capacity/free-space thresholds, and trends remain unavailable and are not implied by the UI.
- Slice 3, requests: complete on 2026-07-20. The workspace provides presets, relative and absolute time controls, advanced operational filters, removable filter chips, oldest/newest sorting, exact matching counts, cursor pagination, a mobile event list, and stable `/requests/{id}` investigation pages. The former modal implementation has been removed.
- Slice 4, token and confirmation UX: complete on 2026-07-20. One-time Docker tokens provide copy support, guarded close/navigation, and explicit retention acknowledgment. Native confirms/alerts have been replaced by an accessible application dialog and mutation recovery notice. A session-bound readable CSRF cookie enables authenticated mutations across tabs while the session cookie remains `HttpOnly`; authentication/CSRF failures provide a sign-in recovery path.
- Slice 5, audit and administration: complete on 2026-07-20. Audit activity uses human action labels, resolved local identities, safe allowlisted details, outcome/action/actor/target/correlation filters, and correlation links while retaining raw action codes as secondary evidence. User access changes provide exact review summaries, immediate-invalidation consequences, no-change protection, and explicit administrator continuity/recovery guidance.

All five implementation slices in this specification are complete. Items explicitly classified as Should Have or Future below remain separate follow-up work.

Related documents:

- [Hostile UI/UX Audit](UI_UX_HOSTILE_AUDIT.md)
- [Admin Control Plane](ADMIN_CONTROL_PLANE.md)
- [Next-Level Service PRD](NEXT_LEVEL_SERVICE_PRD.md)
- [Cache Speed and Capacity Evaluation](CACHE_EVALUATION.md)

## 1. Product Vision

Regstair is an operational control plane for container-image movement, not merely a registry proxy.

The interface must communicate:

> Every image request is understood, controlled, accelerated, and explainable.

The system may need to understand Docker and Harbor topology, authentication challenges, digests, cache storage, routing precedence, and namespace rewriting. The user should instead be able to answer:

- What happened?
- Why did it happen?
- Is anything wrong?
- What should I do next?

## 2. Product Principles

### Dashboard First

The landing page answers whether Regstair is healthy within five seconds. It prioritizes exceptions and current operating state over inventory totals.

### Task-Oriented Navigation

Navigation reflects user jobs: monitor health, investigate requests, understand routing, inspect sources, manage access, and review security activity.

### Operational Clarity

Every status includes scope and time. Counts without a denominator or time window are avoided. Every failure explains what happened, why, and the next safe action.

### Progressive Disclosure

The first view contains the decision summary. Routing steps, credential mode, provenance, timing, and OCI details are available without becoming the default visual weight.

### Security-Aware UX

Secret values remain one-time or write-only. Destructive actions state their immediate effects. Authentication and CSRF failures provide safe recovery without revealing sensitive distinctions.

### Fast Investigation

Filters, details, and navigation state have stable URLs. An operator can share an investigation, refresh it, open it in another tab, and return with browser back/forward.

### Restrained Infrastructure Design

The visual system favors precision, hierarchy, and density. It does not use decorative dashboards, unnecessary charts, oversized marketing composition, or visual effects that compete with operations.

## 3. Architectural Boundaries

The UI is not a second configuration authority.

- Routes remain YAML-owned and read-only in this phase.
- Approved registries and source policy remain YAML-owned and read-only.
- SQLite owns local users, sessions, Docker tokens, per-user registry credentials, requests, provenance, and audit records.
- The UI may mutate only the SQLite-owned records already supported by authenticated APIs.
- Route/source mutation, config migration, cache eviction, and custom dashboards remain separate product decisions.

The interface must not present unavailable data as if it exists. Cache storage percentage, trends, p95 latency, credential expiry, and source latency require backend metrics before they can be displayed.

## 4. Information Architecture

Authenticated users enter the Regstair application through product routes. Only administrator-only work uses the `/admin` namespace:

```text
/                           Dashboard
/requests                   Requests
/requests/{id}              Request detail
/routes                     Routes
/sources                    Sources
/cache                      Cache and artifacts
/account                    Account, Docker tokens, registry credentials
/admin/users                Users
/admin/audit                Audit
```

Administrators receive `/` as their landing page. Normal users are redirected to `/account` and do not see infrastructure administration. Legacy application pages under `/admin` redirect to their canonical product routes; `/admin/api` remains the API namespace until a separate compatibility migration is designed.

Each route must:

- have a distinct document title and primary heading;
- set `aria-current="page"` in primary navigation;
- survive refresh and support bookmarking;
- preserve query parameters through navigation where relevant;
- support browser back and forward;
- avoid loading unrelated page data.

## 5. Application Shell

### Desktop

- Persistent left navigation.
- Compact top header containing current page, global readiness state, theme/account menu, and explicit refresh where data can become stale.
- Main workspace constrained for readability but wide enough for operational tables.
- No page sections disguised as nested cards.

### Mobile

- Compact top bar with Regstair identity, readiness state, and menu button.
- Navigation drawer with the active destination identified.
- No horizontally scrolling primary navigation.
- Tables transform into job-specific summaries or explicitly declare desktop-only behavior where transformation would remove required information.

## 6. Dashboard

### Purpose

Answer: "Is Regstair healthy, and what needs attention?"

### First Viewport

1. **Health strip:** Regstair readiness, metadata/content/key state, and source exceptions.
2. **Time-scoped operating metrics:** requests, failure rate, cache hit ratio, and response time for a stated window.
3. **Needs attention:** failed sources, authentication failures, denied pushes, low disk space, or unavailable credential storage.
4. **Recent activity:** a short list of consequential pulls, pushes, failures, and denials with Inspect links.

Inventory totals such as route count and source count are secondary metadata, not primary health metrics.

### Data Dependencies

The initial dashboard can use existing request events and readiness. Before claiming complete dashboard coverage, add backend aggregation for:

- request totals by time window;
- hit/miss ratio;
- failure/denial counts;
- average and p95 duration;
- source-health snapshots;
- content bytes, object count, and free-space thresholds.

Trend arrows require historical comparison and must not be inferred from a single snapshot.

## 7. Requests Workspace

Requests are the flagship Regstair workflow.

### Default View

Primary controls:

- reference/client search;
- relative or absolute time range;
- status;
- operation;
- Add filter.

Advanced filters:

- route;
- source or destination;
- client;
- credential mode;
- error classification.

Applied filters appear as removable chips and remain encoded in the URL.

Built-in presets:

- Failures;
- Denied operations;
- Cache misses;
- Recent pushes;
- Authentication failures.

### Desktop List

Default columns:

- time;
- operation;
- logical image reference;
- result;
- source/destination;
- cache result;
- duration;
- inspect action.

Secondary fields belong in configurable columns or request detail. Sorting and result count are required. Pagination preserves every filter and scroll context.

### Mobile List

Each request becomes a stable event row containing operation, image, outcome, source/cache summary, time, duration, and a disclosure affordance. Detail opens as a full-screen route or sheet, not a horizontally panned desktop table.

### Request Detail

Request detail has a stable URL and leads with an investigation summary:

- operation and outcome;
- logical reference;
- safe client identity;
- route and selected source/destination;
- credential source wording such as "Current user credential";
- cache result;
- duration and transferred bytes;
- actionable error classification.

Expandable sections:

- routing decision;
- authentication;
- cache;
- provenance;
- timing;
- sanitized OCI details.

The default detail is not a debug dump. Raw internal IDs and provider implementation details are omitted unless operationally necessary and safe.

## 8. Routes

Routes remain read-only and explain decisions rather than exposing YAML syntax.

Each route shows:

- match pattern and precedence;
- pull source order;
- authoritative source and fallback policy;
- push destination or denial;
- namespace rewrite;
- links to requests that matched the route.

The primary action is "View matching requests," not Edit.

## 9. Sources

Sources remain read-only and answer where images come from and whether that path is usable.

Each source shows:

- safe name and ID;
- current health and last check time;
- capabilities;
- authentication mode and whether required configuration exists;
- trusted token-service host policy without credential details;
- routes using the source;
- links to recent failures and source requests.

Latency is displayed only after the health subsystem records it consistently.

## 10. Cache and Artifacts

### Initial Scope

Use existing data for:

- digest search;
- cached tag mappings;
- physical object count and bytes;
- logical-to-physical mapping;
- provenance;
- requests served from cache.

### Required Operational Scope

After cache metrics and lifecycle controls exist, add:

- used, available, and configured maximum bytes;
- hit ratio by time range;
- largest objects;
- recent fills;
- deduplication ratio;
- eviction and garbage-collection state;
- low/high-water alerts.

Do not offer deletion until reference-safe cache eviction exists. Shared digest content must never be removed through a tag-level action that still has other references.

## 11. Users and Administration

Users live under Administration, separate from the operator dashboard and personal Account.

The user list supports search and filters for role and enabled state. Row summaries show safe identity, role, state, and last modification. Mutations use an overflow or detail action rather than permanently exposing every control.

Role/state changes require:

- visible dirty state;
- a concise change summary;
- immediate-invalidation warning where applicable;
- last-administrator protection;
- success confirmation without a full-page reload where practical.

Password reset explicitly states that sessions and Docker tokens are immediately revoked.

## 12. Account

Normal users see only:

- profile and password;
- Docker access tokens;
- approved registry credentials;
- their recent activity;
- relevant audit activity.

Infrastructure routes, global sources, global users, and system audit are not shown to normal users.

### Registry Credential Flow

1. Choose an approved registry.
2. Enter upstream username and secret.
3. Verify required pull/push capabilities.
4. Save only after successful verification.
5. Present safe metadata and a human classification on failure.

Success identifies the registry, verified capabilities, upstream username, and update time. Failure answers what happened and what the user can safely try next without leaking upstream details.

### Docker Token Flow

Token creation must include:

- label and expiry;
- one-time token value;
- Copy token action;
- copied state announced visually and through `aria-live`;
- safe `docker login` instructions;
- guarded close until the user confirms the token was retained;
- simple list/revoke/expiry/last-used visibility.

The interface does not become a general credential-management platform.

## 13. Audit

Audit presents human activity, not database records.

Each event shows:

- safe actor display name and username where allowed;
- human action phrase;
- safe target name;
- relative time plus exact timestamp;
- outcome;
- correlation link to an affected request or entity where available.

Filters:

- actor;
- action category;
- outcome;
- time range.

Machine action names and IDs may appear in expandable technical details, not as the primary reading experience.

## 14. Help and Empty States

Help remains part of the application rather than a separate site explaining every control.

- Put short guidance next to the decision or unfamiliar concept.
- Use expandable detail for deeper technical context.
- Avoid page-wide guides that duplicate labels and force experts to scroll past instruction.
- Every empty state explains what is absent, why it matters, and the next valid action.
- Do not present an action the current role or configuration cannot complete.

## 15. Error and Confirmation Design

Every error answers:

1. What happened?
2. Why, at the safest useful level?
3. What can the user do next?

Rate limits expose retry timing without changing generic credential wording. Expired session or CSRF state offers refresh or sign-in recovery. Native `alert` and `confirm` are replaced by consistent in-product feedback and confirmation dialogs.

Destructive confirmations identify the exact target and effect. Token revocation, credential removal, user disablement, role changes, and password reset are distinct interactions.

## 16. Accessibility Standard

Target: WCAG 2.2 AA.

Required:

- semantic landmarks and heading order;
- keyboard operation and visible focus;
- `aria-current` navigation state;
- minimum target sizes;
- text alternatives and non-color status labels;
- focus management and restoration for dialogs/sheets;
- associated field errors and status announcements;
- reduced-motion, forced-colors, and increased-contrast support;
- 320/768/1024/1440 responsive validation;
- 200% and 400% zoom validation;
- no incoherent overlap or text clipping.

Automated accessibility checks complement, but do not replace, keyboard and screen-reader task verification.

## 17. Visual Language

Desired qualities:

- Apple-like precision;
- Vercel-like simplicity;
- Linear-like hierarchy;
- Datadog-like operational density.

Use semantic green, yellow, red, and blue with text and symbols. Avoid excessive bolding, card grids, decorative gradients, oversized headings, and meaningless charts. Typography and spacing optimize scanning. Icons use the established icon library if one is adopted; familiar symbols replace verbose button labels where safe, with tooltips and accessible names.

## 18. Implementation Strategy

Avoid a framework rewrite as the first step. The backend and security boundaries are sound, and server-rendered routes can establish the correct product architecture. Introduce a frontend framework only when measured interaction complexity and component duplication justify it.

### Slice 1: Application Routes and Shell

- Create dedicated server-rendered routes.
- Add active navigation and route-specific titles.
- Separate Account and Administration from operations.
- Preserve authorization and CSP boundaries.

Acceptance:

- refresh/back/forward/bookmark work;
- admin and normal-user navigation differ correctly;
- no route loads unrelated workspace data;
- desktop and mobile navigation are coherent.

### Slice 2: Dashboard

- Build health-first landing page from truthful existing data.
- Add attention items and recent activity.
- Add backend aggregations before displaying ratios or latency summaries.

Acceptance:

- an operator can identify readiness and current failures in five seconds;
- every attention item drills into a filtered workspace;
- every metric states its time scope.

### Slice 3: Requests

- Add compact default filters, advanced filters, chips, presets, result count, and stable detail routes.
- Add desktop table and mobile event-list presentations.

Acceptance:

- filtering never loses location or context;
- request detail is bookmarkable;
- failed pull and denied push investigations complete without returning to the dashboard;
- mobile investigation requires no horizontal table panning.

### Slice 4: Token and Confirmation UX

- Add copy/confirmation/guarded-close behavior.
- Replace native alerts/confirms.
- Add CSRF/session recovery across tabs.

Acceptance:

- token cannot be silently dismissed before retention acknowledgment;
- mutation failures provide an actionable recovery path;
- opening an authenticated page in a new tab does not create a non-functional session.

### Slice 5: Audit and Administration

- Humanize audit records and add filters/correlation.
- Move user mutations into deliberate workflows with change summaries.

Acceptance:

- an administrator can answer who changed a user or credential and when;
- disablement, role change, reset, and last-administrator protection remain immediate and explicit.

## 19. Release Priority

### Must Have

1. Real application routing and shell.
2. Health-first dashboard.
3. Requests workspace and stable request detail URLs.
4. One-time token UX and consistent confirmations.
5. Cross-tab authenticated mutation recovery.

### Should Have

6. Human audit workspace.
7. Source-health workspace.
8. Mobile request workflow.
9. User filters and deliberate mutation flow.

### Future

10. Saved investigations.
11. Custom dashboard composition.
12. Advanced analytics and trends.
13. Mutable route/source configuration, only after a separate ownership and migration design.

## 20. Final Product Standard

Regstair should feel like an exact, calm interface around infrastructure normally reserved for command-line experts. Complexity remains available for investigation, but it does not become the default user experience.

The implementation is successful when operators trust the summary, can explain any request quickly, and know the next safe action when something fails.
