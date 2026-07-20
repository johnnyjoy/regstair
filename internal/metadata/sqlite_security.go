package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (r *SQLiteRepository) CreateUserWithAudit(ctx context.Context, user User, audit AuditEvent) (*User, error) {
	if err := validateUser(user); err != nil {
		return nil, err
	}
	if err := validateAuditEvent(audit); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin user creation: %w", err)
	}
	defer tx.Rollback()
	if err := requireEnabledAdmin(ctx, tx, audit.ActorUserID); err != nil {
		return nil, err
	}
	now := r.now()
	_, err = tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, user.ID, user.Username, user.PasswordHash, user.DisplayName, user.Email, string(user.Access), boolInt(user.Enabled), timeNanos(now), timeNanos(now))
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	if _, err := insertAuditEvent(ctx, tx, audit, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user creation: %w", err)
	}
	user.CreatedAt, user.UpdatedAt = now, now
	return copyUser(user), nil
}

func (r *SQLiteRepository) UpdateUserSecurity(ctx context.Context, user User, expectedUpdatedAt time.Time, invalidateIdentity bool, audit AuditEvent) (*User, error) {
	if err := validateUser(user); err != nil {
		return nil, err
	}
	if err := validateAuditEvent(audit); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin user security update: %w", err)
	}
	defer tx.Rollback()
	if err := requireEnabledAdmin(ctx, tx, audit.ActorUserID); err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `SELECT id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos FROM users WHERE id = ?`, user.ID)
	current, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user for security update: %w", err)
	}
	if !current.UpdatedAt.Equal(expectedUpdatedAt) {
		return nil, fmt.Errorf("%w: user %q was modified", ErrConflict, user.ID)
	}
	if current.Enabled && current.Access == UserAccessAdmin && (!user.Enabled || user.Access != UserAccessAdmin) {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE enabled = 1 AND access = 'admin'`).Scan(&admins); err != nil {
			return nil, err
		}
		if admins <= 1 {
			return nil, fmt.Errorf("%w: cannot disable or demote the last enabled administrator", ErrConflict)
		}
	}
	now := nextUpdatedAt(r.now(), current.UpdatedAt)
	result, err := tx.ExecContext(ctx, `UPDATE users SET username=?, password_hash=?, display_name=?, email=?, access=?, enabled=?, updated_at_nanos=? WHERE id=? AND updated_at_nanos=?`, user.Username, user.PasswordHash, user.DisplayName, user.Email, string(user.Access), boolInt(user.Enabled), timeNanos(now), user.ID, timeNanos(expectedUpdatedAt))
	if err != nil {
		return nil, fmt.Errorf("update user security: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return nil, fmt.Errorf("%w: user %q was modified", ErrConflict, user.ID)
	}
	if invalidateIdentity {
		if _, err := tx.ExecContext(ctx, `UPDATE web_sessions SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE user_id = ?`, timeNanos(now), user.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE docker_tokens SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE user_id = ?`, timeNanos(now), user.ID); err != nil {
			return nil, err
		}
	}
	if _, err := insertAuditEvent(ctx, tx, audit, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user security update: %w", err)
	}
	user.CreatedAt, user.UpdatedAt = current.CreatedAt, now
	return copyUser(user), nil
}

