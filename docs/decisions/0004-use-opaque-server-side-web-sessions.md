# ADR-0004: Use Opaque Server-Side Web Sessions

Status: accepted
Date: 2026-07-19

## Context

The next phase adds authenticated browser mutations. Client-contained sessions would make immediate user disablement, access-level changes, and session revocation harder and would place more authorization state in the browser.

## Decision

Use 32-byte cryptographically random opaque session tokens. Store only a SHA-256 token digest with user ID, creation, last-seen, idle expiry, absolute expiry, and revocation time. SHA-256 is appropriate here because session tokens are uniformly random high-entropy values, not human passwords.

The cookie is named `__Host-regstair_session`, has `Path=/`, no `Domain`, `HttpOnly`, `SameSite=Lax`, and `Secure` in TLS deployments. In explicit local-development HTTP mode, use a non-`__Host-` development cookie and refuse that mode when production security is configured. Rotate the token at login and privilege changes. Initial expiry is 30 minutes idle and 12 hours absolute. Logout, password reset, access change, and user disablement revoke affected sessions.

Every state-changing browser request requires a 32-byte random synchronizer CSRF token bound to the server session, constant-time verification, same-origin `Origin` validation when present, and form/header token submission. SameSite is defense in depth, not the sole CSRF control.

## Consequences

- SQLite session lookup occurs on authenticated browser requests.
- Cleanup can delete expired/revoked sessions after an audit-safe retention window.
- TLS termination and secure-cookie configuration must be explicit in deployment docs.

## Implementation Plan

- Add session records and repository operations under `internal/metadata/`.
- Add session and CSRF services under `internal/auth/`.
- Add middleware that loads the current user and enforces role checks server-side.
- Add rate limiting for login and sensitive actions.
- Reference ADR-0004 at session middleware and CSRF enforcement entry points.

## Verification

- [ ] Session fixation fails because login rotates the token.
- [ ] Only token digests are stored.
- [ ] Idle and absolute expiry are enforced.
- [ ] Logout, disable, password reset, and access change revoke sessions.
- [ ] Missing, incorrect, and cross-session CSRF tokens fail.
- [ ] Cookie attributes are tested in TLS and explicit development modes.

## References

- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)

