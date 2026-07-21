package metadata

import (
	"context"
	"fmt"
)

func (r *SQLiteRepository) RecordCacheBindings(ctx context.Context, bindings []CacheBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := validateCacheBinding(binding); err != nil {
			return err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cache binding save: %w", err)
	}
	defer tx.Rollback()
	now := r.now()
	for _, binding := range bindings {
		_, err := tx.ExecContext(ctx, `
INSERT INTO cache_bindings (
 logical_repository, route, source, physical_repository, manifest_digest,
 object_digest, object_kind, access, user_id, created_at_nanos, updated_at_nanos
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(logical_repository, route, source, manifest_digest, object_digest, access, user_id) DO UPDATE SET
 physical_repository = excluded.physical_repository,
 object_kind = excluded.object_kind,
 updated_at_nanos = CASE WHEN cache_bindings.updated_at_nanos >= excluded.updated_at_nanos THEN cache_bindings.updated_at_nanos + 1 ELSE excluded.updated_at_nanos END`,
			binding.LogicalRepository, binding.Route, binding.Source, binding.PhysicalRepository, binding.ManifestDigest,
			binding.ObjectDigest, binding.ObjectKind, binding.Access, binding.UserID, timeNanos(now), timeNanos(now))
		if err != nil {
			return fmt.Errorf("save cache binding: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache bindings: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) ListCacheBindings(ctx context.Context, logicalRepository, route, objectDigest string) ([]CacheBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT logical_repository, route, source, physical_repository, manifest_digest,
 object_digest, object_kind, access, user_id, created_at_nanos, updated_at_nanos
FROM cache_bindings
WHERE logical_repository = ? AND route = ? AND object_digest = ?
ORDER BY source, manifest_digest, access, user_id`, logicalRepository, route, objectDigest)
	if err != nil {
		return nil, fmt.Errorf("list cache bindings: %w", err)
	}
	defer rows.Close()
	result := []CacheBinding{}
	for rows.Next() {
		var binding CacheBinding
		var created, updated int64
		if err := rows.Scan(&binding.LogicalRepository, &binding.Route, &binding.Source, &binding.PhysicalRepository, &binding.ManifestDigest, &binding.ObjectDigest, &binding.ObjectKind, &binding.Access, &binding.UserID, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan cache binding: %w", err)
		}
		binding.CreatedAt, binding.UpdatedAt = timeFromNanos(created), timeFromNanos(updated)
		result = append(result, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cache bindings: %w", err)
	}
	return result, nil
}
