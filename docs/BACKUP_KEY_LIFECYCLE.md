# Backup, Restore, and Credential-Key Lifecycle

Status: release procedure
Last exercised: 2026-07-20

## Recovery Set

A complete Regstair recovery set has separate parts with different access controls:

1. **Regstair backup archive:** the entire content root, including SQLite metadata, SQLite WAL/sidecar files if present, OCI blobs, and cached content metadata; plus the authoritative YAML configuration and a versioned backup manifest.
2. **Credential encryption keys:** every key file still referenced by a stored credential envelope. Keys are intentionally never included in the Regstair archive.
3. **Deployment secret values:** shared proxy credentials, legacy client credentials, TLS private keys, and other values referenced by environment/file configuration. The YAML archive contains references, not these external values.
4. **Deployment definition:** Compose overrides, reverse-proxy/TLS configuration, bind address, and key-ID/file mounts.

Losing any content-root data can lose cached content, users, tokens, sessions, audit history, routes' runtime metadata, and stored upstream credentials. Losing the authoritative YAML can change routing and registry identity even if SQLite survives.

## Offline Backup

Stop the Regstair service so SQLite and the content store are not changing. Registry fixtures or external registries do not need to stop.

```bash
docker compose stop regstair
mkdir -p backups
chmod 0777 backups
docker compose run --rm \
  -v "$PWD/backups:/backup" \
  regstair admin backup \
  -content-root /var/lib/regstair/content \
  -config /etc/regstair/regstair.yaml \
  -output /backup/regstair-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
docker compose start regstair
chmod 0700 backups
```

The command refuses to overwrite an archive, refuses symlinks in the content root, writes the archive with mode `0600`, and removes a partial output after failure. Do not place the output inside the content root.

Back up credential key files and deployment secrets separately in an encrypted secret manager or offline encrypted archive. Restrict their readers independently from the Regstair data archive. A database archive and its credential keys stored together provide little protection against offline credential disclosure.

## Restore Drill

Restore only into a new or empty content root and a new configuration path. The command refuses a non-empty destination and unsafe archive paths.

```bash
regstair admin restore \
  -archive /backup/regstair-20260720T150000Z.tar.gz \
  -content-root /restore/content \
  -config-output /restore/config/regstair.yaml
```

Then:

1. Review and install the restored YAML.
2. Restore every referenced credential key under its original key ID.
3. Restore deployment-managed proxy/client/TLS secrets.
4. Start Regstair against the restored content root.
5. Require `GET /readyz` to return `200 {"status":"ready"}`.
6. Test web login, Docker login, one private pull, one push, provenance, and audit visibility.

The archive restores hash-only Docker tokens and opaque sessions as part of SQLite. For a recovery into a less-trusted environment, reset affected passwords or disable users to revoke restored sessions and Docker tokens before network exposure.

## Credential-Key Rotation

Create a backup and a new independent 32-byte key before rotation:

```bash
openssl rand -out regstair-credential-key-2026-08 32
chmod 0600 regstair-credential-key-2026-08
docker compose stop regstair
docker compose run --rm \
  -v "$PWD/regstair-credential-key:/run/secrets/old-key:ro" \
  -v "$PWD/regstair-credential-key-2026-08:/run/secrets/new-key:ro" \
  regstair admin rotate-credential-key \
  -metadata-path /var/lib/regstair/content/metadata/regstair.db \
  -old-key-id primary-2026 \
  -old-key-file /run/secrets/old-key \
  -new-key-id primary-2026-08 \
  -new-key-file /run/secrets/new-key
```

The command decrypts every stored credential before writing anything, creates and verifies every new envelope, then replaces all envelopes and appends `credential.key_rotated` in one SQLite transaction. A missing/wrong old key or changed credential set leaves all ciphertext unchanged.

After the command succeeds, update `REGSTAIR_CREDENTIAL_KEY_ID` and `REGSTAIR_CREDENTIAL_KEY_FILE`, start Regstair, require readiness, and exercise a credentialed operation. Retain the old key in protected backup until a restore drill with the new key succeeds; then remove it from runtime mounts.

## Missing, Wrong, or Lost Keys

- **Missing key:** restored credentials cannot be decrypted; readiness returns `503` with `component: credential_key`.
- **Wrong bytes under the expected key ID:** AEAD authentication fails; readiness returns the same closed classification without revealing record or cryptographic details.
- **Lost key:** there is no recovery mechanism for ciphertext encrypted only by that key. Regstair cannot reconstruct, reset, or export those upstream secrets.

If a key is permanently lost:

1. Preserve the failed recovery set for incident analysis.
2. Generate and configure a new active key.
3. Sign into the local control plane; local password hashes are independent of the credential key.
4. Remove each unavailable upstream credential record.
5. Have each user re-enter and verify their upstream credential.
6. Require readiness and credentialed pull/push tests before restoring network service.

Cache content is not encrypted by the credential key and is not deleted when an upstream credential is removed. Normal authorization still applies before private cached content is served.
