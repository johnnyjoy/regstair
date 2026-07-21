package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"regstair/internal/auth"
	"regstair/internal/config"
	"regstair/internal/content"
	"regstair/internal/metadata"
)

func TestServerListsSourcesAndRoutes(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	server := NewServer(Config{
		Config: testConfig(),
		Repo:   repo,
	})

	sources := getJSON[SourcesResponse](t, server, "/admin/api/sources")
	if len(sources.Sources) != 2 {
		t.Fatalf("source count = %d, want 2", len(sources.Sources))
	}
	if sources.Sources[0].ID != "internal-curated" {
		t.Fatalf("first source = %q, want internal-curated", sources.Sources[0].ID)
	}

	routes := getJSON[RoutesResponse](t, server, "/admin/api/routes")
	if len(routes.Routes) != 2 {
		t.Fatalf("route count = %d, want 2", len(routes.Routes))
	}
	if routes.Routes[0].Pull.Authoritative != "internal-curated" {
		t.Fatalf("authoritative source = %q, want internal-curated", routes.Routes[0].Pull.Authoritative)
	}
}

func TestServerListsRecentRequests(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	if err := repo.RecordRequestEvent(testContext(t), metadata.RequestEvent{
		Timestamp:           time.Date(2026, 7, 18, 17, 0, 0, 0, time.UTC),
		Operation:           metadata.OperationPull,
		LogicalReference:    "library/nginx:1.27",
		MatchedRoute:        "curated-library",
		SourceOrDestination: "external-registry",
		Status:              metadata.StatusSuccess,
		CacheResult:         metadata.CacheMiss,
		Explanation:         []string{"checked internal", "used fallback"},
	}); err != nil {
		t.Fatalf("RecordRequestEvent() error = %v", err)
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	response := getJSON[RequestsResponse](t, server, "/admin/api/requests?limit=10")

	if len(response.Requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(response.Requests))
	}
	if got, want := response.Requests[0].CacheResult, metadata.CacheMiss; got != want {
		t.Fatalf("cache result = %q, want %q", got, want)
	}
	if len(response.Requests[0].Explanation) != 2 {
		t.Fatalf("explanation count = %d, want 2", len(response.Requests[0].Explanation))
	}
}

func TestServerFiltersAndPaginatesRequests(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	base := time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC)
	for _, event := range []metadata.RequestEvent{
		{Timestamp: base, Operation: metadata.OperationPull, ClientIdentity: "ci-builder", LogicalReference: "library/nginx:1.27", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: metadata.StatusSuccess, CacheResult: metadata.CacheMiss},
		{Timestamp: base.Add(-time.Second), Operation: metadata.OperationPull, ClientIdentity: "ci-builder", LogicalReference: "library/alpine:edge", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: metadata.StatusSuccess, CacheResult: metadata.CacheHit},
		{Timestamp: base.Add(-2 * time.Second), Operation: metadata.OperationPush, ClientIdentity: "release-bot", LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", SourceOrDestination: "harbor-team-a", Status: metadata.StatusError, CacheResult: metadata.CacheBypassed, ErrorClassification: "upstream_authentication_failed"},
	} {
		if err := repo.RecordRequestEvent(testContext(t), event); err != nil {
			t.Fatalf("RecordRequestEvent() error = %v", err)
		}
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	first := getJSON[RequestsResponse](t, server, "/admin/api/requests?client_identity=ci-builder&operation=pull&route=curated-library&source=external-registry&reference=library/&status=success&limit=1")
	if len(first.Requests) != 1 || first.Requests[0].LogicalReference != "library/nginx:1.27" {
		t.Fatalf("first page = %#v", first.Requests)
	}
	if first.NextCursor == "" {
		t.Fatal("next cursor is empty")
	}

	second := getJSON[RequestsResponse](t, server, "/admin/api/requests?client_identity=ci-builder&operation=pull&route=curated-library&source=external-registry&reference=library/&status=success&limit=1&cursor="+first.NextCursor)
	if len(second.Requests) != 1 || second.Requests[0].LogicalReference != "library/alpine:edge" {
		t.Fatalf("second page = %#v", second.Requests)
	}
	if second.NextCursor != "" {
		t.Fatalf("second next cursor = %q, want empty", second.NextCursor)
	}
}

func TestServerRejectsInvalidRequestFilters(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})
	for _, path := range []string{
		"/admin/api/requests?operation=delete",
		"/admin/api/requests?status=unknown",
		"/admin/api/requests?cache=unknown",
		"/admin/api/requests?credential=unknown",
		"/admin/api/requests?window=forever",
		"/admin/api/requests?window=24h&after=2026-07-20T10:00",
		"/admin/api/requests?sort=sideways",
		"/admin/api/requests?limit=101",
		"/admin/api/requests?cursor=not-a-cursor",
		"/admin/api/requests?after=not-a-time",
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusBadRequest)
		}
	}
}

