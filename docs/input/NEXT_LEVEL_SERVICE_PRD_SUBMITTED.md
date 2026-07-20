# Regstair Next-Level Service PRD

**Product:** Regstair  
**Working description:** Unified OCI registry gateway and caching proxy  
**Document status:** Implementation PRD  
**Phase:** Local users, approved registry credentials, and premium control-plane UI  
**Primary implementation language:** Go  
**Target deployment:** Small and medium enterprise networks  
**Primary clients:** Docker and other OCI-compatible clients

---

# 1. Purpose

Regstair provides one stable OCI-compatible endpoint for accessing all registries configured by an organization.

Clients should not need to know:

- where an image physically resides;
- which registry product stores it;
- which upstream source contains it;
- which destination accepts a push;
- whether content is being served from cache;
- which upstream credentials are required.

A client interacts with one address:

```text
registry.enterprise.example
```

Regstair applies global routes to:

- resolve pulls across internal and approved external registries;
- cache content locally by digest;
- serve cached content during upstream failures when policy permits;
- route pushes to the configured destination;
- preserve provenance;
- enforce namespace authority and fallback rules;
- explain each routing decision.

Regstair must be clean, secure, fast, useful, and easy to use.

---

# 2. Product Mission

> Provide one seamless, secure, fast, and policy-controlled point of access for every configured internal and external OCI registry.

Regstair is a registry abstraction and caching layer. Registry topology is an administrative concern, not a client concern.

Public content should normally work without authentication. Authentication and upstream credentials are introduced only when required for private pulls, pushes, account administration, or audit attribution.

---

# 3. Current Product Position

The existing product already supports:

- one OCI-compatible endpoint;
- ordered pull resolution;
- namespace authority and controlled fallback;
- external retrieval;
- content-addressed caching;
- cached pulls during upstream outage;
- deterministic push routing;
- SQLite persistence;
- client Basic authentication;
- route authorization;
- proxy-owned upstream credentials;
- upstream bearer-challenge handling;
- authenticated registry fixtures;
- Harbor robot-account integration;
- Docker CLI login, pull, and push;
- request filtering and cursor pagination;
- routing explanations and provenance;
- a responsive read-only administrative UI;
- light, dark, and system themes;
- integrated help;
- secret redaction;
- content security policy;
- automated smoke coverage.

The next phase makes the service practical for multiple locally managed users and gives those users a polished way to configure verified credentials for approved registries.

---

# 4. Phase Outcome

This phase is complete when:

1. A Regstair administrator can create, edit, disable, and delete local users.
2. A local user can sign into the Regstair web interface.
3. An administrator can configure which registries are available for user credentials.
4. A user sees only those approved registries.
5. A user can enter a username and password or token for an approved registry.
6. Regstair verifies the credential before accepting it.
7. Regstair securely stores the verified credential.
8. Regstair uses the correct user credential for configured private pulls or pushes.
9. Public anonymous pulls continue to work without user credentials.
10. The resulting UI is coherent, responsive, accessible, and substantially better than the current control plane.

---

# 5. Core Rules

## 5.1 One stable endpoint

```bash
docker pull registry.enterprise.example/library/postgres:17
docker push registry.enterprise.example/team-a/service:42
```

## 5.2 Routes are global

Routes are configured by administrators and are never created per user.

A route determines:

- logical namespace matching;
- pull-source order;
- namespace rewriting;
- authoritative source;
- fallback behavior;
- cache policy;
- freshness policy;
- push destination;
- upstream credential mode.

User identity does not change the route. It determines whether the user may perform the operation and which configured user credential is available to the selected upstream registry.

## 5.3 Public pulls remain anonymous by default

Approved public external content and explicitly public internal content should be accessible without a Regstair login when the matching route permits it.

Regstair should not request or use a user credential for a public pull unless the registry configuration explicitly requires authenticated access.

## 5.4 User credentials are primarily for private pulls and pushes

