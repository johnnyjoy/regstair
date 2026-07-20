package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteRepository struct {
	db  *sql.DB
	now func() time.Time
}

func NewSQLiteRepository(path string) (*SQLiteRepository, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite metadata path is required")
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite metadata repository: %w", err)
	}
	db.SetMaxOpenConns(1)

	repo := &SQLiteRepository{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}
	if err := repo.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *SQLiteRepository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *SQLiteRepository) CreateUser(ctx context.Context, user User) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateUser(user); err != nil {
		return nil, err
	}
	now := r.now()
	_, err := r.db.ExecContext(ctx, `INSERT INTO users (
		id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, user.ID, user.Username, user.PasswordHash, user.DisplayName, user.Email, string(user.Access), boolInt(user.Enabled), timeNanos(now), timeNanos(now))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, fmt.Errorf("%w: user id or username already exists", ErrConflict)
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	user.CreatedAt = now
	user.UpdatedAt = now
	return copyUser(user), nil
}

func (r *SQLiteRepository) BootstrapAdmin(ctx context.Context, user User, audit AuditEvent) (*User, error) {
	if err := validateUser(user); err != nil {
		return nil, err
	}
	if user.Access != UserAccessAdmin || !user.Enabled {
		return nil, fmt.Errorf("%w: bootstrap user must be an enabled administrator", ErrInvalidRecord)
	}
	if err := validateAuditEvent(audit); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin administrator bootstrap: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return nil, fmt.Errorf("count users for administrator bootstrap: %w", err)
	}
	if count != 0 {
		return nil, fmt.Errorf("%w: administrator bootstrap has already completed", ErrConflict)
	}
	now := r.now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, user.ID, user.Username, user.PasswordHash, user.DisplayName, user.Email, string(user.Access), boolInt(user.Enabled), timeNanos(now), timeNanos(now)); err != nil {
		return nil, fmt.Errorf("insert bootstrap administrator: %w", err)
	}
	if _, err := insertAuditEvent(ctx, tx, audit, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit administrator bootstrap: %w", err)
	}
	user.CreatedAt, user.UpdatedAt = now, now
	return copyUser(user), nil
}

func (r *SQLiteRepository) FindUserByID(ctx context.Context, id string) (*User, error) {
	return r.findUser(ctx, "id", id)
}

func (r *SQLiteRepository) FindUserByUsername(ctx context.Context, username string) (*User, error) {
	return r.findUser(ctx, "username", username)
}

func (r *SQLiteRepository) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos FROM users ORDER BY username ASC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, *user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (r *SQLiteRepository) findUser(ctx context.Context, column, value string) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, username, password_hash, display_name, email, access, enabled, created_at_nanos, updated_at_nanos FROM users WHERE `+column+` = ?`, value)
	user, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by %s: %w", column, err)
	}
	return user, nil
}

func (r *SQLiteRepository) UpdateUser(ctx context.Context, user User, expectedUpdatedAt time.Time) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateUser(user); err != nil {
		return nil, err
	}
	now := nextUpdatedAt(r.now(), expectedUpdatedAt)
	result, err := r.db.ExecContext(ctx, `UPDATE users SET username = ?, password_hash = ?, display_name = ?, email = ?, access = ?, enabled = ?, updated_at_nanos = ? WHERE id = ? AND updated_at_nanos = ?`, user.Username, user.PasswordHash, user.DisplayName, user.Email, string(user.Access), boolInt(user.Enabled), timeNanos(now), user.ID, timeNanos(expectedUpdatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, fmt.Errorf("%w: username already exists", ErrConflict)
		}
		return nil, fmt.Errorf("update user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("count updated users: %w", err)
	}
	if changed == 0 {
		current, findErr := r.FindUserByID(ctx, user.ID)
		if findErr != nil {
			return nil, findErr
		}
		if current == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: user %q was modified", ErrConflict, user.ID)
	}
	return r.FindUserByID(ctx, user.ID)
}