func requireEnabledAdmin(ctx context.Context, tx *sql.Tx, userID string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ? AND enabled = 1 AND access = 'admin'`, userID).Scan(&count); err != nil {
		return fmt.Errorf("authorize administrator transaction: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("%w: acting administrator is not enabled", ErrConflict)
	}
	return nil
}

func (r *SQLiteRepository) ChangeUserPassword(ctx context.Context, userID string, expectedUpdatedAt time.Time, passwordHash string, audit AuditEvent) (*User, error) {
	systemRecovery := audit.ActorUserID == "" && audit.ActorRole == "system" && audit.Action == "user.password_recovered"
	if passwordHash == "" || (!systemRecovery && (audit.ActorUserID != userID || audit.ActorRole != "user")) {
		return nil, fmt.Errorf("%w: invalid password change", ErrInvalidRecord)
	}
	if err := validateAuditEvent(audit); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()
	query := `SELECT id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos FROM users WHERE id = ? AND enabled = 1`
	if systemRecovery {
		query = `SELECT id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos FROM users WHERE id = ?`
	}
	row := tx.QueryRowContext(ctx, query, userID)
	user, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !user.UpdatedAt.Equal(expectedUpdatedAt) {
		return nil, fmt.Errorf("%w: user %q was modified", ErrConflict, userID)
	}
	now := nextUpdatedAt(r.now(), user.UpdatedAt)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at_nanos = ? WHERE id = ? AND updated_at_nanos = ?`, passwordHash, timeNanos(now), userID, timeNanos(expectedUpdatedAt)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE web_sessions SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE user_id = ?`, timeNanos(now), userID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE docker_tokens SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE user_id = ?`, timeNanos(now), userID); err != nil {
		return nil, err
	}
	if _, err := insertAuditEvent(ctx, tx, audit, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit password change: %w", err)
	}
	user.PasswordHash, user.UpdatedAt = passwordHash, now
	return user, nil
}

func (r *SQLiteRepository) CreateDockerToken(ctx context.Context, token DockerToken) (*DockerToken, error) {
	if err := validateDockerToken(token); err != nil {
		return nil, err
	}
	if token.CreatedAt.IsZero() {
		token.CreatedAt = r.now()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO docker_tokens (id, user_id, label, token_hash, created_at_nanos, expires_at_nanos, revoked_at_nanos, last_used_at_nanos) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, token.ID, token.UserID, token.Label, token.TokenHash, timeNanos(token.CreatedAt), timeNanos(token.ExpiresAt), timeNanos(token.RevokedAt), timeNanos(token.LastUsed))
	if err != nil {
		return nil, fmt.Errorf("insert docker token: %w", err)
	}
	copy := token
	copy.TokenHash = append([]byte(nil), token.TokenHash...)
	return &copy, nil
}

func (r *SQLiteRepository) CreateDockerTokenWithAudit(ctx context.Context, token DockerToken, audit AuditEvent) (*DockerToken, error) {
	if err := validateDockerToken(token); err != nil {
		return nil, err
	}
	if err := validateAuditEvent(audit); err != nil {
		return nil, err
	}
	if audit.ActorUserID != token.UserID {
		return nil, fmt.Errorf("%w: token actor must own token", ErrInvalidRecord)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin docker token creation: %w", err)
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM users WHERE id = ?`, token.UserID).Scan(&enabled); err != nil || enabled != 1 {
		return nil, fmt.Errorf("%w: token user is not enabled", ErrConflict)
	}
	if token.CreatedAt.IsZero() {
		token.CreatedAt = r.now()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO docker_tokens (id, user_id, label, token_hash, created_at_nanos, expires_at_nanos, revoked_at_nanos, last_used_at_nanos) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, token.ID, token.UserID, token.Label, token.TokenHash, timeNanos(token.CreatedAt), timeNanos(token.ExpiresAt), 0, 0); err != nil {
		return nil, fmt.Errorf("insert docker token: %w", err)
	}
	if _, err := insertAuditEvent(ctx, tx, audit, token.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit docker token creation: %w", err)
	}
	copy := token
	copy.TokenHash = append([]byte(nil), token.TokenHash...)
	return &copy, nil
}

func (r *SQLiteRepository) FindDockerTokenByHash(ctx context.Context, hash []byte) (*DockerToken, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, user_id, label, token_hash, created_at_nanos, expires_at_nanos, revoked_at_nanos, last_used_at_nanos FROM docker_tokens WHERE token_hash = ?`, hash)
	var token DockerToken
	var created, expires, revoked, lastUsed int64
	if err := row.Scan(&token.ID, &token.UserID, &token.Label, &token.TokenHash, &created, &expires, &revoked, &lastUsed); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("find docker token: %w", err)
	}
	token.CreatedAt, token.ExpiresAt, token.RevokedAt, token.LastUsed = timeFromNanos(created), timeFromNanos(expires), timeFromNanos(revoked), timeFromNanos(lastUsed)
	return &token, nil
}

