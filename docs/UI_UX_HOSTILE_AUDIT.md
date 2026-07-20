# Hostile UI/UX Audit

Date: 2026-07-20
Scope: authenticated administrator and user workflows, responsive behavior, interaction implementation, and comparison with established operational web-app patterns represented in Mobbin.

## Verdict

Regstair is functionally broad, semantically conscientious, and visually coherent, but it is not yet a strong operational product. It is a server-rendered document containing an application, rather than an application organized around jobs. The interface makes every capability visible but makes repeated work slow, context-poor, and difficult to resume.

Scorecard:

| Dimension | Score | Finding |
| --- | ---: | --- |
| Information architecture | 3/10 | One long page combines unrelated operator, administrator, and personal-account jobs. |
| Operational investigation | 4/10 | Strong raw data and request detail, weak navigation, filtering, saved context, and comparison. |
| Account and credential workflows | 6/10 | Core flows exist and protect secrets, but recovery, copy, confirmation, and failure feedback are incomplete. |
| Mobile usability | 3/10 | Responsive containment prevents overlap, but horizontal navigation and desktop tables are not mobile workflows. |
| Accessibility foundation | 7/10 | Semantic HTML, keyboard targets, reduced motion, contrast modes, and native dialogs are good; application-state and navigation semantics remain incomplete. |
| Visual hierarchy | 5/10 | Consistent tokens and restrained styling, but nearly every section has equal weight and summary metrics are weak. |
| Overall | 4.5/10 | Credible internal prototype; not yet comparable to top operational web applications. |

## Critical Findings

### 1. The one-page architecture defeats the primary job

The sidebar is a list of fragment links into one document. Account, Docker tokens, registry credentials, audit, users, page help, and summary metrics all precede Requests in the DOM. Request filtering submits to `/admin/` without `#requests`, returning the operator to the top after every query. Routes, sources, and artifacts continue below the request table, making the page progressively longer as the product grows.

Top operational products use stable, dedicated workspaces. Vercel Logs uses a dedicated tab with persistent facets and a main event stream. Linear places member administration under Settings > Administration > Members and provides role/status filtering. Datadog gives management tables dedicated views, configurable columns, and detail panels.

Required correction: split Overview, Requests, Routes, Sources, Cache/Artifacts, Users, Audit, and Account into routes with stable URLs. Keep account and personal credentials out of the operations landing page.

### 2. The overview does not explain operational health

The seven metrics are raw counts: sources, routes, artifacts, cached digests, recent requests, cache hits, and denied requests. They have no time window, denominator, trend, severity, target, or drill-down. `Cache hits: 12` is not actionable without total pulls or hit ratio. `Denied: 3` does not distinguish expected policy from attack or failure. The overview omits readiness, unhealthy sources, storage use, cache capacity, error rate, p95 latency, and credential/key health.

Required correction: make the first viewport answer: Is Regstair healthy? What changed? What needs attention? Use a compact health strip, time-scoped traffic/error/cache metrics, and an exceptions list linked to filtered requests.

### 3. Request filtering exposes the schema instead of supporting investigation

Ten controls are displayed at once in a five-column grid. Machine-oriented values such as error classification and client identity receive the same visual weight as reference and status. There are no applied-filter chips, saved views, presets, relative time ranges, column controls, sorting, or result count. Dates require two raw UTC datetime inputs. The table is 1,180 pixels wide and horizontal scrolling is the mobile strategy.

Required correction: show search, time range, status, and operation first. Put route/source/client/classification in an Add filter menu. Render applied filters as removable chips in the URL. Add presets such as Failures, Denied, Pushes, Cache misses, and Current user. Preserve filters while opening details and support shareable URLs.

### 4. Mobile is contained, not designed

Below 760 pixels, the sidebar becomes a horizontally scrolling row containing up to nine text links. It has no selected state, overflow affordance, or persistent access after scrolling. Operational tables retain desktop minimum widths of 760-1,180 pixels. Users must pan horizontally while keeping row identity and headers in memory. Dense forms simply collapse into a long vertical sequence.

Required correction: use a compact top bar plus navigation drawer or bottom-level section switcher. Replace request rows with a mobile event list showing time, operation, reference, outcome, and source; open details as a full-screen sheet. Keep large configuration tables desktop-only only where mobile is explicitly unsupported, and state that boundary.

### 5. One-time token handling is unsafe UX

The generated Docker token appears as selectable code with no Copy button or copy confirmation. Closing the dialog permanently loses the only display, but there is no close guard acknowledging that loss. The user can believe creation succeeded while failing to retain the credential.

Required correction: provide Copy token, reveal/copy confirmation, a clear `Copied` state, and a guarded close until the user confirms the token was stored. Show the exact `docker login` command with the secret excluded or safely inserted only into a copy action.

### 6. A valid web session can become mysteriously non-functional in a new tab