func (r *SQLiteRepository) RecordRequestEvent(ctx context.Context, event RequestEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRequestEvent(event); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = r.now()
	}
	explanation, err := json.Marshal(event.Explanation)
	if err != nil {
		return fmt.Errorf("encode request explanation: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
INSERT INTO request_events (
	timestamp_nanos, operation, client_identity, logical_reference, matched_route,
	source_or_destination, status, cache_result, duration_nanos, bytes_transferred,
	error_classification, credential_source, explanation_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		timeNanos(event.Timestamp),
		string(event.Operation),
		event.ClientIdentity,
		event.LogicalReference,
		event.MatchedRoute,
		event.SourceOrDestination,
		string(event.Status),
		string(event.CacheResult),
		int64(event.Duration),
		event.BytesTransferred,
		event.ErrorClassification,
		event.CredentialSource,
		string(explanation),
	)
	if err != nil {
		return fmt.Errorf("insert request event: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) ListRecentRequestEvents(ctx context.Context, limit int) ([]RequestEvent, error) {
	page, err := r.QueryRequestEvents(ctx, RequestEventQuery{Limit: limit})
	return page.Events, err
}

func (r *SQLiteRepository) QueryRequestEvents(ctx context.Context, requestQuery RequestEventQuery) (RequestEventPage, error) {
	if err := ctx.Err(); err != nil {
		return RequestEventPage{}, err
	}
	if requestQuery.Limit < 0 {
		return RequestEventPage{}, fmt.Errorf("%w: negative request event limit", ErrInvalidRecord)
	}

	var query strings.Builder
	query.WriteString(`
SELECT id, timestamp_nanos, operation, client_identity, logical_reference, matched_route,
	source_or_destination, status, cache_result, duration_nanos, bytes_transferred,
	error_classification, credential_source, explanation_json
FROM request_events`)
	conditions := []string{}
	args := []any{}
	addCondition := func(condition string, values ...any) {
		conditions = append(conditions, condition)
		args = append(args, values...)
	}
	filter := requestQuery.Filter
	if filter.ClientIdentity != "" {
		addCondition("client_identity = ?", filter.ClientIdentity)
	}
	if filter.CredentialSource != "" {
		addCondition("credential_source = ?", filter.CredentialSource)
	}
	if filter.Route != "" {
		addCondition("matched_route = ?", filter.Route)
	}
	if filter.Operation != "" {
		addCondition("operation = ?", string(filter.Operation))
	}
	if filter.SourceOrDestination != "" {
		addCondition("source_or_destination = ?", filter.SourceOrDestination)
	}
	if filter.Status != "" {
		addCondition("status = ?", string(filter.Status))
	}
	if filter.CacheResult != "" {
		addCondition("cache_result = ?", string(filter.CacheResult))
	}
	if filter.ErrorClassification != "" {
		addCondition("error_classification = ?", filter.ErrorClassification)
	}
	if filter.ReferenceContains != "" {
		addCondition("instr(logical_reference, ?) > 0", filter.ReferenceContains)
	}
	if !filter.After.IsZero() {
		addCondition("timestamp_nanos >= ?", timeNanos(filter.After))
	}
	if !filter.Before.IsZero() {
		addCondition("timestamp_nanos < ?", timeNanos(filter.Before))
	}
	if requestQuery.Cursor != nil {
		if requestQuery.OldestFirst {
			addCondition("(timestamp_nanos > ? OR (timestamp_nanos = ? AND id > ?))", timeNanos(requestQuery.Cursor.Timestamp), timeNanos(requestQuery.Cursor.Timestamp), requestQuery.Cursor.ID)
		} else {
			addCondition("(timestamp_nanos < ? OR (timestamp_nanos = ? AND id < ?))", timeNanos(requestQuery.Cursor.Timestamp), timeNanos(requestQuery.Cursor.Timestamp), requestQuery.Cursor.ID)
		}
	}
	if len(conditions) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(conditions, " AND "))
	}
	if requestQuery.OldestFirst {
		query.WriteString(" ORDER BY timestamp_nanos ASC, id ASC")
	} else {
		query.WriteString(" ORDER BY timestamp_nanos DESC, id DESC")
	}
	if requestQuery.Limit > 0 {
		query.WriteString(" LIMIT ?")
		args = append(args, requestQuery.Limit+1)
	}

	rows, err := r.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return RequestEventPage{}, fmt.Errorf("query request events: %w", err)
	}
	defer rows.Close()

	events := []RequestEvent{}
	for rows.Next() {
		var event RequestEvent
		var timestampNanos int64
		var operation, status, cacheResult string
		var durationNanos int64
		var explanation string
		if err := rows.Scan(
			&event.ID,
			&timestampNanos,
			&operation,
			&event.ClientIdentity,
			&event.LogicalReference,
			&event.MatchedRoute,
			&event.SourceOrDestination,
			&status,
			&cacheResult,
			&durationNanos,
			&event.BytesTransferred,
			&event.ErrorClassification,
			&event.CredentialSource,
			&explanation,
		); err != nil {
			return RequestEventPage{}, fmt.Errorf("scan request event: %w", err)
		}
		event.Timestamp = timeFromNanos(timestampNanos)
		event.Operation = Operation(operation)
		event.Status = RequestStatus(status)
		event.CacheResult = CacheResult(cacheResult)
		event.Duration = time.Duration(durationNanos)
		if err := json.Unmarshal([]byte(explanation), &event.Explanation); err != nil {
			return RequestEventPage{}, fmt.Errorf("decode request explanation: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return RequestEventPage{}, fmt.Errorf("iterate request events: %w", err)
	}
	hasMore := requestQuery.Limit > 0 && len(events) > requestQuery.Limit
	if hasMore {
		events = events[:requestQuery.Limit]
	}
	page := RequestEventPage{Events: events}
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		page.Next = &RequestEventCursor{Timestamp: last.Timestamp, ID: last.ID}
	}
	return page, nil
}

func (r *SQLiteRepository) SummarizeRequestEvents(ctx context.Context, after time.Time) (RequestEventSummary, error) {
	if err := ctx.Err(); err != nil {
		return RequestEventSummary{}, err
	}
	var summary RequestEventSummary
	err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*),
	COALESCE(SUM(status = 'error'), 0),
	COALESCE(SUM(status = 'denied' AND operation = 'push'), 0),
	COALESCE(SUM(error_classification = 'upstream_authentication_failed'), 0),
	COALESCE(SUM(cache_result = 'hit'), 0),
	COALESCE(SUM(cache_result = 'miss'), 0)
FROM request_events
WHERE timestamp_nanos >= ?`, timeNanos(after)).Scan(&summary.Total, &summary.Errors, &summary.DeniedPushes, &summary.AuthFailures, &summary.CacheHits, &summary.CacheMisses)
	if err != nil {
		return RequestEventSummary{}, fmt.Errorf("summarize request events: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT duration_nanos FROM request_events WHERE timestamp_nanos >= ? AND duration_nanos > 0 ORDER BY duration_nanos`, timeNanos(after))
	if err != nil {
		return RequestEventSummary{}, fmt.Errorf("query request durations: %w", err)
	}
	defer rows.Close()
	durations := make([]time.Duration, 0)
	for rows.Next() {
		var duration int64
		if err := rows.Scan(&duration); err != nil {
			return RequestEventSummary{}, fmt.Errorf("scan request duration: %w", err)
		}
		durations = append(durations, time.Duration(duration))
	}
	if err := rows.Err(); err != nil {
		return RequestEventSummary{}, fmt.Errorf("iterate request durations: %w", err)
	}
	summary.Average, summary.P95 = summarizeDurations(durations)
	return summary, nil
}

