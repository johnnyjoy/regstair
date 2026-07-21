package metadata

import (
	"context"
	"fmt"
	"sort"
)

func validateCacheBinding(binding CacheBinding) error {
	if binding.LogicalRepository == "" || binding.Route == "" || binding.Source == "" || binding.PhysicalRepository == "" || binding.ManifestDigest == "" || binding.ObjectDigest == "" {
		return fmt.Errorf("%w: incomplete cache binding", ErrInvalidRecord)
	}
	if binding.ObjectKind != "manifest" && binding.ObjectKind != "blob" {
		return fmt.Errorf("%w: unsupported cache object kind", ErrInvalidRecord)
	}
	if binding.Access != CacheAccessChallenge && binding.Access != CacheAccessProxy && binding.Access != CacheAccessCurrentUserRequired {
		return fmt.Errorf("%w: unsupported cache access", ErrInvalidRecord)
	}
	if (binding.Access == CacheAccessCurrentUserRequired) != (binding.UserID != "") {
		return fmt.Errorf("%w: current-user cache binding requires exactly one user", ErrInvalidRecord)
	}
	return nil
}

func cacheBindingKey(binding CacheBinding) string {
	return binding.LogicalRepository + "\x00" + binding.Route + "\x00" + binding.Source + "\x00" + binding.ManifestDigest + "\x00" + binding.ObjectDigest + "\x00" + string(binding.Access) + "\x00" + binding.UserID
}

func (r *MemoryRepository) RecordCacheBindings(ctx context.Context, bindings []CacheBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := validateCacheBinding(binding); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for _, binding := range bindings {
		key := cacheBindingKey(binding)
		if existing, ok := r.bindings[key]; ok {
			binding.CreatedAt = existing.CreatedAt
			binding.UpdatedAt = nextUpdatedAt(now, existing.UpdatedAt)
		} else {
			binding.CreatedAt, binding.UpdatedAt = now, now
		}
		r.bindings[key] = binding
	}
	return nil
}

func (r *MemoryRepository) ListCacheBindings(ctx context.Context, logicalRepository, route, objectDigest string) ([]CacheBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := []CacheBinding{}
	for _, binding := range r.bindings {
		if binding.LogicalRepository == logicalRepository && binding.Route == route && binding.ObjectDigest == objectDigest {
			result = append(result, binding)
		}
	}
	sort.Slice(result, func(i, j int) bool { return cacheBindingKey(result[i]) < cacheBindingKey(result[j]) })
	return result, nil
}
