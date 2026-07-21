package resolution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"regstair/internal/content"
	"regstair/internal/identity"
	"regstair/internal/metadata"
	"regstair/internal/policy"
	"regstair/internal/registry"
)

const (
	resolverBlobDigest     = "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"
	resolverManifestDigest = "sha256:bafebd36189ad3688b7b3915ea55d461e0bfcfbdde11e54b0a123999fb6be50f"
)

func TestPullResolverUsesExternalFallbackAndCachesContent(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "internal-curated"}, {ID: "external-registry"}},
		Routes: []policy.Route{
			{
				Name:             "curated-library",
				Match:            "library/**",
				Precedence:       10,
				PullSources:      []string{"internal-curated", "external-registry"},
				Authoritative:    "internal-curated",
				ExternalFallback: true,
			},
		},
	})
	store := newTestStore(t)
	repo := metadata.NewMemoryRepository()

	internal := registry.NewFakeConnector("internal-curated")
	external := registry.NewFakeConnector("external-registry")
	external.AddBlob(resolverBlobDigest, []byte("hello regstair"))
	external.AddManifest("library/nginx", "1.27", testManifest())

	resolver := NewPullResolver(engine, store, repo, map[string]registry.Connector{
		"internal-curated":  internal,
		"external-registry": external,
	})

	result, err := resolver.Pull(context.Background(), PullRequest{Repository: "library/nginx", Reference: "1.27", Principal: identity.Principal{Kind: identity.KindLocalUser, ID: "ci-builder"}})
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}

	if got, want := result.Source, "external-registry"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if !result.FallbackUsed {
		t.Fatal("fallback used = false, want true")
	}
	if got, want := result.Manifest.Digest, resolverManifestDigest; got != want {
		t.Fatalf("manifest digest = %q, want %q", got, want)
	}

	for _, digest := range []string{resolverManifestDigest, resolverBlobDigest} {
		ok, err := store.HasBlob(context.Background(), digest)
		if err != nil {
			t.Fatalf("HasBlob(%s) error = %v", digest, err)
		}
		if !ok {
			t.Fatalf("HasBlob(%s) = false, want true", digest)
		}
	}

	provenance, err := repo.FindProvenanceByLogicalReference(context.Background(), "library/nginx:1.27")
	if err != nil {
		t.Fatalf("FindProvenanceByLogicalReference() error = %v", err)
	}
	if provenance == nil {
		t.Fatal("provenance = nil, want record")
	}
	if !provenance.FallbackUsed {
		t.Fatal("provenance fallback used = false, want true")
	}
	if got, want := provenance.Source, "external-registry"; got != want {
		t.Fatalf("provenance source = %q, want %q", got, want)
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].CacheResult, metadata.CacheMiss; got != want {
		t.Fatalf("cache result = %q, want %q", got, want)
	}
	if got, want := events[0].ClientIdentity, "ci-builder"; got != want {
		t.Fatalf("client identity = %q, want %q", got, want)
	}
}

func TestPullResolverServesFreshTagMappingFromCacheWhenExternalUnavailable(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "internal-curated"}, {ID: "external-registry"}},
		Routes: []policy.Route{
			{
				Name:             "curated-library",
				Match:            "library/**",
				Precedence:       10,
				PullSources:      []string{"internal-curated", "external-registry"},
				Authoritative:    "internal-curated",
				ExternalFallback: true,
			},
		},
	})
	store := newTestStore(t)
	repo := metadata.NewMemoryRepository()

	internal := registry.NewFakeConnector("internal-curated")
	external := registry.NewFakeConnector("external-registry")
	external.AddBlob(resolverBlobDigest, []byte("hello regstair"))
	external.AddManifest("library/nginx", "1.27", testManifest())

	resolver := NewPullResolver(engine, store, repo, map[string]registry.Connector{
		"internal-curated":  internal,
		"external-registry": external,
	})

	first, err := resolver.Pull(context.Background(), PullRequest{Repository: "library/nginx", Reference: "1.27"})
	if err != nil {
		t.Fatalf("first Pull() error = %v", err)
	}
	if got, want := first.Source, "external-registry"; got != want {
		t.Fatalf("first source = %q, want %q", got, want)
	}

	external.SetAvailable(false)

	second, err := resolver.Pull(context.Background(), PullRequest{Repository: "library/nginx", Reference: "1.27"})
	if err != nil {
		t.Fatalf("second Pull() error = %v", err)
	}
	if got, want := second.Source, SourceCache; got != want {
		t.Fatalf("second source = %q, want %q", got, want)
	}
	if second.FallbackUsed {
		t.Fatal("second fallback used = true, want false for cache hit")
	}
	if got, want := string(second.Manifest.Content), `{"schemaVersion":2}`; got != want {
		t.Fatalf("cached manifest body = %q, want %q", got, want)
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].CacheResult, metadata.CacheHit; got != want {
		t.Fatalf("latest cache result = %q, want %q", got, want)
	}
	if got, want := events[1].CacheResult, metadata.CacheMiss; got != want {
		t.Fatalf("previous cache result = %q, want %q", got, want)
	}
}

