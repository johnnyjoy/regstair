package metadata

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type userRepository interface {
	CreateUser(context.Context, User) (*User, error)
	FindUserByID(context.Context, string) (*User, error)
	FindUserByUsername(context.Context, string) (*User, error)
	UpdateUser(context.Context, User, time.Time) (*User, error)
}

func TestMemoryRepositoryStoresUsers(t *testing.T) {
	repo := NewMemoryRepository()
	clock := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return clock }
	testUserRepository(t, repo, func(value time.Time) { clock = value })
}

func testUserRepository(t *testing.T, repo userRepository, setNow func(time.Time)) {
	t.Helper()
	ctx := context.Background()
	createdAt := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	setNow(createdAt)
	created, err := repo.CreateUser(ctx, User{ID: "user-1", Username: "alice", PasswordHash: "$argon2id$fixture", DisplayName: "Alice", Email: "alice@example.test", Access: UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if !created.CreatedAt.Equal(createdAt) || !created.UpdatedAt.Equal(createdAt) {
		t.Fatalf("created timestamps = %v/%v, want %v", created.CreatedAt, created.UpdatedAt, createdAt)
	}

	byUsername, err := repo.FindUserByUsername(ctx, "alice")
	if err != nil || byUsername == nil {
		t.Fatalf("FindUserByUsername() = %#v, %v", byUsername, err)
	}
	if byUsername.PasswordHash != "$argon2id$fixture" || !byUsername.Enabled {
		t.Fatalf("stored user = %#v", byUsername)
	}

	if _, err := repo.CreateUser(ctx, User{ID: "user-2", Username: "alice", PasswordHash: "hash", Access: UserAccessUser, Enabled: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateUser() error = %v, want ErrConflict", err)
	}

	updatedAt := createdAt.Add(time.Minute)
	setNow(updatedAt)
	changed := *created
	changed.DisplayName = "Alice Operator"
	changed.Access = UserAccessAdmin
	changed.Enabled = false
	updated, err := repo.UpdateUser(ctx, changed, created.UpdatedAt)
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if !updated.CreatedAt.Equal(createdAt) || !updated.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated timestamps = %v/%v", updated.CreatedAt, updated.UpdatedAt)
	}
	if updated.DisplayName != "Alice Operator" || updated.Access != UserAccessAdmin || updated.Enabled {
		t.Fatalf("updated user = %#v", updated)
	}

	if _, err := repo.UpdateUser(ctx, changed, created.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateUser() error = %v, want ErrConflict", err)
	}
	missing, err := repo.FindUserByID(ctx, "missing")
	if err != nil || missing != nil {
		t.Fatalf("FindUserByID(missing) = %#v, %v", missing, err)
	}
}

func TestMemoryRepositoryStoresAndListsRecentRequestEvents(t *testing.T) {
	repo := NewMemoryRepository()
	base := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)

	events := []RequestEvent{
		{Timestamp: base, Operation: OperationPull, LogicalReference: "library/nginx:1.27", MatchedRoute: "curated-library", Status: StatusSuccess},
		{Timestamp: base.Add(time.Second), Operation: OperationPull, LogicalReference: "platform/api:1.0.0", MatchedRoute: "protected-platform", Status: StatusDenied},
		{Timestamp: base.Add(2 * time.Second), Operation: OperationPush, LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", Status: StatusSuccess},
	}

	for _, event := range events {
		if err := repo.RecordRequestEvent(context.Background(), event); err != nil {
			t.Fatalf("RecordRequestEvent() error = %v", err)
		}
	}

	got, err := repo.ListRecentRequestEvents(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}

	wantRefs := []string{"team-a/service:4.1", "platform/api:1.0.0"}
	gotRefs := []string{got[0].LogicalReference, got[1].LogicalReference}
	if !reflect.DeepEqual(gotRefs, wantRefs) {
		t.Fatalf("recent refs = %#v, want %#v", gotRefs, wantRefs)
	}
}

func TestMemoryRepositoryAssignsRequestEventTimestampWhenMissing(t *testing.T) {
	repo := NewMemoryRepository()
	before := time.Now().UTC()

	if err := repo.RecordRequestEvent(context.Background(), RequestEvent{
		Operation:        OperationPull,
		LogicalReference: "library/nginx:1.27",
		Status:           StatusSuccess,
	}); err != nil {
		t.Fatalf("RecordRequestEvent() error = %v", err)
	}

	events, err := repo.ListRecentRequestEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRequestEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if events[0].Timestamp.Before(before) {
		t.Fatalf("timestamp = %v, want after %v", events[0].Timestamp, before)
	}
}

func TestMemoryRepositoryRejectsInvalidRequestEvent(t *testing.T) {
	repo := NewMemoryRepository()

	err := repo.RecordRequestEvent(context.Background(), RequestEvent{
		Operation: OperationPull,
		Status:    StatusSuccess,
	})
	if err == nil {
		t.Fatal("RecordRequestEvent() error = nil, want validation error")
	}
}

func TestMemoryRepositoryFiltersAndPaginatesRequestEvents(t *testing.T) {
	testRequestEventQuery(t, NewMemoryRepository())
}

func testRequestEventQuery(t *testing.T, repo Repository) {
	t.Helper()
	base := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	events := []RequestEvent{
		{Timestamp: base, Operation: OperationPull, ClientIdentity: "ci-builder", LogicalReference: "library/nginx:1.27", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: StatusSuccess, CacheResult: CacheMiss, Duration: 100 * time.Millisecond},
		{Timestamp: base, Operation: OperationPull, ClientIdentity: "ci-builder", LogicalReference: "library/alpine:edge", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: StatusSuccess, CacheResult: CacheHit, Duration: 200 * time.Millisecond},
		{Timestamp: base.Add(-time.Minute), Operation: OperationPull, ClientIdentity: "release-bot", LogicalReference: "platform/api:1.0", MatchedRoute: "protected-platform", Status: StatusDenied, CacheResult: CacheMiss, Duration: 300 * time.Millisecond, ErrorClassification: "authorization_denied"},
		{Timestamp: base.Add(-2 * time.Minute), Operation: OperationPush, ClientIdentity: "ci-builder", LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", SourceOrDestination: "harbor-team-a", Status: StatusError, CacheResult: CacheBypassed, Duration: 400 * time.Millisecond, ErrorClassification: "upstream_authentication_failed"},
	}
	for _, event := range events {
		if err := repo.RecordRequestEvent(context.Background(), event); err != nil {
			t.Fatalf("RecordRequestEvent() error = %v", err)
		}
	}

	filtered, err := repo.QueryRequestEvents(context.Background(), RequestEventQuery{
		Filter: RequestEventFilter{
			ClientIdentity:      "ci-builder",
			Operation:           OperationPush,
			Status:              StatusError,
			SourceOrDestination: "harbor-team-a",
			ErrorClassification: "upstream_authentication_failed",
			ReferenceContains:   "service:4",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryRequestEvents(filtered) error = %v", err)
	}
	if len(filtered.Events) != 1 || filtered.Events[0].LogicalReference != "team-a/service:4.1" {
		t.Fatalf("filtered events = %#v", filtered.Events)
	}

	first, err := repo.QueryRequestEvents(context.Background(), RequestEventQuery{Limit: 2})
	if err != nil {
		t.Fatalf("QueryRequestEvents(first) error = %v", err)
	}
	if len(first.Events) != 2 || first.Next == nil {
		t.Fatalf("first page = %#v, next = %#v", first.Events, first.Next)
	}
	if first.Events[0].ID <= first.Events[1].ID {
		t.Fatalf("equal-timestamp IDs are not descending: %d, %d", first.Events[0].ID, first.Events[1].ID)
	}

	second, err := repo.QueryRequestEvents(context.Background(), RequestEventQuery{Limit: 2, Cursor: first.Next})
	if err != nil {
		t.Fatalf("QueryRequestEvents(second) error = %v", err)
	}
	if len(second.Events) != 2 || second.Next != nil {
		t.Fatalf("second page = %#v, next = %#v", second.Events, second.Next)
	}
	seen := map[int64]bool{}
	for _, event := range append(first.Events, second.Events...) {
		if seen[event.ID] {
			t.Fatalf("event ID %d appeared on multiple pages", event.ID)
		}
		seen[event.ID] = true
	}
	summary, err := repo.SummarizeRequestEvents(context.Background(), base.Add(-3*time.Minute))
	if err != nil {
		t.Fatalf("SummarizeRequestEvents() error = %v", err)
	}
	if summary.Total != 4 || summary.Errors != 1 || summary.AuthFailures != 1 || summary.CacheHits != 1 || summary.CacheMisses != 2 || summary.Average != 250*time.Millisecond || summary.P95 != 400*time.Millisecond {
		t.Fatalf("request summary = %#v", summary)
	}
	count, err := repo.CountRequestEvents(context.Background(), RequestEventFilter{CacheResult: CacheMiss})
	if err != nil || count != 2 {
		t.Fatalf("CountRequestEvents(cache miss) = %d, %v, want 2", count, err)
	}
	oldest, err := repo.QueryRequestEvents(context.Background(), RequestEventQuery{Filter: RequestEventFilter{CacheResult: CacheMiss}, Limit: 2, OldestFirst: true})
	if err != nil || len(oldest.Events) != 2 || oldest.Events[0].LogicalReference != "platform/api:1.0" {
		t.Fatalf("oldest cache misses = %#v, %v", oldest.Events, err)
	}
}

func TestMemoryRepositoryStoresAndFindsProvenanceByLogicalReference(t *testing.T) {
	repo := NewMemoryRepository()
	retrievedAt := time.Date(2026, 7, 18, 16, 10, 0, 0, time.UTC)
	validatedAt := retrievedAt.Add(time.Minute)

	record := ProvenanceRecord{
		LogicalReference:        "library/nginx:1.27",
		PhysicalSourceReference: "registry-2/library/nginx:1.27",
		RequestedReference:      "1.27",
		ResolvedDigest:          "sha256:abc123",
		Source:                  "external-registry",
		Route:                   "curated-library",
		FallbackUsed:            true,
		StaleServed:             false,
		RetrievedAt:             retrievedAt,
		ValidatedAt:             validatedAt,
	}

	if err := repo.RecordProvenance(context.Background(), record); err != nil {
		t.Fatalf("RecordProvenance() error = %v", err)
	}

	got, err := repo.FindProvenanceByLogicalReference(context.Background(), "library/nginx:1.27")
	if err != nil {
		t.Fatalf("FindProvenanceByLogicalReference() error = %v", err)
	}
	if got == nil {
		t.Fatal("FindProvenanceByLogicalReference() = nil, want record")
	}
	if !reflect.DeepEqual(*got, record) {
		t.Fatalf("provenance = %#v, want %#v", *got, record)
	}
}

func TestMemoryRepositoryReturnsLatestProvenanceForLogicalReference(t *testing.T) {
	repo := NewMemoryRepository()
	oldTime := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)

	records := []ProvenanceRecord{
		{LogicalReference: "library/nginx:latest", RequestedReference: "latest", ResolvedDigest: "sha256:old", Source: "external-registry", Route: "curated-library", RetrievedAt: oldTime},
		{LogicalReference: "library/nginx:latest", RequestedReference: "latest", ResolvedDigest: "sha256:new", Source: "external-registry", Route: "curated-library", RetrievedAt: newTime},
	}
	for _, record := range records {
		if err := repo.RecordProvenance(context.Background(), record); err != nil {
			t.Fatalf("RecordProvenance() error = %v", err)
		}
	}

	got, err := repo.FindProvenanceByLogicalReference(context.Background(), "library/nginx:latest")
	if err != nil {
		t.Fatalf("FindProvenanceByLogicalReference() error = %v", err)
	}
	if got == nil {
		t.Fatal("FindProvenanceByLogicalReference() = nil, want record")
	}
	if got.ResolvedDigest != "sha256:new" {
		t.Fatalf("resolved digest = %q, want sha256:new", got.ResolvedDigest)
	}
}

func TestMemoryRepositoryRejectsInvalidProvenance(t *testing.T) {
	repo := NewMemoryRepository()

	err := repo.RecordProvenance(context.Background(), ProvenanceRecord{
		LogicalReference: "library/nginx:1.27",
	})
	if err == nil {
		t.Fatal("RecordProvenance() error = nil, want validation error")
	}
}

func TestMemoryRepositoryStoresAndFindsTagMapping(t *testing.T) {
	repo := NewMemoryRepository()
	resolvedAt := time.Date(2026, 7, 18, 16, 30, 0, 0, time.UTC)
	expiresAt := resolvedAt.Add(time.Hour)

	mapping := TagMapping{
		LogicalRepository: "library/nginx",
		Tag:               "1.27",
		Digest:            "sha256:bafebd36189ad3688b7b3915ea55d461e0bfcfbdde11e54b0a123999fb6be50f",
		MediaType:         "application/vnd.oci.image.manifest.v1+json",
		Size:              19,
		BlobDigests:       []string{"sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"},
		Source:            "external-registry",
		Route:             "curated-library",
		ResolvedAt:        resolvedAt,
		LastValidatedAt:   resolvedAt,
		FreshUntil:        expiresAt,
	}

	if err := repo.RecordTagMapping(context.Background(), mapping); err != nil {
		t.Fatalf("RecordTagMapping() error = %v", err)
	}

	got, err := repo.FindTagMapping(context.Background(), "library/nginx", "1.27")
	if err != nil {
		t.Fatalf("FindTagMapping() error = %v", err)
	}
	if got == nil {
		t.Fatal("FindTagMapping() = nil, want mapping")
	}
	if !reflect.DeepEqual(*got, mapping) {
		t.Fatalf("tag mapping = %#v, want %#v", *got, mapping)
	}

	got.BlobDigests[0] = "sha256:mutated"
	again, err := repo.FindTagMapping(context.Background(), "library/nginx", "1.27")
	if err != nil {
		t.Fatalf("FindTagMapping() second error = %v", err)
	}
	if again.BlobDigests[0] != mapping.BlobDigests[0] {
		t.Fatal("stored tag mapping blob digests were mutated through returned pointer")
	}
}

func TestMemoryRepositoryReplacesTagMapping(t *testing.T) {
	repo := NewMemoryRepository()
	oldMapping := TagMapping{
		LogicalRepository: "library/nginx",
		Tag:               "latest",
		Digest:            "sha256:old",
		MediaType:         "application/vnd.oci.image.manifest.v1+json",
		Size:              19,
		Source:            "external-registry",
		Route:             "curated-library",
	}
	newMapping := oldMapping
	newMapping.Digest = "sha256:new"

	if err := repo.RecordTagMapping(context.Background(), oldMapping); err != nil {
		t.Fatalf("RecordTagMapping(old) error = %v", err)
	}
	if err := repo.RecordTagMapping(context.Background(), newMapping); err != nil {
		t.Fatalf("RecordTagMapping(new) error = %v", err)
	}

	got, err := repo.FindTagMapping(context.Background(), "library/nginx", "latest")
	if err != nil {
		t.Fatalf("FindTagMapping() error = %v", err)
	}
	if got == nil {
		t.Fatal("FindTagMapping() = nil, want mapping")
	}
	if got.Digest != "sha256:new" {
		t.Fatalf("digest = %q, want sha256:new", got.Digest)
	}
}

func TestMemoryRepositoryRejectsInvalidTagMapping(t *testing.T) {
	repo := NewMemoryRepository()

	err := repo.RecordTagMapping(context.Background(), TagMapping{
		LogicalRepository: "library/nginx",
		Tag:               "1.27",
	})
	if err == nil {
		t.Fatal("RecordTagMapping() error = nil, want validation error")
	}
}

func TestMemoryRepositoryListsTagMappings(t *testing.T) {
	repo := NewMemoryRepository()
	mappings := []TagMapping{
		{
			LogicalRepository: "library/nginx",
			Tag:               "1.27",
			Digest:            "sha256:aaa",
			MediaType:         "application/vnd.oci.image.manifest.v1+json",
			Source:            "external-registry",
			Route:             "curated-library",
		},
		{
			LogicalRepository: "team-a/service",
			Tag:               "4.1",
			Digest:            "sha256:bbb",
			MediaType:         "application/vnd.oci.image.manifest.v1+json",
			Source:            "harbor-team-a",
			Route:             "team-a-publish",
		},
	}
	for _, mapping := range mappings {
		if err := repo.RecordTagMapping(context.Background(), mapping); err != nil {
			t.Fatalf("RecordTagMapping() error = %v", err)
		}
	}

	got, err := repo.ListTagMappings(context.Background())
	if err != nil {
		t.Fatalf("ListTagMappings() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("mapping count = %d, want 2", len(got))
	}
	if got[0].LogicalRepository != "library/nginx" {
		t.Fatalf("first mapping repository = %q, want library/nginx", got[0].LogicalRepository)
	}
}