func (r *SQLiteRepository) CountRequestEvents(ctx context.Context, filter RequestEventFilter) (int, error) {
	conditions := []string{}
	args := []any{}
	add := func(condition string, values ...any) {
		conditions = append(conditions, condition)
		args = append(args, values...)
	}
	if filter.ClientIdentity != "" {
		add("client_identity = ?", filter.ClientIdentity)
	}
	if filter.CredentialSource != "" {
		add("credential_source = ?", filter.CredentialSource)
	}
	if filter.Route != "" {
		add("matched_route = ?", filter.Route)
	}
	if filter.Operation != "" {
		add("operation = ?", string(filter.Operation))
	}
	if filter.SourceOrDestination != "" {
		add("source_or_destination = ?", filter.SourceOrDestination)
	}
	if filter.Status != "" {
		add("status = ?", string(filter.Status))
	}
	if filter.CacheResult != "" {
		add("cache_result = ?", string(filter.CacheResult))
	}
	if filter.ErrorClassification != "" {
		add("error_classification = ?", filter.ErrorClassification)
	}
	if filter.ReferenceContains != "" {
		add("instr(logical_reference, ?) > 0", filter.ReferenceContains)
	}
	if !filter.After.IsZero() {
		add("timestamp_nanos >= ?", timeNanos(filter.After))
	}
	if !filter.Before.IsZero() {
		add("timestamp_nanos < ?", timeNanos(filter.Before))
	}
	query := "SELECT COUNT(*) FROM request_events"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	var count int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count request events: %w", err)
	}
	return count, nil
}