func TestServerFindsProvenanceByReference(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	if err := repo.RecordProvenance(testContext(t), metadata.ProvenanceRecord{
		LogicalReference:        "library/nginx:1.27",
		PhysicalSourceReference: "library/nginx:1.27",
		RequestedReference:      "1.27",
		ResolvedDigest:          "sha256:abc",
		Source:                  "external-registry",
		Route:                   "curated-library",
		FallbackUsed:            true,
	}); err != nil {
		t.Fatalf("RecordProvenance() error = %v", err)
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	response := getJSON[ProvenanceResponse](t, server, "/admin/api/provenance?reference=library/nginx:1.27")

	if response.Provenance == nil {
		t.Fatal("provenance = nil, want record")
	}
	if !response.Provenance.FallbackUsed {
		t.Fatal("fallback used = false, want true")
	}
}

func TestServerReturnsNotFoundForMissingProvenance(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/api/provenance?reference=library/nginx:missing", nil)
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestServerListsArtifactsFromTagMappings(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	if err := repo.RecordTagMapping(testContext(t), metadata.TagMapping{
		LogicalRepository: "library/nginx",
		Tag:               "1.27",
		Digest:            "sha256:abc",
		MediaType:         "application/vnd.oci.image.manifest.v1+json",
		Size:              123,
		Source:            "external-registry",
		Route:             "curated-library",
	}); err != nil {
		t.Fatalf("RecordTagMapping() error = %v", err)
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	response := getJSON[ArtifactsResponse](t, server, "/admin/api/artifacts")

	if len(response.Artifacts) != 1 {
		t.Fatalf("artifact count = %d, want 1", len(response.Artifacts))
	}
	if got, want := response.Artifacts[0].LogicalReference, "library/nginx:1.27"; got != want {
		t.Fatalf("logical reference = %q, want %q", got, want)
	}
}

func TestServerListsCacheBlobs(t *testing.T) {
	store, err := content.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if _, err := store.PutBlob(testContext(t), "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69", strings.NewReader("hello regstair")); err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}

	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository(), Store: store})
	response := getJSON[CacheResponse](t, server, "/admin/api/cache")

	if len(response.Blobs) != 1 {
		t.Fatalf("blob count = %d, want 1", len(response.Blobs))
	}
	if got, want := response.Blobs[0].Digest, "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"; got != want {
		t.Fatalf("blob digest = %q, want %q", got, want)
	}
}

func TestServerRejectsUnsupportedAdminRoute(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/api/nope", nil)
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestServerServesReactRequestsWorkspace(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	if err := repo.RecordRequestEvent(testContext(t), metadata.RequestEvent{
		Operation:           metadata.OperationPull,
		ClientIdentity:      "ci-builder",
		LogicalReference:    "library/nginx:1.27",
		MatchedRoute:        "curated-library",
		SourceOrDestination: "external-registry",
		Status:              metadata.StatusSuccess,
		CacheResult:         metadata.CacheHit,
		ErrorClassification: "upstream_authentication_failed",
		Explanation:         []string{"served tag reference from fresh local cache mapping"},
	}); err != nil {
		t.Fatalf("RecordRequestEvent() error = %v", err)
	}
	if err := repo.RecordTagMapping(testContext(t), metadata.TagMapping{
		LogicalRepository: "library/nginx",
		Tag:               "1.27",
		Digest:            "sha256:abc",
		MediaType:         "application/vnd.oci.image.manifest.v1+json",
		Source:            "external-registry",
		Route:             "curated-library",
	}); err != nil {
		t.Fatalf("RecordTagMapping() error = %v", err)
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/requests", nil)
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got, want := response.Header().Get("Content-Type"), "text/html; charset=utf-8"; got != want {
		t.Fatalf("content type = %q, want %q", got, want)
	}
	body := response.Body.String()
	if !strings.Contains(body, `<div id="root"></div>`) || !strings.Contains(body, `type="module"`) {
		t.Fatalf("requests route did not serve the React application: %s", body)
	}
}

func TestServerRendersDedicatedAdminPages(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})
	application := httptest.NewRecorder()
	server.ServeHTTP(application, httptest.NewRequest(http.MethodGet, "/", nil))
	if application.Code != http.StatusOK {
		t.Fatalf("application status = %d, want %d", application.Code, http.StatusOK)
	}
	if body := application.Body.String(); !strings.Contains(body, `<div id="root"></div>`) || !strings.Contains(body, `type="module"`) || strings.Contains(body, `<h1>Overview</h1>`) {
		t.Fatalf("root did not serve the React application document: %s", body)
	}

	tests := []struct{ path, title, activeHref, present, absent string }{}
	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body %s", response.Code, http.StatusOK, response.Body.String())
			}
			body := response.Body.String()
			for _, want := range []string{"<title>" + test.title + " | Regstair</title>", `<h1>` + test.title + `</h1>`, `href="` + test.activeHref + `" aria-current="page"`, test.present} {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			if strings.Contains(body, test.absent) {
				t.Errorf("body contains unrelated workspace %q", test.absent)
			}
		})
	}
}