func (r *SQLiteRepository) ListDockerTokensForUser(ctx context.Context, userID string) ([]DockerToken, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, user_id, label, created_at_nanos, expires_at_nanos, revoked_at_nanos, last_used_at_nanos FROM docker_tokens WHERE user_id = ? ORDER BY created_at_nanos DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list docker tokens: %w", err)
	}
	defer rows.Close()
	tokens := []DockerToken{}
	for rows.Next() {
		var token DockerToken
		var created, expires, revoked, used int64
		if err := rows.Scan(&token.ID, &token.UserID, &token.Label, &created, &expires, &revoked, &used); err != nil {
			return nil, err
		}
		token.CreatedAt, token.ExpiresAt, token.RevokedAt, token.LastUsed = timeFromNanos(created), timeFromNanos(expires), timeFromNanos(revoked), timeFromNanos(used)
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (r *SQLiteRepository) RevokeDockerToken(ctx context.Context, id string, revokedAt time.Time) error {
	if revokedAt.IsZero() {
		revokedAt = r.now()
	}
	_, err := r.db.ExecContext(ctx, `UPDATE docker_tokens SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE id = ?`, timeNanos(revokedAt), id)
	if err != nil {
		return fmt.Errorf("revoke docker token: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) RevokeDockerTokenWithAudit(ctx context.Context, userID, id string, revokedAt time.Time, audit AuditEvent) error {
	if err := validateAuditEvent(audit); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE docker_tokens SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE id = ? AND user_id = ?`, timeNanos(revokedAt), id, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil
	}
	if _, err := insertAuditEvent(ctx, tx, audit, revokedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteRepository) CreateWebSession(ctx context.Context, session WebSession) (*WebSession, error) {
	if err := validateWebSession(session); err != nil {
		return nil, err
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = r.now()
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = session.CreatedAt
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO web_sessions (id, user_id, token_hash, csrf_token_hash, created_at_nanos, last_seen_at_nanos, idle_expires_at_nanos, absolute_expires_at_nanos, revoked_at_nanos) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, session.ID, session.UserID, session.TokenHash, session.CSRFTokenHash, timeNanos(session.CreatedAt), timeNanos(session.LastSeenAt), timeNanos(session.IdleExpiresAt), timeNanos(session.AbsoluteExpiresAt), timeNanos(session.RevokedAt))
	if err != nil {
		return nil, fmt.Errorf("insert web session: %w", err)
	}
	copy := session
	copy.TokenHash = append([]byte(nil), session.TokenHash...)
	copy.CSRFTokenHash = append([]byte(nil), session.CSRFTokenHash...)
	return &copy, nil
}

func (r *SQLiteRepository) FindWebSessionByHash(ctx context.Context, hash []byte) (*WebSession, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, user_id, token_hash, csrf_token_hash, created_at_nanos, last_seen_at_nanos, idle_expires_at_nanos, absolute_expires_at_nanos, revoked_at_nanos FROM web_sessions WHERE token_hash = ?`, hash)
	var session WebSession
	var created, lastSeen, idleExpires, absoluteExpires, revoked int64
	if err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFTokenHash, &created, &lastSeen, &idleExpires, &absoluteExpires, &revoked); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("find web session: %w", err)
	}
	session.CreatedAt, session.LastSeenAt = timeFromNanos(created), timeFromNanos(lastSeen)
	session.IdleExpiresAt, session.AbsoluteExpiresAt, session.RevokedAt = timeFromNanos(idleExpires), timeFromNanos(absoluteExpires), timeFromNanos(revoked)
	return &session, nil
}

