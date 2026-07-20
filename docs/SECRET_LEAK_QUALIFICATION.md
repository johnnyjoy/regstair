# Secret-Leak Qualification

Status: passing implementation baseline
Last run: 2026-07-20

## Canary Method

Tests submit unique, recognizable values through each secret-bearing channel and fail on an exact byte/string match in any prohibited output. This supplements code review with negative execution evidence. Hashes, encrypted envelopes, controlled classifications, request surfaces, and operation names remain available as useful diagnostics.

## Executed Matrix

| Flow | Forced condition | Scanned surfaces | Evidence |
| --- | --- | --- | --- |
| Web login | Incorrect submitted password | Captured structured logs and HTTP response | `TestFailedAuthenticationLogsAndResponseContainNoSubmittedPassword` |
| Credential verification | Upstream error containing the submitted secret and private detail | HTTP response, audit JSON, SQLite main file and WAL | `TestCredentialCanaryAbsentFromAPIAuditAndSQLiteAfterFailedAndSuccessfulFlows` |
| Credential persistence | Successful encrypted save | HTTP response, audit JSON, SQLite main file and WAL | Same test plus `TestEncryptedRegistryCredentialPersistenceContainsNoPlaintext` |
| Authentication persistence | Successful password hash, Docker-token issue, web-session issue, CSRF issue, and encrypted upstream credential | SQLite main file and WAL | `TestSQLitePersistsSecurityStateWithoutRecoverableAuthenticationSecrets` |
| Panic recovery | Panic value plus secrets in query, Authorization header, and request body | Captured structured logs and HTTP 500 response | `TestRecoverHTTPDoesNotLeakPanicOrRequestSecrets` |
| Upstream challenge | Bearer realm controlled by an unapproved host | Attacker-observed request credentials and returned error | `TestHTTPConnectorRejectsUnapprovedBearerRealmWithoutSendingCredentials` |
| Upload continuation | Absolute upload location controlled by another origin | Attacker-observed request credentials and returned error | `TestHTTPConnectorRejectsCrossOriginUploadLocationWithoutSendingCredentials` |
| Readiness failure | Closed SQLite repository | Readiness HTTP response | `TestReadinessDetectsClosedMetadataWithoutLeakingInternalError` |

## Allowed Diagnostics

- Public error code and stable remediation message.
- Operation and coarse request surface (`admin`, `oci`, `probe`, or `other`).
- Registry/source ID, route, error classification, outcome, and timestamps where already allowlisted.
- Password and token hashes, encrypted credential envelopes, nonces, and key IDs only inside their intended persistent security records; these are never returned by APIs or audit views.

## Prohibited Values

- Local passwords, Docker token values, session values, CSRF tokens, upstream credentials, authorization headers, encryption keys, and plaintext panic values.
- Internal upstream response details in public errors.
- SQLite paths, SQL/driver errors, or content-store paths in readiness output.

## Nonexistent Surfaces

Regstair currently exposes no metrics endpoint and creates no support bundle. These surfaces are therefore not marked as tested. Any future metrics, tracing, diagnostic export, or support-bundle feature must add canary coverage before release.

## Commands

```bash
go test ./...
go test -race ./internal/security ./internal/auth ./internal/admin ./internal/registry ./internal/app
```
