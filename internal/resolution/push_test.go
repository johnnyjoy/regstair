package resolution

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"regstair/internal/metadata"
	"regstair/internal/policy"
	"regstair/internal/registry"
)

func TestPushResolverPublishesToPolicyDestination(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "harbor-team-a"}},
		Routes: []policy.Route{
			{
				Name:            "team-a-publish",
				Match:           "team-a/**",
				Precedence:      10,
				PushDestination: "harbor-team-a",
				Rewrite: policy.Rewrite{
					StripPrefix: "team-a/",
					AddPrefix:   "production-team-a/",
				},
			},
		},
	})
	store := newTestStore(t)
	repo := metadata.NewMemoryRepository()
	destination := registry.NewFakeConnector("harbor-team-a")
	if _, err := store.PutBlob(context.Background(), resolverBlobDigest, bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}

	resolver := NewPushResolver(engine, store, repo, map[string]registry.Connector{
		"harbor-team-a": destination,
	})

	result, err := resolver.Push(context.Background(), PushRequest{
		Repository:     "team-a/service",
		Reference:      "4.1",
		Manifest:       testManifest(),
		ClientIdentity: "ci-builder",
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	if got, want := result.Destination, "harbor-team-a"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
	if got, want := result.PhysicalRepository, "production-team-a/service"; got != want {
		t.Fatalf("physical repository = %q, want %q", got, want)
	}
	if got, want := result.ManifestDigest, resolverManifestDigest; got != want {
		t.Fatalf("manifest digest = %q, want %q", got, want)
	}

	published, err := destination.ResolveManifest(context.Background(), "production-team-a/service", "4.1")
	if err != nil {
		t.Fatalf("ResolveManifest() error = %v", err)
	}
	if got, want := published.Digest, resolverManifestDigest; got != want {
		t.Fatalf("published digest = %q, want %q", got, want)
	}

	provenance, err := repo.FindProvenanceByLogicalReference(context.Background(), "team-a/service:4.1")
	if err != nil {
		t.Fatalf("FindProvenanceByLogicalReference() error = %v", err)
	}
	if provenance == nil {
		t.Fatal("provenance = nil, want record")
	}
	if got, want := provenance.Source, "harbor-team-a"; got != want {
		t.Fatalf("provenance destination source = %q, want %q", got, want)
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].Operation, metadata.OperationPush; got != want {
		t.Fatalf("event operation = %q, want %q", got, want)
	}
	if got, want := events[0].Status, metadata.StatusSuccess; got != want {
		t.Fatalf("event status = %q, want %q", got, want)
	}
	if got, want := events[0].ClientIdentity, "ci-builder"; got != want {
		t.Fatalf("event client identity = %q, want %q", got, want)
	}
}

func TestPushResolverRejectsDeniedRoute(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Routes: []policy.Route{
			{Name: "github", Match: "github/**", Precedence: 10, PushDenied: true},
		},
	})
	repo := metadata.NewMemoryRepository()
	resolver := NewPushResolver(engine, newTestStore(t), repo, map[string]registry.Connector{})

	_, err := resolver.Push(context.Background(), PushRequest{
		Repository: "github/org/image",
		Reference:  "latest",
		Manifest:   testManifest(),
	})
	if !errors.Is(err, policy.ErrPushDenied) {
		t.Fatalf("Push() error = %v, want policy.ErrPushDenied", err)
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].Status, metadata.StatusDenied; got != want {
		t.Fatalf("event status = %q, want %q", got, want)
	}
}

func TestPushResolverDeniesUnauthorizedRoute(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "harbor-team-a"}},
		Routes: []policy.Route{
			{Name: "team-a-publish", Match: "team-a/**", Precedence: 10, PushDestination: "harbor-team-a"},
		},
	})
	repo := metadata.NewMemoryRepository()
	resolver := NewPushResolver(
		engine,
		newTestStore(t),
		repo,
		map[string]registry.Connector{"harbor-team-a": registry.NewFakeConnector("harbor-team-a")},
		WithAuthorizer(staticAuthorizer{allowed: false}),
	)

	_, err := resolver.Push(context.Background(), PushRequest{
		Repository:     "team-a/service",
		Reference:      "4.1",
		Manifest:       testManifest(),
		ClientIdentity: "ci-builder",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Push() error = %v, want ErrUnauthorized", err)
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].Status, metadata.StatusDenied; got != want {
		t.Fatalf("event status = %q, want %q", got, want)
	}
	if got, want := events[0].ErrorClassification, "authorization_denied"; got != want {
		t.Fatalf("event error class = %q, want %q", got, want)
	}
	if got, want := events[0].ClientIdentity, "ci-builder"; got != want {
		t.Fatalf("event client identity = %q, want %q", got, want)
	}
}

func TestPushResolverRejectsMissingStagedBlob(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "harbor-team-a"}},
		Routes: []policy.Route{
			{Name: "team-a", Match: "team-a/**", Precedence: 10, PushDestination: "harbor-team-a"},
		},
	})
	resolver := NewPushResolver(engine, newTestStore(t), metadata.NewMemoryRepository(), map[string]registry.Connector{
		"harbor-team-a": registry.NewFakeConnector("harbor-team-a"),
	})

	_, err := resolver.Push(context.Background(), PushRequest{
		Repository: "team-a/service",
		Reference:  "4.1",
		Manifest:   testManifest(),
	})
	if !errors.Is(err, ErrStagedBlobMissing) {
		t.Fatalf("Push() error = %v, want ErrStagedBlobMissing", err)
	}
}

func TestPushResolverReturnsUnavailableForMissingDestinationConnector(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "harbor-team-a"}},
		Routes: []policy.Route{
			{Name: "team-a", Match: "team-a/**", Precedence: 10, PushDestination: "harbor-team-a"},
		},
	})
	store := newTestStore(t)
	if _, err := store.PutBlob(context.Background(), resolverBlobDigest, bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}
	resolver := NewPushResolver(engine, store, metadata.NewMemoryRepository(), map[string]registry.Connector{})

	_, err := resolver.Push(context.Background(), PushRequest{
		Repository: "team-a/service",
		Reference:  "4.1",
		Manifest:   testManifest(),
	})
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("Push() error = %v, want ErrSourceUnavailable", err)
	}
}

func TestPushResolverRejectsManifestDigestMismatch(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "harbor-team-a"}},
		Routes: []policy.Route{
			{Name: "team-a", Match: "team-a/**", Precedence: 10, PushDestination: "harbor-team-a"},
		},
	})
	store := newTestStore(t)
	if _, err := store.PutBlob(context.Background(), resolverBlobDigest, bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}
	manifest := testManifest()
	manifest.Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	resolver := NewPushResolver(engine, store, metadata.NewMemoryRepository(), map[string]registry.Connector{
		"harbor-team-a": registry.NewFakeConnector("harbor-team-a"),
	})

	_, err := resolver.Push(context.Background(), PushRequest{
		Repository: "team-a/service",
		Reference:  "4.1",
		Manifest:   manifest,
	})
	if !errors.Is(err, ErrManifestDigestMismatch) {
		t.Fatalf("Push() error = %v, want ErrManifestDigestMismatch", err)
	}
}