func (r *SQLiteRepository) RevokeWebSession(ctx context.Context, id string, revokedAt time.Time) error {
	if revokedAt.IsZero() {
		revokedAt = r.now()
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE web_sessions SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE id = ?`, timeNanos(revokedAt), id); err != nil {
		return fmt.Errorf("revoke web session: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) RevokeWebSessionsForUser(ctx context.Context, userID string, revokedAt time.Time) error {
	if revokedAt.IsZero() {
		revokedAt = r.now()
	}
	_, err := r.db.ExecContext(ctx, `UPDATE web_sessions SET revoked_at_nanos = CASE WHEN revoked_at_nanos = 0 THEN ? ELSE revoked_at_nanos END WHERE user_id = ?`, timeNanos(revokedAt), userID)
	if err != nil {
		return fmt.Errorf("revoke web sessions: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) FindRegistryCredential(ctx context.Context, userID, sourceID string) (*RegistryCredential, error) {
	return findRegistryCredential(ctx, r.db, userID, sourceID)
}

func (r *SQLiteRepository) ListRegistryCredentialsForUser(ctx context.Context, userID string) ([]RegistryCredential, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, user_id, source_id, username, encrypted_secret, created_at_nanos, updated_at_nanos FROM registry_credentials WHERE user_id = ? ORDER BY source_id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list registry credentials: %w", err)
	}
	defer rows.Close()
	credentials := []RegistryCredential{}
	for rows.Next() {
		var credential RegistryCredential
		var created, updated int64
		if err := rows.Scan(&credential.ID, &credential.UserID, &credential.SourceID, &credential.Username, &credential.EncryptedSecret, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan registry credential: %w", err)
		}
		credential.CreatedAt, credential.UpdatedAt = timeFromNanos(created), timeFromNanos(updated)
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registry credentials: %w", err)
	}
	return credentials, nil
}

func findRegistryCredential(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID, sourceID string) (*RegistryCredential, error) {
	row := q.QueryRowContext(ctx, `SELECT id, user_id, source_id, username, encrypted_secret, created_at_nanos, updated_at_nanos FROM registry_credentials WHERE user_id = ? AND source_id = ?`, userID, sourceID)
	var credential RegistryCredential
	var created, updated int64
	if err := row.Scan(&credential.ID, &credential.UserID, &credential.SourceID, &credential.Username, &credential.EncryptedSecret, &created, &updated); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("find registry credential: %w", err)
	}
	credential.CreatedAt, credential.UpdatedAt = timeFromNanos(created), timeFromNanos(updated)
	return &credential, nil
}

func (r *SQLiteRepository) SaveRegistryCredential(ctx context.Context, credential RegistryCredential, audit AuditEvent) (*RegistryCredential, error) {
	if err := validateRegistryCredential(credential); err != nil {
		return nil, err
	}
	if err := validateAuditEvent(audit); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin credential save: %w", err)
	}
	defer tx.Rollback()
	existing, err := findRegistryCredential(ctx, tx, credential.UserID, credential.SourceID)
	if err != nil {
		return nil, err
	}
	now := r.now()
	if existing == nil {
		credential.CreatedAt, credential.UpdatedAt = now, now
		_, err = tx.ExecContext(ctx, `INSERT INTO registry_credentials (id, user_id, source_id, username, encrypted_secret, created_at_nanos, updated_at_nanos) VALUES (?, ?, ?, ?, ?, ?, ?)`, credential.ID, credential.UserID, credential.SourceID, credential.Username, credential.EncryptedSecret, timeNanos(now), timeNanos(now))
	} else {
		if existing.ID != credential.ID {
			return nil, fmt.Errorf("%w: credential id does not match existing user/source record", ErrConflict)
		}
		credential.CreatedAt = existing.CreatedAt
		credential.UpdatedAt = nextUpdatedAt(now, existing.UpdatedAt)
		_, err = tx.ExecContext(ctx, `UPDATE registry_credentials SET username = ?, encrypted_secret = ?, updated_at_nanos = ? WHERE id = ?`, credential.Username, credential.EncryptedSecret, timeNanos(credential.UpdatedAt), credential.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("save registry credential: %w", err)
	}
	if _, err := insertAuditEvent(ctx, tx, audit, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit credential save: %w", err)
	}
	return &credential, nil
}

func (r *SQLiteRepository) DeleteRegistryCredential(ctx context.Context, userID, sourceID string, audit AuditEvent) error {
	if err := validateAuditEvent(audit); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential deletion: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM registry_credentials WHERE user_id = ? AND source_id = ?`, userID, sourceID); err != nil {
		return fmt.Errorf("delete registry credential: %w", err)
	}
	if _, err := insertAuditEvent(ctx, tx, audit, r.now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit credential deletion: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) RotateRegistryCredentialSecrets(ctx context.Context, replacements map[string]string, audit AuditEvent) (int, error) {
	if err := validateAuditEvent(audit); err != nil {
		return 0, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin credential key rotation: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM registry_credentials ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("list credentials for key rotation: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan credential for key rotation: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate credentials for key rotation: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close credential key rotation query: %w", err)
	}
	if len(ids) != len(replacements) {
		return 0, fmt.Errorf("%w: credential key rotation set changed", ErrConflict)
	}
	for _, id := range ids {
		encrypted, ok := replacements[id]
		if !ok || encrypted == "" {
			return 0, fmt.Errorf("%w: credential key rotation is incomplete", ErrInvalidRecord)
		}
		result, err := tx.ExecContext(ctx, `UPDATE registry_credentials SET encrypted_secret = ? WHERE id = ?`, encrypted, id)
		if err != nil {
			return 0, fmt.Errorf("rotate credential key: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return 0, fmt.Errorf("%w: credential key rotation set changed", ErrConflict)
		}
	}
	if _, err := insertAuditEvent(ctx, tx, audit, r.now()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit credential key rotation: %w", err)
	}
	return len(ids), nil
}

func (r *SQLiteRepository) RecordAuditEvent(ctx context.Context, event AuditEvent) (*AuditEvent, error) {
	if err := validateAuditEvent(event); err != nil {
		return nil, err
	}
	return insertAuditEvent(ctx, r.db, event, r.now())
}

type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAuditEvent(ctx context.Context, executor auditExecutor, event AuditEvent, fallbackTime time.Time) (*AuditEvent, error) {
	if event.Timestamp.IsZero() {
		event.Timestamp = fallbackTime
	}
	details, err := json.Marshal(event.Details)
	if err != nil {
		return nil, fmt.Errorf("encode audit details: %w", err)
	}
	result, err := executor.ExecContext(ctx, `INSERT INTO audit_events (timestamp_nanos, actor_user_id, actor_role, action, target_type, target_id, outcome, correlation_id, remote_address, details_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, timeNanos(event.Timestamp), event.ActorUserID, event.ActorRole, event.Action, event.TargetType, event.TargetID, event.Outcome, event.CorrelationID, event.RemoteAddress, string(details))
	if err != nil {
		return nil, fmt.Errorf("insert audit event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read audit event id: %w", err)
	}
	return &event, nil
}

func (r *SQLiteRepository) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: audit limit must be between 1 and 100", ErrInvalidRecord)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, timestamp_nanos, actor_user_id, actor_role, action, target_type, target_id, outcome, correlation_id, remote_address, details_json FROM audit_events ORDER BY timestamp_nanos DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var timestamp int64
		var details string
		if err := rows.Scan(&event.ID, &timestamp, &event.ActorUserID, &event.ActorRole, &event.Action, &event.TargetType, &event.TargetID, &event.Outcome, &event.CorrelationID, &event.RemoteAddress, &details); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.Timestamp = timeFromNanos(timestamp)
		if err := json.Unmarshal([]byte(details), &event.Details); err != nil {
			return nil, fmt.Errorf("decode audit details: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

var _ SecurityRepository = (*SQLiteRepository)(nil)
