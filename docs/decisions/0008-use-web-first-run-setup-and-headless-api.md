# ADR-0008: Use Web First-Run Setup and a Headless API

Status: accepted
Date: 2026-07-20
Supersedes: ADR-0006 for initial bootstrap; ADR-0006 local recovery remains accepted

## Context

The local CLI bootstrap made initial access operationally awkward and left an unauthenticated legacy dashboard visible while no users existed. It also failed the product requirement that bootstrap be obvious and difficult to leave unfinished. A network service with a web control plane should establish its first administrator through that control plane, while deployment automation needs the same capability without a browser.

## Decision

When no local user exists, Regstair exposes only health, static setup assets, `GET /admin/setup`, and `GET|POST /admin/api/setup`. `/admin/` and `/admin/login` redirect to the setup page. Operational and account APIs return `428 setup_required`; the old unauthenticated dashboard does not exist in production.

The setup page creates the first enabled administrator and immediately issues an authenticated web session. The JSON API supports the same one-shot operation for headless automation. Both use an ephemeral process-memory setup token, and browser submission additionally requires a matching Origin. The existing SQLite transaction remains the final concurrency boundary, so only one first administrator can be created.

Compose binds Regstair to `127.0.0.1` by default. Operators must complete setup over a trusted local connection before explicitly selecting a non-loopback bind address or publishing the service through a TLS reverse proxy. Regstair has no default password and does not print or generate an administrator password.

Host-only password recovery remains a separate local CLI operation because a pre-authentication web recovery endpoint would create an account-takeover path.

## Consequences

- First use is a complete product workflow instead of a deployment ceremony.
- Browser and API automation share one transactional service boundary.
- An attacker with access to an uninitialized, intentionally network-exposed instance could race to claim it; loopback-by-default binding and setup-before-exposure are mandatory controls.
- Restarting before setup changes the ephemeral setup token. Completing setup permanently closes the endpoint.

## Verification

- The legacy dashboard and operational APIs are inaccessible with zero users.
- Setup page and status API clearly report first-run state.
- Missing setup token and cross-origin browser submissions fail.
- Concurrent or repeated setup creates exactly one administrator.
- Successful setup creates a session and redirects the browser into the authenticated UI.
- Existing local recovery revokes sessions and Docker tokens and remains unavailable over HTTP.