Most users will need registry credentials only when:

- pushing to an approved registry;
- pulling private content;
- accessing an upstream that requires authentication.

## 5.5 Registry access is administrator-controlled

Users cannot add arbitrary registry hosts.

The credential interface lists only registries that are:

- configured and enabled by an administrator;
- permitted to use user-linked credentials;
- relevant to at least one configured pull or push route.

## 5.6 Routing remains deterministic

Request-time routing is rule-based and explainable. Authentication failure, missing credentials, or upstream failure must not silently redirect a request to an unrelated public source.

---

# 6. Local User Accounts

Regstair uses local user accounts managed by a Regstair administrator.

A user record supports:

- web-interface authentication;
- ownership of upstream registry credentials;
- attribution of private pulls and pushes;
- administrative access when the user is an administrator.

The first implementation needs two access levels:

- `user`
- `admin`

More elaborate role systems are not required for this phase.

## 6.1 Local user record

```text
id
username
password_hash
display_name
email
access
enabled
ctime
mtime
```

Requirements:

- usernames are unique;
- passwords are stored only as strong password hashes;
- disabled users cannot sign in or initiate authenticated operations;
- changing a record updates `mtime`;
- `ctime` never changes.

---

# 7. Docker Client Authentication Decision

The mechanism used by a Docker client to authenticate to Regstair is a separate decision from the local web login.

Two viable approaches remain:

1. Use the local Regstair username and password with `docker login`.
2. Use the local username and a Regstair-generated token in place of the password.

The implementation must not assume the token approach until the decision is made.

If tokens are selected:

- every token must have an expiration time;
- the raw token is shown only when created;
- Regstair stores only a secure token hash;
- a token can be revoked before expiration;
- token scope should remain no broader than necessary.

This decision must be resolved before implementing authenticated Docker operations for the new local-user model.

---

# 8. Approved Registry Configuration

Each upstream registry record defines whether and how Regstair may use it.

## 8.1 Registry record

```text
id
name
host
kind
enabled
allow_anonymous_pull
allow_user_credentials
allow_private_pull
allow_push
verification_repository
ctime
mtime
```

The administrator configures:

- display name;
- registry host;
- internal or external classification;
- whether anonymous pull is supported;
- whether users may save credentials;
- whether those credentials may be used for private pull;
- whether those credentials may be used for push;
- the repository or namespace used for credential verification.

The administrator may also configure a shared Regstair-owned upstream credential where the existing product already supports it.

---

# 9. User Registry Credentials

## 9.1 User input

For each approved registry, the user enters only:

- username;
- password or token.

Provider-specific labels may improve clarity, but the underlying fields remain username and secret.

Examples:

```text
GitHub username
Personal access token
```

```text
Harbor username
Password or CLI secret
```

## 9.2 Persistent record

```text
id
user_id
registry_id
username
encrypted_secret
ctime
mtime
```

A unique constraint must prevent more than one active credential record for the same user and registry.

Additional state should be added only when required by implementation. Successful verification can be represented by the existence of the saved record because unverified credentials are not accepted.

## 9.3 Secret storage

The credential must never be stored in plaintext.

For this phase, use authenticated encryption with:

- the encryption key supplied outside the SQLite database;
- a unique nonce for each encrypted value;
- authenticated ciphertext;
- complete redaction from logs, API responses, errors, audit records, and support output.

The storage implementation should be internally replaceable, but integration with external vault products is not part of this phase.

---

# 10. Credential Verification

The user action is **Verify and Save**.

Regstair must verify the submitted credential before replacing or creating the saved record.

## 10.1 Verification requirements

Verification must determine:

1. whether the registry is reachable;
2. whether the username and secret are accepted;
3. whether the credential has the capability required by the registry configuration.

## 10.2 Pull verification

When private pull is required, Regstair verifies access to the administrator-configured verification repository without downloading unnecessary layer content.

