# Regstair Next-Level Threat Model

Status: release-qualified implementation baseline
Last updated: 2026-07-21

## Protected Assets

- Local user password hashes and account status.
- Docker access-token hashes and lifecycle state.
- Plaintext and encrypted upstream registry credentials.
- Credential-encryption keys mounted outside SQLite.
- Web sessions and CSRF tokens.
- Global route and registry configuration.
- Cached private manifests and blobs.
- Request, provenance, and administrative audit records.

## Trust Boundaries

1. Browser to Regstair web endpoints.
2. Docker/OCI client to Regstair `/v2/` endpoints.
3. Regstair to configured upstream registries and token services.
4. Regstair process to SQLite and content storage.
5. Regstair process to mounted credential keys and host-only recovery secret files.
6. Container/deployment administrator to the runtime and persistent volume.

## Required Data Flows

### Web Login

The browser submits a local password over TLS. Regstair verifies Argon2id, rotates an opaque server-side session, sets a protected cookie, and never returns or stores the password.

### Docker Login and OCI Request

The Docker client submits local username and a time-limited Regstair token. Regstair validates the token hash, expiry, revocation, enabled user, and route authorization. Web passwords are not accepted.

### Verify and Save

An authenticated user submits an upstream username and secret with CSRF protection. Regstair verifies only the configured approved source and capability. On success it encrypts with source/user-bound associated data and commits credential plus audit atomically. On failure it stores no credential.

### Runtime Pull or Push

Regstair authenticates/authorizes the local user, matches the global route, chooses exactly one configured credential mode, decrypts only the selected user's credential when required, contacts only the configured upstream host, and records redacted outcome/provenance.

## Primary Threats and Controls

| Threat | Required controls |
| --- | --- |
| Offline password cracking after DB theft | Argon2id, unique salts, parameter upgrades, strong password length |
| Session theft or fixation | TLS, protected cookies, opaque random tokens, digest-only storage, rotation, expiry, revocation |
| CSRF on credential/user mutations | Synchronizer token, Origin validation, SameSite cookie, server authorization |
| Cross-user credential access | User-scoped service queries, associated-data binding, negative authorization tests |
| Database-only credential disclosure | AES-256-GCM and keys outside SQLite |
| Ciphertext swapping or tampering | AEAD authentication and record/user/source associated data |
| Key loss or compromise | Key IDs, keyring, backup, rotation command, readiness failure, documented recovery |
| Secret leakage | Structured allowlisted errors/audit details, redaction, fixture scans across all outputs |
| Malicious upstream challenge or redirect | Configured-host allowlist, HTTPS policy, no client Authorization forwarding, redirect host checks |
| Dependency-confusion fallback | Treat 401/403 distinctly, honor namespace authority, never retry unrelated credentials |
| Cached private-content exposure | Exact repository/route/object binding; repository-aware manifest and blob reads; cache presence never grants access |
| Stale private-cache replay | Recheck enabled user and current source credential on every user-bound cache read; do not bind to token or credential version |
| Disabled user retaining access | Session and token revocation plus enabled check on every authentication |
| Bootstrap takeover | Loopback bind by default, explicit setup-only mode, ephemeral setup token, same-origin browser submission, one-shot transaction, and setup before network exposure |
| Concurrent credential replacement | SQLite transaction, unique user/source constraint, atomic audit coupling |
| Audit repudiation | Append-only events and mutation/audit transaction coupling |
| Login/token brute force | Process-local source-address and normalized-account failure buckets, generic failures, bounded expensive hash work, `Retry-After`, and structured operational signal |

## Authentication Abuse Controls

- Browser login and presented Docker/OCI credentials allow five failures in a five-minute window, then block both the source-address and account buckets for fifteen minutes.
- The check occurs before Argon2id password verification and Docker-token/configured-client validation. A blocked request therefore cannot trigger additional expensive password work.
- A successful authentication clears both buckets. Password reset, role change, disablement, token revocation, and session revocation still take effect immediately through repository-backed validation and are not cached by the limiter.
- Docker requests without an Authorization header do not enter the credential limiter. Sources and routes intended for anonymous pull therefore retain anonymous precedence even when presented credentials from the same address are blocked.
- Responses are generic and expose only an integer `Retry-After`. Structured warnings name only `admin_login` or `docker_auth`; they omit username, source address, password, token, and request details.
- Buckets are process-local and deliberately ignore forwarding headers. A trusted reverse proxy should enforce an additional edge limit; Regstair does not trust attacker-controlled `X-Forwarded-For`. Multi-replica deployments require a shared edge limiter for aggregate enforcement.

## Abuse-Case Evidence

| Case | Evidence |
| --- | --- |
| Login threshold, pre-hash block, generic response, and recovery | `TestLoginRateLimitBlocksExpensiveAuthenticationAndRecovers` |
| Docker threshold, generic response, recovery, and anonymous precedence | `TestGatewayAuthenticationRateLimitPreservesAnonymousRequestsAndRecovers` |
| Account bucket across source addresses | `TestFailureLimiterBlocksEitherDimensionAndRecovers` |
| Source-address bucket across rotated usernames | `TestFailureLimiterAddressBucketStopsAccountRotation` |
| Disable, password reset, role change, and token revocation invalidate immediately | Admin/auth account integration tests |
| Malicious Bearer realm and upload redirect cannot receive credentials | Registry HTTP hostile realm/location tests |
| Secret-free database, API, audit, logs, panic, and upstream errors | `docs/SECRET_LEAK_QUALIFICATION.md` and executable canary tests |
| Missing/wrong keys, rotation, backup, and restore | `docs/BACKUP_KEY_LIFECYCLE.md` and CLI/readiness tests |
| Repository confusion, cross-user replay, removal, disablement, and replacement | Resolver/runtime/gateway cache-binding tests and real Harbor outage replay |

## Explicit Non-Guarantees

- A fully compromised Regstair process can access mounted keys and plaintext credentials while using them.
- This phase does not provide HSM-backed keys, enterprise federation, MFA, or tamper-evident external audit storage.
- TLS termination may be external, but production deployment must preserve secure-cookie and authenticated-client semantics.

## Release Checks

- Threat cases above have automated tests where feasible and documented manual evidence otherwise.
- No critical/high unresolved finding.
- Database, logs, APIs, audit records, and panic/support output pass secret-fixture scans.
- Backup/restore and key rotation are exercised in Docker.
