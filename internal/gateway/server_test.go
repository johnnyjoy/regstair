package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"regstair/internal/content"
	"regstair/internal/identity"
	"regstair/internal/registry"
	"regstair/internal/resolution"
	"regstair/internal/security"
)

const (
	gatewayConfigDigest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	gatewayBlobDigest     = "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"
	gatewayManifestDigest = "sha256:08f6f171f91c3195accee237259f1369de38d56933d06f29463c35eb66a1015c"
)

func TestGatewayV2Ping(t *testing.T) {
	server := newTestServer(t)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Header().Get("Docker-Distribution-API-Version"), "registry/2.0"; got != want {
		t.Fatalf("api version header = %q, want %q", got, want)
	}
}

func TestGatewayAdvertisesBearerAuthWhenAuthenticatorConfigured(t *testing.T) {
	server := newTestServer(t, WithAuthenticator(staticAuthenticator{
		username: "ci",
		password: "secret",
		identity: "ci-builder",
	}))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := response.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") || !strings.Contains(got, "/auth/token") {
		t.Fatalf("WWW-Authenticate = %q, want Bearer token challenge", got)
	}
}

func TestGatewayAnonymousBearerTokenPreservesAnonymousPrecedence(t *testing.T) {
	server := newTestServer(t, WithAuthenticator(staticAuthenticator{username: "alice", password: "token", identity: "user-1"}))
	anonymous := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	request.Header.Set("Authorization", "Bearer anonymous")
	server.ServeHTTP(anonymous, request)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous ping status = %d body=%s", anonymous.Code, anonymous.Body.String())
	}
	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	invalidRequest.Header.Set("Authorization", "Bearer wrong")
	server.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid presented credential status = %d", invalid.Code)
	}
}

func TestGatewayAuthenticationRateLimitPreservesAnonymousRequestsAndRecovers(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	server := newTestServer(t,
		WithAuthenticator(staticAuthenticator{username: "alice", password: "token", identity: "user-1"}),
		WithAuthenticationLimiter(security.NewFailureLimiter(2, time.Minute, 10*time.Minute, func() time.Time { return now })),
	)
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v2/", nil)
		request.Header.Set("Authorization", "Bearer wrong")
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failed auth status = %d", response.Code)
		}
	}
	blocked := httptest.NewRecorder()
	blockedRequest := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	blockedRequest.Header.Set("Authorization", "Bearer access")
	server.ServeHTTP(blocked, blockedRequest)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" || strings.Contains(blocked.Body.String(), "alice") {
		t.Fatalf("blocked auth = %d retry=%q body=%s", blocked.Code, blocked.Header().Get("Retry-After"), blocked.Body.String())
	}
	if output := logs.String(); !strings.Contains(output, "surface=docker_auth") || strings.Contains(output, "alice") || strings.Contains(output, "token") || strings.Contains(output, "192.0.2.1") {
		t.Fatalf("rate-limit logs contain unsafe or missing diagnostics: %q", output)
	}
	anonymous := httptest.NewRecorder()
	anonymousRequest := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	anonymousRequest.Header.Set("Authorization", "Bearer anonymous")
	server.ServeHTTP(anonymous, anonymousRequest)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous request during credential block = %d", anonymous.Code)
	}
	now = now.Add(10 * time.Minute)
	recovered := httptest.NewRecorder()
	recoveredRequest := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	recoveredRequest.Header.Set("Authorization", "Bearer access")
	server.ServeHTTP(recovered, recoveredRequest)
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovered auth = %d %s", recovered.Code, recovered.Body.String())
	}
}

func TestGatewayPassesAuthenticatedClientIdentityToPuller(t *testing.T) {
	puller := &fakePuller{result: resolution.PullResult{Manifest: testGatewayManifest()}}
	server := newTestServer(t,
		WithPuller(puller),
		WithAuthenticator(staticAuthenticator{username: "ci", password: "secret", identity: "ci-builder"}),
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/library/nginx/manifests/1.27", nil)
	request.Header.Set("Authorization", "Bearer access")
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d body %s", got, want, response.Body.String())
	}
	if got, want := puller.request.Principal.EventIdentity(), "ci-builder"; got != want {
		t.Fatalf("client identity = %q, want %q", got, want)
	}
}

