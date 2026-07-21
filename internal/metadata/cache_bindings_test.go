package metadata

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCacheBindingsAreRepositoryRouteAndUserBound(t *testing.T) {
	for name, open := range map[string]func(*testing.T) Repository{
		"memory": func(*testing.T) Repository { return NewMemoryRepository() },
		"sqlite": func(t *testing.T) Repository {
			repo, err := NewSQLiteRepository(filepath.Join(t.TempDir(), "metadata.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			return repo
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := open(t)
			binding := CacheBinding{LogicalRepository: "team/private", Route: "team", Source: "harbor", PhysicalRepository: "team/private", ManifestDigest: "sha256:manifest", ObjectDigest: "sha256:shared", ObjectKind: "blob", Access: CacheAccessCurrentUserRequired, UserID: "alice"}
			if err := repo.RecordCacheBindings(context.Background(), []CacheBinding{binding}); err != nil {
				t.Fatal(err)
			}
			got, err := repo.ListCacheBindings(context.Background(), "team/private", "team", "sha256:shared")
			if err != nil || len(got) != 1 || got[0].UserID != "alice" || got[0].CreatedAt.IsZero() || got[0].UpdatedAt.IsZero() {
				t.Fatalf("bindings = %#v, %v", got, err)
			}
			for _, query := range [][3]string{{"other/private", "team", "sha256:shared"}, {"team/private", "other", "sha256:shared"}, {"team/private", "team", "sha256:other"}} {
				found, err := repo.ListCacheBindings(context.Background(), query[0], query[1], query[2])
				if err != nil || len(found) != 0 {
					t.Fatalf("cross-boundary query %v = %#v, %v", query, found, err)
				}
			}
		})
	}
}