## 10.3 Push verification

When push is required, Regstair verifies push authority against the configured verification repository using the least destructive supported operation.

Preferred methods:

1. request the appropriate repository push scope;
2. verify the granted scope when the registry exposes it;
3. initiate and cancel or abandon a blob upload only when needed.

Verification must not publish a permanent manifest or pollute a production namespace.

## 10.4 Save behavior

On successful verification:

1. encrypt the submitted secret;
2. create or atomically replace the credential;
3. update `mtime`;
4. discard the plaintext;
5. record a redacted audit event.

On failure:

- do not save or activate the submitted credential;
- discard the plaintext;
- distinguish invalid credentials, insufficient permission, registry failure, and verification configuration failure;
- return a useful error without exposing secret material.

## 10.5 Removal

Removing a credential:

- requires confirmation;
- deletes the stored encrypted secret;
- prevents future operations that require that user credential;
- does not affect anonymous pulls;
- does not delete cached content;
- records a redacted audit event.

---

# 11. Runtime Credential Selection

For each upstream operation, the configured registry and route determine the credential source:

```text
anonymous
shared Regstair credential
current user's saved credential
```

Selection must be explicit.

Regstair must not try unrelated credentials until one works.

Typical behavior:

```text
Public pull
    -> anonymous upstream access

Private pull using user credentials
    -> current user's verified credential

Push using user credentials
    -> current user's verified credential

Route using a shared registry credential
    -> administrator-configured shared credential
```

A missing or rejected credential must produce a clear authentication or authorization failure. It must not be treated as repository absence.

---

# 12. Cache and Authorization

Regstair remains a caching proxy. User management must not undermine cache performance or content-addressed storage.

Requirements:

- manifests, indexes, configurations, and blobs remain stored by digest;
- physical blobs may be deduplicated across routes and users;
- authorization is checked before serving a private logical reference;
- private content does not become public merely because its digest is cached;
- public cached content remains accessible anonymously when the route permits it;
- authentication work is bypassed for anonymous public pulls;
- cached-pull performance must not materially regress.

Existing cache retention and freshness behavior remain unchanged unless implementation requires a narrowly scoped adjustment.

---

# 13. Push Flow

For a push through Regstair:

1. identify the Regstair user using the selected Docker authentication mechanism;
2. match the global route;
3. confirm that the operation is permitted;
4. select the configured destination registry;
5. select the configured upstream credential source;
6. proxy and validate the OCI upload;
7. publish the artifact;
8. record provenance and request outcome;
9. update cache metadata as appropriate.

The upstream registry credential does not determine Regstair authorization by itself. Regstair must decide whether the local user may perform the push before using the upstream credential.

---

# 14. Failure Semantics

Regstair must distinguish:

- client not authenticated;
- local user disabled;
- operation not permitted;
- approved registry credential missing;
- upstream credential rejected;
- insufficient private-pull permission;
- insufficient push permission;
- upstream registry unavailable;
- upstream authentication service unavailable;
- repository or tag absent;
- fallback prohibited;
- anonymous rate limit reached.

Rules:

```text
404
    Continue only when the matched global route permits fallback.

401 or 403 from an upstream
    Treat as an authentication or authorization failure, not as not-found.

Timeout or 5xx
    Apply the existing source-failure and stale-cache policy.

Missing user credential
    Stop with an actionable credential-required response.
```

A broken private or internal credential must never cause Regstair to retrieve an identically named public image.

---

# 15. Audit and Request Detail

Authenticated operations and credential changes must record enough information to explain what happened without storing secrets.

Record:

- local user;
- operation;
- logical reference;
- matched route;
- source or destination registry;
- upstream credential source;
- upstream username when safe;
- physical repository;
- digest;
- cache result;
- fallback result;
- status;
- timing;
- explanation;
- event time.

Credential values, authorization headers, encrypted payloads, and encryption keys must never appear.