func (r *SQLiteRepository) FindRequestEventByID(ctx context.Context, id int64) (*RequestEvent, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: invalid request event id", ErrInvalidRecord)
	}
	row := r.db.QueryRowContext(ctx, `
SELECT id, timestamp_nanos, operation, client_identity, logical_reference, matched_route,
	source_or_destination, status, cache_result, duration_nanos, bytes_transferred,
	error_classification, credential_source, explanation_json
FROM request_events WHERE id = ?`, id)
	var event RequestEvent
	var timestampNanos, durationNanos int64
	var operation, status, cacheResult, explanation string
	if err := row.Scan(&event.ID, &timestampNanos, &operation, &event.ClientIdentity, &event.LogicalReference, &event.MatchedRoute, &event.SourceOrDestination, &status, &cacheResult, &durationNanos, &event.BytesTransferred, &event.ErrorClassification, &event.CredentialSource, &explanation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find request event: %w", err)
	}
	event.Timestamp = timeFromNanos(timestampNanos)
	event.Operation, event.Status, event.CacheResult, event.Duration = Operation(operation), RequestStatus(status), CacheResult(cacheResult), time.Duration(durationNanos)
	if err := json.Unmarshal([]byte(explanation), &event.Explanation); err != nil {
		return nil, fmt.Errorf("decode request explanation: %w", err)
	}
	return &event, nil
}