func TestGatewayPassesAuthenticatedClientIdentityToPusher(t *testing.T) {
	pusher := &fakePusher{}
	server := newTestServer(t,
		WithPusher(pusher),
		WithAuthenticator(staticAuthenticator{username: "ci", password: "secret", identity: "ci-builder"}),
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/v2/team-a/service/manifests/4.1", bytes.NewReader(testGatewayManifest().Content))
	request.Header.Set("Authorization", "Bearer access")
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d body %s", got, want, response.Body.String())
	}
	if got, want := pusher.request.Principal.EventIdentity(), "ci-builder"; got != want {
		t.Fatalf("client identity = %q, want %q", got, want)
	}
}

func TestGatewayServesManifestPull(t *testing.T) {
	puller := &fakePuller{result: resolution.PullResult{Manifest: testGatewayManifest()}}
	server := newTestServer(t, WithPuller(puller))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/library/nginx/manifests/1.27", nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Header().Get("Docker-Content-Digest"), gatewayManifestDigest; got != want {
		t.Fatalf("digest header = %q, want %q", got, want)
	}
	if got, want := response.Body.String(), string(testGatewayManifest().Content); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := puller.request.Repository, "library/nginx"; got != want {
		t.Fatalf("pull repository = %q, want %q", got, want)
	}
}

func TestGatewayServesBlobFromContentStore(t *testing.T) {
	server := newTestServer(t, WithPuller(&fakePuller{blob: "hello regstair"}))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/library/nginx/blobs/"+gatewayBlobDigest, nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Header().Get("Docker-Content-Digest"), gatewayBlobDigest; got != want {
		t.Fatalf("digest header = %q, want %q", got, want)
	}
	if got, want := response.Header().Get("Content-Length"), "14"; got != want {
		t.Fatalf("content length = %q, want %q", got, want)
	}
	if got, want := response.Body.String(), "hello regstair"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestGatewayServesBlobHeadWithContentLength(t *testing.T) {
	server := newTestServer(t, WithPuller(&fakePuller{blob: "hello regstair"}))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodHead, "/v2/library/nginx/blobs/"+gatewayBlobDigest, nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Header().Get("Content-Length"), "14"; got != want {
		t.Fatalf("content length = %q, want %q", got, want)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", response.Body.Len())
	}
}

func TestGatewayBlobGetAndHeadCannotBypassRepositoryAuthorization(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			puller := &fakePuller{blobErr: registry.ErrAuthorization}
			server := newTestServer(t, WithPuller(puller), WithAuthenticator(staticAuthenticator{username: "alice", password: "token", identity: "alice"}))
			response := httptest.NewRecorder()
			request := httptest.NewRequest(method, "/v2/team/private/blobs/"+gatewayBlobDigest, nil)
			request.Header.Set("Authorization", "Bearer anonymous")
			server.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden { t.Fatalf("status = %d, want 403", response.Code) }
			if puller.blobRequest.Repository != "team/private" || puller.blobRequest.Principal.Kind != identity.KindAnonymous { t.Fatalf("blob authorization request = %#v", puller.blobRequest) }
		})
	}
}

func TestGatewayReturnsOCIErrorForMissingManifest(t *testing.T) {
	server := newTestServer(t, WithPuller(&fakePuller{err: resolution.ErrResolutionNotFound}))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/library/nginx/manifests/missing", nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if !strings.Contains(response.Body.String(), "MANIFEST_UNKNOWN") {
		t.Fatalf("body = %q, want MANIFEST_UNKNOWN", response.Body.String())
	}
}

func TestGatewayCompletesBlobUploadIntoContentStore(t *testing.T) {
	store := newGatewayStore(t)
	server := newTestServer(t, WithContentStore(store))

	start := httptest.NewRecorder()
	server.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/v2/team-a/service/blobs/uploads/", nil))
	if got, want := start.Code, http.StatusAccepted; got != want {
		t.Fatalf("start status = %d, want %d", got, want)
	}

	location := start.Header().Get("Location")
	if location == "" {
		t.Fatal("upload location is empty")
	}

	patch := httptest.NewRecorder()
	server.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, location, strings.NewReader("hello ")))
	if got, want := patch.Code, http.StatusAccepted; got != want {
		t.Fatalf("patch status = %d, want %d", got, want)
	}

	complete := httptest.NewRecorder()
	server.ServeHTTP(complete, httptest.NewRequest(http.MethodPut, location+"?digest="+gatewayBlobDigest, strings.NewReader("regstair")))
	if got, want := complete.Code, http.StatusCreated; got != want {
		t.Fatalf("complete status = %d, want %d body %s", got, want, complete.Body.String())
	}

	rc, err := store.OpenBlob(context.Background(), gatewayBlobDigest)
	if err != nil {
		t.Fatalf("OpenBlob() error = %v", err)
	}
	if got, want := readCloserString(t, rc), "hello regstair"; got != want {
		t.Fatalf("stored body = %q, want %q", got, want)
	}
}