The request-detail UI should join the existing request, route, cache, and provenance information into a single investigation view.

---

# 16. User Interface Requirements

The UI must be rebuilt as a coherent product experience rather than extended as a collection of administrative forms.

The standard is premium consumer-product discipline applied to infrastructure software:

- obvious hierarchy;
- minimal clutter;
- excellent typography and spacing;
- strong light and dark modes;
- fast navigation;
- progressive disclosure;
- clear empty states;
- precise errors;
- restrained motion;
- responsive layouts;
- complete keyboard use;
- WCAG 2.2 AA target.

## 16.1 Information architecture

### User navigation

```text
Overview
Requests
Credentials
Account
Help
```

### Administrator navigation

```text
Overview
Requests
Registries
Routes
Users
Audit
Settings
Help
```

The exact labels may change during design, but the interface should remain small and task-oriented.

## 16.2 Credentials page

Show only approved registries.

Each item shows:

- registry name and host;
- what credentials enable;
- public access behavior;
- configured or not configured;
- verified username when configured;
- `mtime`;
- one clear primary action.

Example:

```text
GitHub Container Registry
ghcr.io

Public images work without credentials.
Credentials enable private pulls and publishing.

Status: Not configured

[Add credentials]
```

## 16.3 Add or replace credential

Use a focused page, sheet, or drawer containing:

- registry identity;
- short provider-specific instruction;
- username;
- password or token;
- reveal control;
- **Verify and Save**;
- live verification progress;
- precise success or failure result.

## 16.4 Registry administration

An administrator can:

- add or edit an approved registry;
- enable or disable it;
- configure supported operations;
- allow or disallow user credentials;
- configure the verification repository;
- inspect health;
- see which routes use it.

## 16.5 User administration

An administrator can:

- create a local user;
- edit user details;
- enable or disable the user;
- reset the local password;
- grant or remove administrator access;
- inspect redacted credential status and recent activity.

## 16.6 Request investigation

A request detail page shows:

- user or anonymous access;
- operation;
- logical reference;
- route;
- cache decision;
- upstream registry;
- credential source;
- fallback;
- physical reference;
- digest;
- provenance;
- timing;
- deterministic explanation.

---

# 17. Security Requirements

- Strong password hashing for local user passwords.
- Authenticated encryption for upstream registry secrets.
- Encryption key stored outside SQLite.
- Secure, HTTP-only web session cookies.
- CSRF protection for browser mutations.
- Authorization checks on every user and administrator API.
- No cross-user credential access.
- No client authorization-header forwarding to upstream hosts.
- No secret material in logs, traces, metrics, responses, audit records, panic output, or support data.
- Atomic credential replacement and deletion.
- Safe handling of concurrent requests during credential replacement.
- Clear session invalidation when a local user is disabled.
- Time-limited, revocable tokens if Docker authentication uses tokens.

---

# 18. API Surface

## User account and credentials

```http
GET    /api/account
PUT    /api/account
GET    /api/account/registries
GET    /api/account/registry-credentials
POST   /api/account/registry-credentials/{registry_id}/verify-and-save
DELETE /api/account/registry-credentials/{registry_id}
```

## Administrator users

```http
GET    /admin/api/users
POST   /admin/api/users
GET    /admin/api/users/{id}
PUT    /admin/api/users/{id}
POST   /admin/api/users/{id}/enable
POST   /admin/api/users/{id}/disable
POST   /admin/api/users/{id}/reset-password
```

## Administrator registries

```http
GET    /admin/api/registries
POST   /admin/api/registries
GET    /admin/api/registries/{id}
PUT    /admin/api/registries/{id}
POST   /admin/api/registries/{id}/verify
```

## Request detail

```http
GET /admin/api/requests/{id}
```

No credential API may return a saved secret.

The precise routes may be adjusted to match current Regstair conventions.

---

# 19. Testing Requirements

## Unit

