# ADR-0010: Bind Cached Objects to Logical Repository Authorization

Status: accepted
Date: 2026-07-21

## Context

Regstair deduplicates OCI content globally by digest. A digest can therefore be present because one user fetched a private repository, while another request asks for the same bytes through a different repository or route. Content-addressed storage proves integrity, not authorization. Serving a global blob solely because its digest exists would expose private content and would also allow stale credentials or disabled accounts to replay it during an upstream outage.

## Decision

Every cached manifest and referenced blob receives one or more authorization bindings. A binding contains the logical repository, route, source, physical repository, manifest digest, object digest and kind, credential strategy, and, only for user-bound access, the local user ID.

Bindings deliberately do not contain a Docker token ID or upstream credential version. At cache-read time Regstair resolves the current route, finds an exact repository/route/object binding, and applies its strategy:

- `challenge`: cached pulls may be served anonymously.
- `proxy`: the requester must be an enabled authenticated local user and the configured shared connector remains responsible for upstream identity.
- `current_user_required`: the requester must be the same enabled local user and that user must still have a credential for the bound source.

Credential removal and user disablement therefore block replay immediately. A verified replacement credential for the same user and source preserves replay. Global physical deduplication remains unchanged; only authorization metadata is repository-bound.

All enabled configured registries are valid credential targets. There is no separate approval flag. Registry/source ownership otherwise remains governed by ADR-0002, and route ownership by ADR-0009.

## Consequences

- Manifest and blob `GET` and `HEAD` paths must use the repository-aware resolver; direct reads from the global content store are forbidden at the HTTP boundary.
- A cached object without a matching binding fails closed.
- Private outage replay remains available to the user who established the binding while current account and credential state remain valid.
- Credential replacement does not invalidate useful cached content merely because ciphertext or credential record time changed.
- Registry mounts must not begin until repository confusion, cross-user access, revocation, replacement, anonymous precedence, and real Harbor outage replay pass.

## Verification

- Memory and SQLite repositories have binding parity and exact logical-repository, route, object, and user scoping tests.
- Resolver abuse tests deny anonymous, second-user, and alternate-repository reads of private cached manifests and blobs.
- Gateway tests prove both blob `GET` and `HEAD` pass repository and principal context to authorization.
- Runtime tests prove credential removal and user disablement reject immediately while verified same-user replacement remains valid.
- The real Harbor smoke test stops Harbor and proves authenticated manifest/blob replay, anonymous denial, deletion denial, and replacement-preserved replay.

## Non-Goals

- Binding grants to individual Docker tokens.
- Binding grants to encrypted credential versions or timestamps.
- Implementing registry mounts or namespace shortcuts.
- Treating digest equality as an authorization relationship between repositories.
