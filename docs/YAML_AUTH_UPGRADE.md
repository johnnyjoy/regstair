# YAML Client Authentication Upgrade

Regstair supports a staged migration from environment-backed YAML `clients` to local users and time-limited Docker tokens. Routes and approved registries remain YAML-owned throughout this migration. No user or credential records are imported from YAML.

## Preconditions

1. Back up the content root, SQLite database, YAML configuration, and deployment-managed secrets.
2. Keep the existing `clients` entries and their environment variables in place.
3. Upgrade Regstair and confirm `/readyz` before changing authentication configuration.
4. Complete first-run setup through `/admin/setup` or the headless setup API.

## Staged Cutover

1. Create each local Regstair user through the admin UI.
2. Have each user create a time-limited Docker token and validate `docker login`, pull, and push while the existing YAML client remains enabled.
3. Configure and Verify-and-Save per-user upstream credentials for any source using `auth.mode: current_user`.
4. Change a source to `current_user` only after every required user has a verified credential and a rollback backup exists.
5. Remove YAML `clients` only after local Docker-token access has been observed successfully.
6. Restart Regstair and confirm removed YAML credentials return `401`, local tokens still work, and intended public pulls remain anonymous.

Configured YAML clients and local Docker tokens intentionally overlap during migration. Configured clients retain their route allowlists; authenticated local users use global route policy. Removing a YAML client does not delete local users, sessions, tokens, per-user credentials, cache data, or request history.

## Rollback

Restore the previous YAML `clients` entries and environment variables, then restart Regstair. Shared proxy credentials remain supported and can be restored independently of local-user records. Do not restore an older SQLite copy unless rolling back the entire data set, because doing so can resurrect revoked sessions or tokens and discard newer credential/audit state.

## Automated Evidence

Run:

```bash
./scripts/upgrade-smoke.sh
```

The suite uses one persistent disposable SQLite volume across both configurations and proves legacy access, overlap, local-token continuity, legacy rejection after removal, and anonymous precedence after cutover.