- local-user password handling;
- enabled and disabled users;
- `ctime` and `mtime`;
- approved-registry visibility;
- credential encryption and decryption;
- credential replacement;
- secret redaction;
- pull and push verification results;
- credential selection;
- authentication failures not treated as not-found.

## Integration

- anonymous public pull;
- cached anonymous pull;
- local web login;
- approved registry listing;
- verified credential save;
- rejected credential save;
- private pull with user credential;
- push with user credential;
- missing credential;
- insufficient push permission;
- shared credential route;
- upstream outage;
- Harbor verification.

## Browser

- user login;
- administrator user management;
- registry configuration;
- credential add, replace, and remove;
- verification progress and errors;
- request detail;
- keyboard navigation;
- focus handling;
- responsive behavior;
- light and dark modes;
- accessibility checks.

## Security

- secret leakage;
- cross-user access;
- unauthorized administration;
- CSRF;
- disabled-user sessions;
- malicious upstream responses;
- concurrent credential replacement;
- token expiry and revocation if tokens are selected.

---

# 20. Implementation Order

## Slice 1 — Local account foundation

- local user schema;
- password hashing;
- administrator user management;
- web login and session handling;
- user/admin access distinction;
- anonymous OCI behavior preserved.

## Slice 2 — Approved registry catalog

- registry administration;
- user-credential enablement;
- allowed operations;
- verification repository;
- user-visible approved registry list.

## Slice 3 — Verified user credentials

- encrypted local credential storage;
- Verify and Save;
- replace and remove;
- private pull and push verification;
- runtime credential selection;
- audit and redaction.

## Slice 4 — Premium UI

- unified information architecture;
- design system;
- user account and credentials experience;
- registry administration;
- user administration;
- request detail;
- responsive and accessibility completion.

## Slice 5 — Docker user authentication

Resolve and implement one of:

- local username and password;
- local username and time-limited Regstair token.

The selected mechanism must work with normal `docker login` behavior.

---

# 21. Immediate Build Slice

The next build slice is:

> Local user accounts, administrator-approved registry credentials, and a polished Verify-and-Save workflow for one real upstream registry.

Use Harbor first because the existing project already contains real Harbor integration.

Acceptance criteria:

1. Existing anonymous public pulls remain unchanged.
2. An administrator can create and disable local users.
3. A user can sign into the Regstair web interface.
4. An administrator can expose Harbor as an approved credential target.
5. The user sees Harbor and no unapproved registries.
6. The user enters only a username and password or token.
7. Regstair verifies the credential against the configured Harbor verification repository.
8. A failed verification saves nothing.
9. A successful verification stores the encrypted credential.
10. The user can replace and remove the credential.
11. Regstair can use the credential for a real configured push.
12. Another user cannot access or use it.
13. The administrator cannot retrieve the secret.
14. `ctime` and `mtime` behave correctly.
15. Logs, APIs, and audit records reveal no secret material.
16. The workflow is polished, responsive, keyboard accessible, and understandable without documentation.

---

# 22. Success Criteria

This phase succeeds when:

- Regstair remains one fast endpoint for all configured registries;
- public pulls remain effortless;
- local users can be managed without external identity infrastructure;
- users see only registries approved by an administrator;
- users enter only the upstream username and password or token;
- credentials are verified before storage;
- private pulls and pushes use the correct credential;
- global routes remain deterministic;
- cached content remains secure and fast;
- every failure is explainable;
- the UI feels like one deliberate product rather than a collection of tools.

---

# 23. Product Definition

> Regstair is a unified OCI registry gateway and caching proxy that gives an organization one secure, fast, and stable endpoint for pulling and publishing container images across every configured internal and external registry.

**Core promise:** One endpoint. Every approved registry. Faster pulls. Predictable publishing. No registry topology exposed to the client.

**Usability promise:** Public content simply works. Private access is configured once. Regstair explains the rest.