func TestPullResolverServesDigestReferenceFromCache(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Routes: []policy.Route{
			{Name: "library", Match: "library/**", Precedence: 10, PullSources: []string{"external-registry"}, ExternalFallback: true},
		},
	})
	store := newTestStore(t)
	repo := metadata.NewMemoryRepository()
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"digest":"` + resolverBlobDigest + `"},"layers":[]}`)
	digest := testDigest(body)
	if _, err := store.PutBlob(context.Background(), digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}
	if err := repo.RecordCacheBindings(context.Background(), []metadata.CacheBinding{{LogicalRepository: "library/nginx", Route: "library", Source: "external-registry", PhysicalRepository: "library/nginx", ManifestDigest: digest, ObjectDigest: digest, ObjectKind: "manifest", Access: metadata.CacheAccessChallenge}}); err != nil {
		t.Fatal(err)
	}

	resolver := NewPullResolver(engine, store, repo, map[string]registry.Connector{})

	result, err := resolver.Pull(context.Background(), PullRequest{Repository: "library/nginx", Reference: digest})
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if got, want := result.Source, SourceCache; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if got, want := string(result.Manifest.Content), string(body); got != want {
		t.Fatalf("manifest content = %q, want %q", got, want)
	}
	if got, want := result.Manifest.MediaType, "application/vnd.docker.distribution.manifest.v2+json"; got != want {
		t.Fatalf("media type = %q, want %q", got, want)
	}
}

func testDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestPullResolverDoesNotFallbackForProtectedNamespace(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "internal-curated"}, {ID: "external-registry"}},
		Routes: []policy.Route{
			{
				Name:             "protected-platform",
				Match:            "platform/**",
				Precedence:       10,
				PullSources:      []string{"internal-curated", "external-registry"},
				Authoritative:    "internal-curated",
				ExternalFallback: false,
			},
		},
	})
	store := newTestStore(t)
	repo := metadata.NewMemoryRepository()

	internal := registry.NewFakeConnector("internal-curated")
	external := registry.NewFakeConnector("external-registry")
	external.AddBlob(resolverBlobDigest, []byte("hello regstair"))
	external.AddManifest("platform/api", "1.0.0", testManifest())

	resolver := NewPullResolver(engine, store, repo, map[string]registry.Connector{
		"internal-curated":  internal,
		"external-registry": external,
	})

	_, err := resolver.Pull(context.Background(), PullRequest{Repository: "platform/api", Reference: "1.0.0"})
	if !errors.Is(err, ErrResolutionNotFound) {
		t.Fatalf("Pull() error = %v, want ErrResolutionNotFound", err)
	}

	ok, err := store.HasBlob(context.Background(), resolverBlobDigest)
	if err != nil {
		t.Fatalf("HasBlob() error = %v", err)
	}
	if ok {
		t.Fatal("external blob was cached for protected namespace")
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].Status, metadata.StatusDenied; got != want {
		t.Fatalf("event status = %q, want %q", got, want)
	}
}

func TestPullResolverDeniesUnauthorizedRoute(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "external-registry"}},
		Routes: []policy.Route{
			{Name: "curated-library", Match: "library/**", Precedence: 10, PullSources: []string{"external-registry"}, ExternalFallback: true},
		},
	})
	repo := metadata.NewMemoryRepository()
	resolver := NewPullResolver(
		engine,
		newTestStore(t),
		repo,
		map[string]registry.Connector{"external-registry": registry.NewFakeConnector("external-registry")},
		WithAuthorizer(staticAuthorizer{allowed: false}),
	)

	_, err := resolver.Pull(context.Background(), PullRequest{
		Repository: "library/nginx",
		Reference:  "1.27",
		Principal:  identity.Principal{Kind: identity.KindLocalUser, ID: "ci-builder"},
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Pull() error = %v, want ErrUnauthorized", err)
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

func TestPullResolverReturnsUnavailableForMissingConnector(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Routes: []policy.Route{
			{Name: "library", Match: "library/**", Precedence: 10, PullSources: []string{"external-registry"}, ExternalFallback: true},
		},
	})

	resolver := NewPullResolver(engine, newTestStore(t), metadata.NewMemoryRepository(), map[string]registry.Connector{})

	_, err := resolver.Pull(context.Background(), PullRequest{Repository: "library/nginx", Reference: "1.27"})
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("Pull() error = %v, want ErrSourceUnavailable", err)
	}
}

func TestPullResolverDoesNotFallbackAfterCredentialSelectionFailure(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{
		Sources: []policy.Source{{ID: "private"}, {ID: "public"}},
		Routes:  []policy.Route{{Name: "library", Match: "library/**", Precedence: 10, PullSources: []string{"private", "public"}, ExternalFallback: true}},
	})
	provider := &recordingConnectorProvider{connectors: map[string]registry.Connector{"public": registry.NewFakeConnector("public")}, failSource: "private"}
	resolver := NewPullResolver(engine, newTestStore(t), metadata.NewMemoryRepository(), nil, WithConnectorProvider(provider))

	_, err := resolver.Pull(context.Background(), PullRequest{Repository: "library/alpine", Reference: "edge", Principal: identity.Principal{Kind: identity.KindLocalUser, ID: "user-1"}})
	if !errors.Is(err, registry.ErrCredentialRequired) {
		t.Fatalf("Pull() error = %v, want credential required", err)
	}
	if len(provider.requested) != 1 || provider.requested[0] != "private" {
		t.Fatalf("requested sources = %#v, want only private", provider.requested)
	}
}

func TestPullResolverPrivateCacheIsRepositoryAndUserBound(t *testing.T) {
	engine := newTestPolicyEngine(t, policy.Config{Sources: []policy.Source{{ID: "harbor"}}, Routes: []policy.Route{{Name: "private", Match: "team/**", Precedence: 10, PullSources: []string{"harbor"}}}})
	store := newTestStore(t)
	repo := metadata.NewMemoryRepository()
	manifest := testManifest()
	if _, err := store.PutBlob(context.Background(), manifest.Digest, bytes.NewReader(manifest.Content)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutBlob(context.Background(), resolverBlobDigest, strings.NewReader("hello regstair")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repo.RecordTagMapping(context.Background(), metadata.TagMapping{LogicalRepository: "team/private", Tag: "latest", Digest: manifest.Digest, MediaType: manifest.MediaType, Size: manifest.Size, BlobDigests: manifest.BlobDigests, Source: "harbor", Route: "private", ResolvedAt: now, LastValidatedAt: now, FreshUntil: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	bindings := []metadata.CacheBinding{
		{LogicalRepository: "team/private", Route: "private", Source: "harbor", PhysicalRepository: "team/private", ManifestDigest: manifest.Digest, ObjectDigest: manifest.Digest, ObjectKind: "manifest", Access: metadata.CacheAccessCurrentUserRequired, UserID: "alice"},
		{LogicalRepository: "team/private", Route: "private", Source: "harbor", PhysicalRepository: "team/private", ManifestDigest: manifest.Digest, ObjectDigest: resolverBlobDigest, ObjectKind: "blob", Access: metadata.CacheAccessCurrentUserRequired, UserID: "alice"},
	}
	if err := repo.RecordCacheBindings(context.Background(), bindings); err != nil {
		t.Fatal(err)
	}
	provider := cacheGrantProvider{allowedUser: "alice"}
	resolver := NewPullResolver(engine, store, repo, nil, WithConnectorProvider(provider))

	for _, principal := range []identity.Principal{identity.Anonymous(), {Kind: identity.KindLocalUser, ID: "bob"}} {
		if _, err := resolver.Pull(context.Background(), PullRequest{Repository: "team/private", Reference: "latest", Principal: principal}); err == nil {
			t.Fatalf("principal %#v received private cached manifest", principal)
		}
		if _, err := resolver.OpenBlob(context.Background(), BlobRequest{Repository: "team/private", Digest: resolverBlobDigest, Principal: principal}); err == nil {
			t.Fatalf("principal %#v received private cached blob", principal)
		}
	}
	alice := identity.Principal{Kind: identity.KindLocalUser, ID: "alice"}
	if result, err := resolver.Pull(context.Background(), PullRequest{Repository: "team/private", Reference: "latest", Principal: alice}); err != nil || result.Source != SourceCache {
		t.Fatalf("alice cache pull = %#v, %v", result, err)
	}
	blob, err := resolver.OpenBlob(context.Background(), BlobRequest{Repository: "team/private", Digest: resolverBlobDigest, Principal: alice})
	if err != nil {
		t.Fatal(err)
	}
	_ = blob.Content.Close()

	if _, err := resolver.OpenBlob(context.Background(), BlobRequest{Repository: "team/other", Digest: resolverBlobDigest, Principal: alice}); !errors.Is(err, content.ErrBlobNotFound) {
		t.Fatalf("cross-repository blob error = %v, want not found", err)
	}
}

type cacheGrantProvider struct{ allowedUser string }

func (p cacheGrantProvider) ConnectorFor(context.Context, identity.Principal, string, metadata.Operation) (registry.Connector, string, error) {
	return nil, "", registry.ErrUnavailable
}
func (p cacheGrantProvider) AuthorizeCache(_ context.Context, principal identity.Principal, binding metadata.CacheBinding, _ metadata.Operation) (string, error) {
	if binding.Access == metadata.CacheAccessCurrentUserRequired && principal.ID == p.allowedUser && binding.UserID == p.allowedUser {
		return "current_user", nil
	}
	return "", registry.ErrAuthorization
}

type recordingConnectorProvider struct {
	connectors map[string]registry.Connector
	failSource string
	requested  []string
}

func (p *recordingConnectorProvider) ConnectorFor(_ context.Context, _ identity.Principal, source string, _ metadata.Operation) (registry.Connector, string, error) {
	p.requested = append(p.requested, source)
	if source == p.failSource {
		return nil, "", registry.ErrCredentialRequired
	}
	return p.connectors[source], "anonymous", nil
}

func (p *recordingConnectorProvider) AuthorizeCache(context.Context, identity.Principal, metadata.CacheBinding, metadata.Operation) (string, error) {
	return "anonymous", nil
}

type staticAuthorizer struct {
	allowed bool
}

func (a staticAuthorizer) Authorize(identity.Principal, metadata.Operation, string) bool {
	return a.allowed
}

func newTestPolicyEngine(t *testing.T, cfg policy.Config) *policy.Engine {
	t.Helper()

	engine, err := policy.NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

func newTestStore(t *testing.T) *content.FileStore {
	t.Helper()

	store, err := content.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	return store
}

func testManifest() registry.Manifest {
	body := []byte(`{"schemaVersion":2}`)
	return registry.Manifest{
		Descriptor: registry.Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    resolverManifestDigest,
			Size:      int64(len(body)),
		},
		Content:     bytes.Clone(body),
		BlobDigests: []string{resolverBlobDigest},
	}
}

func readAllString(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return string(body)
}