The session cookie is shared across tabs, but the CSRF token is stored in `sessionStorage`, which is tab-scoped. Opening `/admin/` in a new tab can render an authenticated page while mutations send an empty CSRF token and fail. Most mutation handlers then show generic alerts such as “could not be updated,” with no recovery path.

Required correction: provide an authenticated CSRF bootstrap endpoint or render a fresh CSRF token into every authenticated page. On CSRF/session failure, distinguish expired interaction state and offer a safe refresh or reauthentication path.

### 7. Audit data is technically present but operationally unreadable

Audit rows display opaque actor and target IDs, machine action names, and no expandable human explanation. There is no filtering, pagination control, correlation, or navigation to the affected user/credential. This is storage inspection, not an audit product.

Required correction: resolve safe display identities, translate actions into human language, show relative and exact time, support actor/action/outcome filters, and link to affected entities and correlated request details without exposing secrets.

## High-Severity Findings

- Navigation order and document order disagree: Audit appears before Registry credentials in navigation but after it in the page.
- Navigation has no `aria-current` or visible active section. Anchor scrolling gives no persistent location feedback.
- User role and enabled state are editable inline with a separate Save button but no dirty state, change summary, or confirmation for access loss.
- Credential removal, token revocation, and several failures use native `confirm`/`alert`, producing inconsistent, context-poor interactions.
- The UI masks authentication rate-limit responses as ordinary bad credentials and does not present `Retry-After`, encouraging repeated attempts during lockout.
- Source, route, artifact, and request data are server snapshots with no refresh control, polling state, or stale-data indicator beyond a footer timestamp.
- Request detail is modal-only. It cannot be linked, opened in another tab, compared with another request, or retained through refresh.
- Local client identity is often an opaque user ID in request/audit records rather than a safe username/display label.
- Summary panels, account panels, settings, and operations all use the same bordered-panel treatment, flattening urgency and hierarchy.
- Empty states report absence but rarely offer the next valid action or a filtered explanation.

## What Is Good

- Setup is a genuine first-run product state rather than a hidden CLI prerequisite.
- Semantic landmarks, headings, labels, captions, fieldsets, keyboard-sized controls, skip link, native dialogs, focus restoration, reduced-motion support, forced-colors handling, and text status labels form a better accessibility baseline than many internal tools.
- Secret workflows clear submitted values, avoid redisplay, and keep credential state metadata-only.
- Request details lead with an investigation summary and keep technical explanation/provenance expandable.
- Light, dark, and system themes are implemented without a framework dependency.
- The visual system is restrained and appropriate for an operations tool; the problem is structure and workflow, not lack of decoration.

## Mobbin-Caliber Comparison

Mobbin's strongest references are useful because they capture shipped flows rather than isolated attractive screens. Regstair should emulate the recurring patterns, not another product's branding:

| Reference pattern | Strong-product behavior | Regstair gap |
| --- | --- | --- |
| Vercel Logs/activity | Dedicated workspace, persistent facets, chronological event list, inspectable detail | Requests are one section in a long page; filters are an exposed schema grid; details are not linkable. |
| Datadog management/explorers | Dense but configurable tables, bulk/context actions, facets, saved scope, clear object counts | Fixed columns, no sorting/customization, no saved views, no bulk investigation. |
| Linear administration | Settings hierarchy, role/status filters, row overflow actions, suspended-user history | Account, security, users, and operations are mixed; every action is visible inline. |
| Vercel/Stripe-style secret issuance | One-time value, explicit copy action, confirmation, scoped metadata, recoverable revoke/reissue path | One-time value is plain selectable text and can be dismissed without acknowledgment. |
| Modern responsive operations apps | Mobile event summaries and full-screen detail sheets; complex tables adapt or declare desktop scope | Horizontal nav and horizontally panned desktop tables. |

## Recommended Redesign Sequence

1. Create real routes and layouts: Overview, Requests, Routes, Sources, Cache, Users, Audit, Account.
2. Rebuild Requests as the flagship workflow with compact filters, chips, presets, stable detail URLs, and mobile event rows.
3. Rebuild Overview around health, exceptions, traffic, error rate, cache hit ratio/capacity, and drill-down.
4. Fix CSRF cross-tab recovery and one-time-token copy/close behavior.
5. Move personal account, Docker tokens, and registry credentials into Account; keep Users and Audit under Administration.
6. Replace native confirmations and generic alerts with consistent dialogs/toasts and actionable error states.
7. Add active navigation, responsive navigation, human audit identities, sortable/configurable tables, and refresh/staleness controls.
8. Validate with task-based tests: diagnose a failed pull, identify a denied push, issue and retain a token, replace a credential, disable a user, and inspect the resulting audit event on desktop and mobile.

Do not begin with color, animation, charts, React, or MUI. The highest-value work is routing, hierarchy, investigation continuity, and failure recovery. A framework may make the resulting components easier to maintain, but it cannot repair the current information architecture by itself.