func TestReactOverviewDocumentAndOperationalAPIsExposeActivity(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	now := time.Now().UTC()
	events := []metadata.RequestEvent{
		{Timestamp: now.Add(-time.Hour), Operation: metadata.OperationPull, LogicalReference: "library/alpine:edge", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: metadata.StatusSuccess, CacheResult: metadata.CacheHit, Duration: 100 * time.Millisecond},
		{Timestamp: now.Add(-2 * time.Hour), Operation: metadata.OperationPull, LogicalReference: "library/nginx:latest", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: metadata.StatusError, CacheResult: metadata.CacheMiss, Duration: 300 * time.Millisecond, ErrorClassification: "upstream_authentication_failed"},
		{Timestamp: now.Add(-3 * time.Hour), Operation: metadata.OperationPush, LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", SourceOrDestination: "harbor-team-a", Status: metadata.StatusDenied, CacheResult: metadata.CacheBypassed, Duration: 200 * time.Millisecond, ErrorClassification: "authorization_denied"},
		{Timestamp: now.Add(-25 * time.Hour), Operation: metadata.OperationPull, LogicalReference: "old/ignored:1", Status: metadata.StatusError, CacheResult: metadata.CacheMiss, Duration: time.Second},
	}
	for _, event := range events {
		if err := repo.RecordRequestEvent(testContext(t), event); err != nil {
			t.Fatal(err)
		}
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	application := httptest.NewRecorder()
	server.ServeHTTP(application, httptest.NewRequest(http.MethodGet, "/", nil))
	if application.Code != http.StatusOK {
		t.Fatalf("application = %d %s", application.Code, application.Body.String())
	}
	document := application.Body.String()
	nonceMatch := regexp.MustCompile(`name="csp-nonce" content="([^"]+)"`).FindStringSubmatch(document)
	if len(nonceMatch) != 2 || !strings.Contains(application.Header().Get("Content-Security-Policy"), "'nonce-"+nonceMatch[1]+"'") {
		t.Fatalf("application document and CSP do not share a style nonce")
	}
	assetMatch := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindStringSubmatch(document)
	if len(assetMatch) != 2 {
		t.Fatalf("application document does not reference a hashed JavaScript asset: %s", document)
	}
	asset := httptest.NewRecorder()
	server.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, assetMatch[1], nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("application asset response = %d %q", asset.Code, asset.Header().Get("Content-Type"))
	}
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("application asset cache control = %q", got)
	}

	requests := getJSON[RequestsResponse](t, server, "/admin/api/requests?limit=12")
	if len(requests.Requests) != 4 {
		t.Fatalf("overview requests = %d, want 4", len(requests.Requests))
	}
	for _, want := range []string{"library/nginx:latest", "team-a/service:4.1"} {
		found := false
		for _, event := range requests.Requests {
			found = found || event.LogicalReference == want
		}
		if !found {
			t.Errorf("overview API missing %q", want)
		}
	}
}

