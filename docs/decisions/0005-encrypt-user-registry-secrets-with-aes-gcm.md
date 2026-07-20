# ADR-0005: Encrypt User Registry Secrets with AES-256-GCM

Status: accepted
Date: 2026-07-19

## Context

Per-user upstream passwords and tokens must be recoverable for registry requests but must not be plaintext in SQLite. Encryption keys must remain outside the database and support rotation.

## Decision

Encrypt each secret with AES-256-GCM from Go's standard library. Each value uses a fresh cryptographically random 96-bit nonce. The versioned envelope stores format version, algorithm, key ID, nonce, and ciphertext. Associated data binds the envelope version, credential record ID, user ID, and configured source ID so ciphertext cannot be moved between records or owners.

Keys are 32 random bytes supplied through mounted secret files. Environment-key input is not the production default. Configuration supplies an active key ID and a key-ID-to-file mapping. New writes use the active key; reads may use retained old keys. Rotation re-encrypts records transactionally before an old key is removed. Missing active keys fail readiness and all credential writes; missing historical keys fail affected reads without exposing details.

## Consequences

- Database backup alone cannot reveal credentials and cannot restore them without the key set.
- Key backup, rotation, compromise, and loss procedures are release requirements.
- Plaintext lifetime in memory must be minimized, though Go cannot guarantee memory zeroization.

## Implementation Plan

- Add a versioned credential-encryption service under `internal/auth/` or a focused `internal/secrets/` package.
- Inject a keyring interface; keep file loading at application startup.
- Store envelope fields or one serialized binary envelope in SQLite, never a key.
- Add an offline/administrative rotation command before release.
- Reference ADR-0005 at encryption and key-loading entry points.

## Verification

- [x] Round trip succeeds with the correct key and associated data.
- [x] Nonces differ for repeated plaintext.
- [x] Tampering, wrong key, and moved ciphertext fail authentication.
- [x] Database inspection finds no plaintext fixture.
- [x] Rotation preserves decryptability and changes key ID/ciphertext.
- [x] Startup/readiness behavior for missing and wrong keys is tested.

## References

- [OWASP Cryptographic Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html)
- [OWASP Key Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Key_Management_Cheat_Sheet.html)
