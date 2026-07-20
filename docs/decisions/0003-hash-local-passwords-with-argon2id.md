# ADR-0003: Hash Local Passwords with Argon2id

Status: accepted
Date: 2026-07-19

## Context

Regstair will authenticate locally managed web users. Passwords require an adaptive, memory-hard one-way representation and an upgrade path when cost parameters change.

## Decision

Use Argon2id with a unique 16-byte random salt and a PHC-style encoded hash containing algorithm version and parameters. Initial parameters are 19 MiB memory, 2 iterations, and parallelism 1. Passwords must be 15-128 Unicode code points, may contain spaces, receive no composition-rule requirement, and are rejected rather than truncated. Comparison is constant-time. Successful authentication rehashes when stored parameters are below current policy.

The initial parameters follow the current OWASP minimum and must be benchmarked inside the project container before release. A server-side pepper is not required in this phase because the encrypted upstream-secret key already creates a separate high-value key lifecycle; pepper support may be added only with its own rotation design.

## Consequences

- Add `golang.org/x/crypto/argon2`.
- Login is intentionally CPU/memory bounded and requires rate limiting in the handler layer.
- Hash format changes do not require a schema change.

## Implementation Plan

- Add a password hasher/verifier in `internal/auth/` with injectable randomness and parameters for tests.
- Store only the encoded hash in the user record.
- Add rehash-on-success through the user service transaction.
- Reference ADR-0003 at the password service entry point.

## Verification

- [ ] Equal passwords produce different hashes.
- [ ] Correct verification succeeds and incorrect verification fails.
- [ ] Malformed hashes fail safely.
- [ ] Boundary lengths and Unicode are tested without truncation.
- [ ] Parameter upgrades trigger rehash.
- [ ] Container benchmark confirms acceptable latency and memory.

## References

- [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)
- [NIST SP 800-63B-4](https://tsapps.nist.gov/publication/get_pdf.cfm?pub_id=959882)