func TestDashboardFiltersAndPaginatesRequests(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	base := time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC)
	for _, event := range []metadata.RequestEvent{
		{Timestamp: base, Operation: metadata.OperationPull, ClientIdentity: "ci-builder", LogicalReference: "library/nginx:1.27", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: metadata.StatusSuccess, CacheResult: metadata.CacheMiss},
		{Timestamp: base.Add(-time.Second), Operation: metadata.OperationPull, ClientIdentity: "ci-builder", LogicalReference: "library/alpine:edge", MatchedRoute: "curated-library", SourceOrDestination: "external-registry", Status: metadata.StatusSuccess, CacheResult: metadata.CacheHit},
		{Timestamp: base.Add(-2 * time.Second), Operation: metadata.OperationPush, ClientIdentity: "release-bot", LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", SourceOrDestination: "internal-curated", Status: metadata.StatusError, CacheResult: metadata.CacheBypassed},
	} {
		if err := repo.RecordRequestEvent(testContext(t), event); err != nil {
			t.Fatalf("RecordRequestEvent() error = %v", err)
		}
	}

	server := NewServer(Config{Config: testConfig(), Repo: repo})
	path := "/requests?client_identity=ci-builder&operation=pull&route=curated-library&source=external-registry&reference=library%2F&status=success&limit=1"
	first := getJSON[RequestsResponse](t, server, "/admin/api"+path)
	if len(first.Requests) != 1 || first.Requests[0].LogicalReference != "library/nginx:1.27" || first.NextCursor == "" {
		t.Fatalf("first filtered API page = %#v", first)
	}
	second := getJSON[RequestsResponse](t, server, "/admin/api"+path+"&cursor="+first.NextCursor)
	if len(second.Requests) != 1 || second.Requests[0].LogicalReference != "library/alpine:edge" {
		t.Fatalf("second filtered API page = %#v", second)
	}
}

