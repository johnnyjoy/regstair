package metadata

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSQLiteRepositoryPersistsMetadataAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regstair.db")
	repo := openSQLiteRepository(t, path)

	eventTime := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	if err := repo.RecordRequestEvent(context.Background(), RequestEvent{
		Timestamp:           eventTime,
		Operation:           OperationPull,
		LogicalReference:    "library/nginx:1.27",
		MatchedRoute:        "curated-library",
		SourceOrDestination: "external-registry",
		Status:              StatusSuccess,
		CacheResult:         CacheMiss,
		Duration:            125 * time.Millisecond,
		BytesTransferred:    42,
		Explanation:         []string{"checked internal", "used fallback"},
	}); err != nil {
		t.Fatalf("RecordRequestEvent() error = %v", err)
	}

	provenance := ProvenanceRecord{
		LogicalReference:        "library/nginx:1.27",
		PhysicalSourceReference: "library/nginx:1.27",
		RequestedReference:      "1.27",
		ResolvedDigest:          "sha256:abc",
		Source:                  "external-registry",
		Route:                   "curated-library",
		FallbackUsed:            true,
		RetrievedAt:             eventTime,
		ValidatedAt:             eventTime.Add(time.Second),
	}
	if err := repo.RecordProvenance(context.Background(), provenance); err != nil {
		t.Fatalf("RecordProvenance() error = %v", err)
	}

	mapping := TagMapping{
		LogicalRepository: "library/nginx",
		Tag:               "1.27",
		Digest:            "sha256:abc",
		MediaType:         "application/vnd.oci.image.manifest.v1+json",
		Size:              42,
		BlobDigests:       []string{"sha256:config", "sha256:layer"},
		Source:            "external-registry",
		Route:             "curated-library",
		ResolvedAt:        eventTime,
		LastValidatedAt:   eventTime.Add(time.Second),
		FreshUntil:        eventTime.Add(time.Hour),
	}
	if err := repo.RecordTagMapping(context.Background(), mapping); err != nil {
		t.Fatalf("RecordTagMapping() error = %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openSQLiteRepository(t, path)
	defer reopened.Close()

	events, err := reopened.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if !reflect.DeepEqual(events[0].Explanation, []string{"checked internal", "used fallback"}) {
		t.Fatalf("event explanation = %#v", events[0].Explanation)
	}
	if events[0].Duration != 125*time.Millisecond {
		t.Fatalf("duration = %v, want 125ms", events[0].Duration)
	}

	gotProvenance, err := reopened.FindProvenanceByLogicalReference(context.Background(), "library/nginx:1.27")
	if err != nil {
		t.Fatalf("FindProvenanceByLogicalReference() error = %v", err)
	}
	if gotProvenance == nil {
		t.Fatal("FindProvenanceByLogicalReference() = nil, want record")
	}
	if !reflect.DeepEqual(*gotProvenance, provenance) {
		t.Fatalf("provenance = %#v, want %#v", *gotProvenance, provenance)
	}

	gotMapping, err := reopened.FindTagMapping(context.Background(), "library/nginx", "1.27")
	if err != nil {
		t.Fatalf("FindTagMapping() error = %v", err)
	}
	if gotMapping == nil {
		t.Fatal("FindTagMapping() = nil, want mapping")
	}
	if !reflect.DeepEqual(*gotMapping, mapping) {
		t.Fatalf("mapping = %#v, want %#v", *gotMapping, mapping)
	}
}

func TestSQLiteRepositoryListsRecentEventsAndTagMappings(t *testing.T) {
	repo := openSQLiteRepository(t, filepath.Join(t.TempDir(), "regstair.db"))
	defer repo.Close()

	base := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	for _, event := range []RequestEvent{
		{Timestamp: base, Operation: OperationPull, LogicalReference: "library/nginx:1.27", Status: StatusSuccess},
		{Timestamp: base.Add(time.Second), Operation: OperationPush, LogicalReference: "team-a/service:4.1", Status: StatusSuccess},
	} {
		if err := repo.RecordRequestEvent(context.Background(), event); err != nil {
			t.Fatalf("RecordRequestEvent() error = %v", err)
		}
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if got, want := events[0].LogicalReference, "team-a/service:4.1"; got != want {
		t.Fatalf("recent event = %q, want %q", got, want)
	}

	for _, mapping := range []TagMapping{
		{LogicalRepository: "team-a/service", Tag: "4.1", Digest: "sha256:bbb", MediaType: "application/vnd.oci.image.manifest.v1+json", Source: "harbor-team-a", Route: "team-a-publish"},
		{LogicalRepository: "library/nginx", Tag: "1.27", Digest: "sha256:aaa", MediaType: "application/vnd.oci.image.manifest.v1+json", Source: "external-registry", Route: "curated-library"},
	} {
		if err := repo.RecordTagMapping(context.Background(), mapping); err != nil {
			t.Fatalf("RecordTagMapping() error = %v", err)
		}
	}

	mappings, err := repo.ListTagMappings(context.Background())
	if err != nil {
		t.Fatalf("ListTagMappings() error = %v", err)
	}
	if got, want := mappings[0].LogicalRepository, "library/nginx"; got != want {
		t.Fatalf("first mapping = %q, want %q", got, want)
	}
}

func TestSQLiteRepositoryFiltersAndPaginatesRequestEvents(t *testing.T) {
	repo := openSQLiteRepository(t, filepath.Join(t.TempDir(), "regstair.db"))
	defer repo.Close()
	testRequestEventQuery(t, repo)
}

func TestSQLiteRepositoryStoresUsers(t *testing.T) {
	repo := openSQLiteRepository(t, filepath.Join(t.TempDir(), "regstair.db"))
	defer repo.Close()
	clock := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return clock }
	testUserRepository(t, repo, func(value time.Time) { clock = value })
}

func openSQLiteRepository(t *testing.T, path string) *SQLiteRepository {
	t.Helper()
	repo, err := NewSQLiteRepository(path)
	if err != nil {
		t.Fatalf("NewSQLiteRepository() error = %v", err)
	}
	return repo
}