func (r *SQLiteRepository) RecordProvenance(ctx context.Context, record ProvenanceRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateProvenanceRecord(record); err != nil {
		return err
	}
	if record.RetrievedAt.IsZero() {
		record.RetrievedAt = r.now()
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO provenance_records (
	logical_reference, physical_source_reference, requested_reference, resolved_digest,
	source, route, fallback_used, stale_served, retrieved_at_nanos, validated_at_nanos
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.LogicalReference,
		record.PhysicalSourceReference,
		record.RequestedReference,
		record.ResolvedDigest,
		record.Source,
		record.Route,
		boolInt(record.FallbackUsed),
		boolInt(record.StaleServed),
		timeNanos(record.RetrievedAt),
		timeNanos(record.ValidatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert provenance record: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) FindProvenanceByLogicalReference(ctx context.Context, logicalReference string) (*ProvenanceRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	row := r.db.QueryRowContext(ctx, `
SELECT logical_reference, physical_source_reference, requested_reference, resolved_digest,
	source, route, fallback_used, stale_served, retrieved_at_nanos, validated_at_nanos
FROM provenance_records
WHERE logical_reference = ?
ORDER BY retrieved_at_nanos DESC, id DESC
LIMIT 1`, logicalReference)

	record, err := scanProvenance(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find provenance record: %w", err)
	}
	return record, nil
}

func (r *SQLiteRepository) RecordTagMapping(ctx context.Context, mapping TagMapping) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTagMapping(mapping); err != nil {
		return err
	}
	if mapping.ResolvedAt.IsZero() {
		mapping.ResolvedAt = r.now()
	}
	if mapping.LastValidatedAt.IsZero() {
		mapping.LastValidatedAt = mapping.ResolvedAt
	}
	blobDigests, err := json.Marshal(mapping.BlobDigests)
	if err != nil {
		return fmt.Errorf("encode tag mapping blob digests: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
INSERT INTO tag_mappings (
	logical_repository, tag, digest, media_type, size, blob_digests_json,
	source, route, resolved_at_nanos, last_validated_at_nanos, fresh_until_nanos
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(logical_repository, tag) DO UPDATE SET
	digest = excluded.digest,
	media_type = excluded.media_type,
	size = excluded.size,
	blob_digests_json = excluded.blob_digests_json,
	source = excluded.source,
	route = excluded.route,
	resolved_at_nanos = excluded.resolved_at_nanos,
	last_validated_at_nanos = excluded.last_validated_at_nanos,
	fresh_until_nanos = excluded.fresh_until_nanos`,
		mapping.LogicalRepository,
		mapping.Tag,
		mapping.Digest,
		mapping.MediaType,
		mapping.Size,
		string(blobDigests),
		mapping.Source,
		mapping.Route,
		timeNanos(mapping.ResolvedAt),
		timeNanos(mapping.LastValidatedAt),
		timeNanos(mapping.FreshUntil),
	)
	if err != nil {
		return fmt.Errorf("upsert tag mapping: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) FindTagMapping(ctx context.Context, logicalRepository string, tag string) (*TagMapping, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	row := r.db.QueryRowContext(ctx, `
SELECT logical_repository, tag, digest, media_type, size, blob_digests_json,
	source, route, resolved_at_nanos, last_validated_at_nanos, fresh_until_nanos
FROM tag_mappings
WHERE logical_repository = ? AND tag = ?`, logicalRepository, tag)

	mapping, err := scanTagMapping(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find tag mapping: %w", err)
	}
	return mapping, nil
}

func (r *SQLiteRepository) ListTagMappings(ctx context.Context) ([]TagMapping, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `
SELECT logical_repository, tag, digest, media_type, size, blob_digests_json,
	source, route, resolved_at_nanos, last_validated_at_nanos, fresh_until_nanos
FROM tag_mappings
ORDER BY logical_repository ASC, tag ASC`)
	if err != nil {
		return nil, fmt.Errorf("list tag mappings: %w", err)
	}
	defer rows.Close()

	mappings := []TagMapping{}
	for rows.Next() {
		mapping, err := scanTagMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tag mapping: %w", err)
		}
		mappings = append(mappings, *mapping)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tag mappings: %w", err)
	}
	return mappings, nil
}

func (r *SQLiteRepository) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS request_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp_nanos INTEGER NOT NULL,
			operation TEXT NOT NULL,
			client_identity TEXT NOT NULL DEFAULT '',
			logical_reference TEXT NOT NULL,
			matched_route TEXT NOT NULL DEFAULT '',
			source_or_destination TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			cache_result TEXT NOT NULL DEFAULT '',
			duration_nanos INTEGER NOT NULL DEFAULT 0,
			bytes_transferred INTEGER NOT NULL DEFAULT 0,
			error_classification TEXT NOT NULL DEFAULT '',
			credential_source TEXT NOT NULL DEFAULT '',
			explanation_json TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_request_events_timestamp ON request_events(timestamp_nanos DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			access TEXT NOT NULL CHECK(access IN ('user', 'admin')),
			enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
			created_at_nanos INTEGER NOT NULL,
			updated_at_nanos INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS docker_tokens (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			label TEXT NOT NULL DEFAULT '',
			token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash) = 32),
			created_at_nanos INTEGER NOT NULL,
			expires_at_nanos INTEGER NOT NULL,
			revoked_at_nanos INTEGER NOT NULL DEFAULT 0,
			last_used_at_nanos INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS web_sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash) = 32),
			csrf_token_hash BLOB NOT NULL CHECK(length(csrf_token_hash) = 32),
			created_at_nanos INTEGER NOT NULL,
			last_seen_at_nanos INTEGER NOT NULL,
			idle_expires_at_nanos INTEGER NOT NULL,
			absolute_expires_at_nanos INTEGER NOT NULL,
			revoked_at_nanos INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS registry_credentials (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			source_id TEXT NOT NULL,
			username TEXT NOT NULL,
			encrypted_secret TEXT NOT NULL,
			created_at_nanos INTEGER NOT NULL,
			updated_at_nanos INTEGER NOT NULL,
			UNIQUE(user_id, source_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp_nanos INTEGER NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			outcome TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			remote_address TEXT NOT NULL DEFAULT '',
			details_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_timestamp ON audit_events(timestamp_nanos DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS provenance_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			logical_reference TEXT NOT NULL,
			physical_source_reference TEXT NOT NULL DEFAULT '',
			requested_reference TEXT NOT NULL,
			resolved_digest TEXT NOT NULL,
			source TEXT NOT NULL,
			route TEXT NOT NULL,
			fallback_used INTEGER NOT NULL DEFAULT 0,
			stale_served INTEGER NOT NULL DEFAULT 0,
			retrieved_at_nanos INTEGER NOT NULL,
			validated_at_nanos INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_provenance_logical_reference ON provenance_records(logical_reference, retrieved_at_nanos DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS tag_mappings (
			logical_repository TEXT NOT NULL,
			tag TEXT NOT NULL,
			digest TEXT NOT NULL,
			media_type TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			blob_digests_json TEXT NOT NULL DEFAULT '[]',
			source TEXT NOT NULL,
			route TEXT NOT NULL,
			resolved_at_nanos INTEGER NOT NULL,
			last_validated_at_nanos INTEGER NOT NULL,
			fresh_until_nanos INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(logical_repository, tag)
		)`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at_nanos INTEGER NOT NULL
		)`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at_nanos) VALUES (1, unixepoch('subsec') * 1000000000)`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate sqlite metadata repository: %w", err)
		}
	}
	if err := r.ensureRequestEventCredentialSource(ctx); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ensureRequestEventCredentialSource(ctx context.Context) error {
	rows, err := r.db.QueryContext(ctx, `PRAGMA table_info(request_events)`)
	if err != nil {
		return fmt.Errorf("inspect request event schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan request event schema: %w", err)
		}
		if name == "credential_source" {
			return nil
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `ALTER TABLE request_events ADD COLUMN credential_source TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add request event credential source: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*User, error) {
	var user User
	var access string
	var enabled int
	var createdAtNanos, updatedAtNanos int64
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName, &user.Email, &access, &enabled, &createdAtNanos, &updatedAtNanos); err != nil {
		return nil, err
	}
	user.Access = UserAccess(access)
	user.Enabled = enabled != 0
	user.CreatedAt = timeFromNanos(createdAtNanos)
	user.UpdatedAt = timeFromNanos(updatedAtNanos)
	return &user, nil
}

func scanProvenance(row scanner) (*ProvenanceRecord, error) {
	var record ProvenanceRecord
	var fallbackUsed, staleServed int
	var retrievedAtNanos, validatedAtNanos int64
	if err := row.Scan(
		&record.LogicalReference,
		&record.PhysicalSourceReference,
		&record.RequestedReference,
		&record.ResolvedDigest,
		&record.Source,
		&record.Route,
		&fallbackUsed,
		&staleServed,
		&retrievedAtNanos,
		&validatedAtNanos,
	); err != nil {
		return nil, err
	}
	record.FallbackUsed = fallbackUsed != 0
	record.StaleServed = staleServed != 0
	record.RetrievedAt = timeFromNanos(retrievedAtNanos)
	record.ValidatedAt = timeFromNanos(validatedAtNanos)
	return &record, nil
}

func scanTagMapping(row scanner) (*TagMapping, error) {
	var mapping TagMapping
	var blobDigests string
	var resolvedAtNanos, lastValidatedAtNanos, freshUntilNanos int64
	if err := row.Scan(
		&mapping.LogicalRepository,
		&mapping.Tag,
		&mapping.Digest,
		&mapping.MediaType,
		&mapping.Size,
		&blobDigests,
		&mapping.Source,
		&mapping.Route,
		&resolvedAtNanos,
		&lastValidatedAtNanos,
		&freshUntilNanos,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(blobDigests), &mapping.BlobDigests); err != nil {
		return nil, err
	}
	mapping.ResolvedAt = timeFromNanos(resolvedAtNanos)
	mapping.LastValidatedAt = timeFromNanos(lastValidatedAtNanos)
	mapping.FreshUntil = timeFromNanos(freshUntilNanos)
	return &mapping, nil
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sqlite metadata directory: %w", err)
	}
	return nil
}

func timeNanos(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func timeFromNanos(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

var _ Repository = (*SQLiteRepository)(nil)