func TestRequestDetailAPIIsFocusedAndRedactsCredentialInternals(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	ctx := testContext(t)
	event := metadata.RequestEvent{Timestamp: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Operation: metadata.OperationPush, ClientIdentity: "user-1", LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", SourceOrDestination: "harbor-team-a", Status: metadata.StatusSuccess, CacheResult: metadata.CacheBypassed, CredentialSource: "current_user", Duration: 1250 * time.Millisecond, BytesTransferred: 4096, Explanation: []string{"matched route", "published manifest"}}
	if err := repo.RecordRequestEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordProvenance(ctx, metadata.ProvenanceRecord{LogicalReference: event.LogicalReference, PhysicalSourceReference: "production/service:4.1", RequestedReference: "4.1", ResolvedDigest: "sha256:abc", Source: "harbor-team-a", Route: "team-a-publish", RetrievedAt: event.Timestamp}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{Config: testConfig(), Repo: repo})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/requests/1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, required := range []string{`"credential":"Current user credential"`, `"duration_ms":1250`, `"physical_source_reference":"production/service:4.1"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("detail missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{`"credential_source"`, `current_user`, `credential_ref`, `encrypted`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("detail leaked %q: %s", forbidden, body)
		}
	}

	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/admin/api/requests/999", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing detail = %d", missing.Code)
	}
}

func TestRequestDetailPageIsAStableReactRoute(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	event := metadata.RequestEvent{Timestamp: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Operation: metadata.OperationPush, ClientIdentity: "alice", LogicalReference: "team-a/service:4.1", MatchedRoute: "team-a-publish", SourceOrDestination: "harbor-team-a", Status: metadata.StatusError, CacheResult: metadata.CacheBypassed, CredentialSource: "current_user", Duration: 1250 * time.Millisecond, BytesTransferred: 4096, ErrorClassification: "upstream_authentication_failed", Explanation: []string{"matched team route", "upstream rejected authentication"}}
	if err := repo.RecordRequestEvent(testContext(t), event); err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{Config: testConfig(), Repo: repo})
	body := getHTML(t, server, "/requests/1")
	if !strings.Contains(body, `<div id="root"></div>`) || !strings.Contains(body, `type="module"`) {
		t.Fatalf("request detail route did not serve React application: %s", body)
	}
}

func TestRequestsWorkspacePreservesFilterQueryForTheReactClient(t *testing.T) {
	repo := metadata.NewMemoryRepository()
	if err := repo.RecordRequestEvent(testContext(t), metadata.RequestEvent{Operation: metadata.OperationPull, LogicalReference: "library/alpine:edge", Status: metadata.StatusError, CacheResult: metadata.CacheMiss}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{Config: testConfig(), Repo: repo})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/requests?status=error&operation=pull&reference=alpine", nil)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("filtered requests route = %d %s", response.Code, response.Body.String())
	}
}

func TestAuditWorkspaceHumanizesFiltersAndCorrelatesEvents(t *testing.T) {
	fixture := newAuthServerFixture(t)
	ctx := testContext(t)
	alice, err := fixture.admins.Create(ctx, fixture.admin.ID, auth.NewUser{Username: "alice", DisplayName: "Alice Operator", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []metadata.AuditEvent{
		{ActorUserID: fixture.admin.ID, ActorRole: "admin", Action: "user.access_changed", TargetType: "user", TargetID: alice.ID, Outcome: "success", CorrelationID: "change-42"},
		{ActorUserID: alice.ID, ActorRole: "user", Action: "credential.verification_failed", TargetType: "registry_credential", TargetID: "harbor", Outcome: "failure", Details: map[string]string{"source_id": "harbor", "error_classification": "upstream_authentication_failed"}},
	} {
		if _, err := fixture.repo.RecordAuditEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	response := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/audit", nil, login.cookie, "")
	body := response.Body.String()
	for _, want := range []string{"Changed user access", "admin", "Alice Operator", "user.access_changed", "change-42", "Credential verification failed", "upstream_authentication_failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("audit API missing %q", want)
		}
	}
	for _, forbidden := range []string{"credential_secret", "encrypted_secret", "token_hash"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("audit API exposed forbidden field %q", forbidden)
		}
	}
}

func TestLegacyAdminAssetsAreNotReachable(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})
	for _, path := range []string{"/admin/static/admin.css", "/admin/static/admin.js"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("legacy asset %s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}
}

func TestServerPreservesAndServesRegstairLogo(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/regstair-logo.png", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("logo status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("logo content type = %q, want image/png", got)
	}
	want, err := adminAssets.ReadFile("frontend-dist/regstair-logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.Body.Bytes(), want) {
		t.Fatal("served logo bytes differ from embedded original")
	}
}

func TestServerSetsAdminSecurityHeaders(t *testing.T) {
	server := NewServer(Config{Config: testConfig(), Repo: metadata.NewMemoryRepository()})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/sources", nil)
	server.ServeHTTP(response, request)

	checks := map[string]string{
		"Content-Security-Policy": "default-src 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	}
	for name, want := range checks {
		if got := response.Header().Get(name); !strings.Contains(got, want) {
			t.Fatalf("%s = %q, want containing %q", name, got, want)
		}
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self'") {
		t.Fatalf("Content-Security-Policy = %q, want self-hosted scripts allowed", got)
	}
}

func getJSON[T any](t *testing.T, handler http.Handler, path string) T {
	t.Helper()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body %s", response.Code, http.StatusOK, response.Body.String())
	}

	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return value
}

func getHTML(t *testing.T, handler http.Handler, path string) string {
	t.Helper()
	path, _, _ = strings.Cut(path, "#")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want %d body %s", path, response.Code, http.StatusOK, response.Body.String())
	}
	return response.Body.String()
}

func dashboardLink(t *testing.T, body, relation string) string {
	t.Helper()
	pattern := regexp.MustCompile(`href="([^"]+)" rel="` + regexp.QuoteMeta(relation) + `"`)
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("dashboard missing %s link", relation)
	}
	return html.UnescapeString(match[1])
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func testConfig() config.Config {
	return config.Config{
		Version: 1,
		Sources: []config.Source{
			{ID: "internal-curated", Name: "Internal Curated", Endpoint: "http://internal-registry:5000", Type: "internal", Enabled: true},
			{ID: "external-registry", Name: "External Registry", Endpoint: "http://external-registry:5000", Type: "external", Enabled: true},
		},
		Routes: []config.Route{
			{
				Name:       "curated-library",
				Match:      "library/**",
				Precedence: 10,
				Pull: config.Pull{
					Sources:          []string{"internal-curated", "external-registry"},
					Authoritative:    "internal-curated",
					ExternalFallback: true,
				},
				Push: config.Push{Destination: "internal-curated"},
			},
			{
				Name:       "protected-platform",
				Match:      "platform/**",
				Precedence: 20,
				Pull: config.Pull{
					Sources:          []string{"internal-curated"},
					Authoritative:    "internal-curated",
					ExternalFallback: false,
				},
				Push: config.Push{Destination: "internal-curated"},
			},
		},
	}
}