func TestGatewayPublishesManifestThroughPushResolver(t *testing.T) {
	pusher := &fakePusher{}
	server := newTestServer(t, WithPusher(pusher))
	body := testGatewayManifest().Content

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/v2/team-a/service/manifests/4.1", bytes.NewReader(body))
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d body %s", got, want, response.Body.String())
	}
	if got, want := response.Header().Get("Docker-Content-Digest"), gatewayManifestDigest; got != want {
		t.Fatalf("digest header = %q, want %q", got, want)
	}
	if got, want := pusher.request.Repository, "team-a/service"; got != want {
		t.Fatalf("push repository = %q, want %q", got, want)
	}
	if got, want := pusher.request.Reference, "4.1"; got != want {
		t.Fatalf("push reference = %q, want %q", got, want)
	}
	if got, want := pusher.request.Manifest.BlobDigests, []string{gatewayConfigDigest, gatewayBlobDigest}; !equalStrings(got, want) {
		t.Fatalf("blob digests = %#v, want %#v", got, want)
	}
}

func TestGatewayRejectsUnsupportedRoute(t *testing.T) {
	server := newTestServer(t)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/library/nginx/tags/list", nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

type fakePuller struct {
	request     resolution.PullRequest
	result      resolution.PullResult
	err         error
	blobRequest resolution.BlobRequest
	blob        string
	blobErr     error
}

func (p *fakePuller) OpenBlob(_ context.Context, request resolution.BlobRequest) (resolution.BlobResult, error) {
	p.blobRequest = request
	if p.blobErr != nil {
		return resolution.BlobResult{}, p.blobErr
	}
	if p.blob == "" {
		return resolution.BlobResult{}, content.ErrBlobNotFound
	}
	return resolution.BlobResult{Content: io.NopCloser(strings.NewReader(p.blob)), Size: int64(len(p.blob))}, nil
}

func (p *fakePuller) Pull(ctx context.Context, request resolution.PullRequest) (resolution.PullResult, error) {
	p.request = request
	if p.err != nil {
		return resolution.PullResult{}, p.err
	}
	return p.result, nil
}

type fakePusher struct {
	request resolution.PushRequest
	result  resolution.PushResult
	err     error
}

func (p *fakePusher) Push(ctx context.Context, request resolution.PushRequest) (resolution.PushResult, error) {
	p.request = request
	if p.err != nil {
		return resolution.PushResult{}, p.err
	}
	return resolution.PushResult{ManifestDigest: request.Manifest.Digest}, nil
}

type staticAuthenticator struct {
	username string
	password string
	identity string
}

func (a staticAuthenticator) Issue(_ context.Context, username, password, _ string, _ []string) (string, time.Time, error) {
	if username == "" && password == "" {
		return "anonymous", time.Now().Add(time.Minute), nil
	}
	if username != a.username || password != a.password {
		return "", time.Time{}, errors.New("invalid credentials")
	}
	return "access", time.Now().Add(time.Minute), nil
}

func (a staticAuthenticator) Authenticate(_ context.Context, token, _, _ string) (identity.Principal, error) {
	if token == "anonymous" {
		return identity.Anonymous(), nil
	}
	if token != "access" {
		return identity.Principal{}, errors.New("invalid token")
	}
	return identity.Principal{Kind: identity.KindLocalUser, ID: a.identity, Username: a.username}, nil
}

func newTestServer(t *testing.T, options ...Option) *Server {
	t.Helper()

	opts := []Option{
		WithContentStore(newGatewayStore(t)),
		WithPuller(&fakePuller{result: resolution.PullResult{Manifest: testGatewayManifest()}}),
		WithPusher(&fakePusher{}),
	}
	opts = append(opts, options...)
	server, err := NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func newGatewayStore(t *testing.T) *content.FileStore {
	t.Helper()
	store, err := content.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	return store
}

func testGatewayManifest() registry.Manifest {
	body := []byte(`{"schemaVersion":2,"config":{"digest":"` + gatewayConfigDigest + `"},"layers":[{"digest":"` + gatewayBlobDigest + `"}]}`)
	return registry.Manifest{
		Descriptor: registry.Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    gatewayManifestDigest,
			Size:      int64(len(body)),
		},
		Content:     bytes.Clone(body),
		BlobDigests: []string{gatewayBlobDigest},
	}
}

func readCloserString(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return string(body)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
