package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPConnectorHealth(t *testing.T) {
	distribution := newTestDistribution()
	connector := newTestHTTPConnector(t, distribution)

	if err := connector.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
}

func TestHTTPConnectorResolvesManifest(t *testing.T) {
	distribution := newTestDistribution()
	distribution.manifests["library/nginx@1.27"] = testHTTPManifest()
	connector := newTestHTTPConnector(t, distribution)

	manifest, err := connector.ResolveManifest(context.Background(), "library/nginx", "1.27")
	if err != nil {
		t.Fatalf("ResolveManifest() error = %v", err)
	}
	if got, want := manifest.Digest, httpManifestDigest; got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	if got, want := manifest.BlobDigests, []string{httpConfigDigest, httpBlobDigest}; !sameStrings(got, want) {
		t.Fatalf("blob digests = %#v, want %#v", got, want)
	}
}

func TestHTTPConnectorKeepsManifestBodyMediaTypeWhenHeaderDiffers(t *testing.T) {
	distribution := newTestDistribution()
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"digest":"` + httpConfigDigest + `"},"layers":[{"digest":"` + httpBlobDigest + `"}]}`)
	manifest, err := ParseManifest(body)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	distribution.manifests["library/nginx@1.27"] = manifest
	distribution.contentTypes = map[string]string{
		"library/nginx@1.27": "application/vnd.oci.image.manifest.v1+json",
	}
	connector := newTestHTTPConnector(t, distribution)

	got, err := connector.ResolveManifest(context.Background(), "library/nginx", "1.27")
	if err != nil {
		t.Fatalf("ResolveManifest() error = %v", err)
	}
	if got.MediaType != "application/vnd.docker.distribution.manifest.v2+json" {
		t.Fatalf("media type = %q, want manifest body media type", got.MediaType)
	}
}

func TestHTTPConnectorReturnsNotFoundForMissingManifest(t *testing.T) {
	connector := newTestHTTPConnector(t, newTestDistribution())

	_, err := connector.ResolveManifest(context.Background(), "library/nginx", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveManifest() error = %v, want ErrNotFound", err)
	}
}

func TestHTTPConnectorReturnsAuthenticationError(t *testing.T) {
	connector, err := NewHTTPConnector("source", "http://registry.test", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, nil, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewHTTPConnector() error = %v", err)
	}

	_, err = connector.ResolveManifest(context.Background(), "secure/base", "1.0")
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("ResolveManifest() error = %v, want ErrAuthentication", err)
	}
}

func TestHTTPConnectorSeparatesForbiddenFromUnauthorized(t *testing.T) {
	for _, tt := range []struct {
		status int
		target error
	}{{http.StatusUnauthorized, ErrAuthentication}, {http.StatusForbidden, ErrAuthorization}} {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tt.status, nil, nil), nil })}
		connector, err := NewHTTPConnector("harbor", "https://harbor.test", client)
		if err != nil {
			t.Fatal(err)
		}
		_, err = connector.ResolveManifest(context.Background(), "check/repo", "tag")
		if !errors.Is(err, tt.target) {
			t.Fatalf("status %d error = %v, want %v", tt.status, err, tt.target)
		}
	}
}

func TestHTTPConnectorOpenBlob(t *testing.T) {
	distribution := newTestDistribution()
	distribution.blobs[httpBlobDigest] = []byte("hello regstair")
	connector := newTestHTTPConnector(t, distribution)

	rc, desc, err := connector.OpenBlob(context.Background(), "library/nginx", httpBlobDigest)
	if err != nil {
		t.Fatalf("OpenBlob() error = %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(body), "hello regstair"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := desc.Size, int64(14); got != want {
		t.Fatalf("size = %d, want %d", got, want)
	}
}

func TestHTTPConnectorPutsBlobAndManifest(t *testing.T) {
	distribution := newTestDistribution()
	connector := newTestHTTPConnector(t, distribution)

	if _, err := connector.PutBlob(context.Background(), "team-a/service", httpConfigDigest, bytes.NewReader([]byte("config"))); err != nil {
		t.Fatalf("PutBlob(config) error = %v", err)
	}
	if _, err := connector.PutBlob(context.Background(), "team-a/service", httpBlobDigest, bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("PutBlob(layer) error = %v", err)
	}
	if _, err := connector.PutManifest(context.Background(), "team-a/service", "4.1", testHTTPManifest()); err != nil {
		t.Fatalf("PutManifest() error = %v", err)
	}

	got, ok := distribution.manifests["team-a/service@4.1"]
	if !ok {
		t.Fatal("published manifest not stored by test distribution")
	}
	if got.Digest != httpManifestDigest {
		t.Fatalf("published digest = %q, want %q", got.Digest, httpManifestDigest)
	}
}

func TestHTTPConnectorMapsTransportFailureToUnavailable(t *testing.T) {
	connector, err := NewHTTPConnector("source", "http://registry.test", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		}),
	})
	if err != nil {
		t.Fatalf("NewHTTPConnector() error = %v", err)
	}

	err = connector.Health(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Health() error = %v, want ErrUnavailable", err)
	}
}

func TestHTTPConnectorAppliesBasicAuth(t *testing.T) {
	var got string
	connector, err := NewHTTPConnector("source", "http://registry.test", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			got = r.Header.Get("Authorization")
			return response(http.StatusOK, nil, nil), nil
		}),
	}, WithBasicAuth("regstair", "secret"), WithPreemptiveBasicAuth())
	if err != nil {
		t.Fatalf("NewHTTPConnector() error = %v", err)
	}

	if err := connector.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("regstair:secret"))
	if got != want {
		t.Fatalf("authorization header = %q, want %q", got, want)
	}
}

func TestHTTPConnectorChallengeStrategyKeepsPublicPullAnonymous(t *testing.T) {
	var authorization string
	connector, err := NewHTTPConnector("public", "https://registry.test", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		authorization = request.Header.Get("Authorization")
		return response(http.StatusOK, []byte(`{"schemaVersion":2}`), map[string]string{"Content-Type": "application/vnd.oci.image.manifest.v1+json"}), nil
	})}, WithBasicAuth("alice", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.ResolveManifest(context.Background(), "library/alpine", "edge"); err != nil {
		t.Fatalf("ResolveManifest() error = %v", err)
	}
	if authorization != "" {
		t.Fatalf("public request authorization = %q, want empty", authorization)
	}
}

func TestHTTPConnectorExchangesBearerChallengeAndCachesToken(t *testing.T) {
	var tokenRequests int
	var registryRequests int
	connector, err := NewHTTPConnector("harbor", "http://harbor.test", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "auth.test" {
				tokenRequests++
				username, password, ok := r.BasicAuth()
				if !ok || username != "robot$regstair" || password != "robot-secret" {
					return response(http.StatusUnauthorized, nil, nil), nil
				}
				if got := r.URL.Query().Get("scope"); got != "repository:regstair/base:pull,push" {
					t.Fatalf("token scope = %q", got)
				}
				return response(http.StatusOK, []byte(`{"token":"harbor-token","expires_in":300}`), nil), nil
			}
			registryRequests++
			if r.Header.Get("Authorization") == "Bearer harbor-token" {
				return response(http.StatusNotFound, nil, nil), nil
			}
			return response(http.StatusUnauthorized, nil, map[string]string{
				"WWW-Authenticate": `Bearer realm="http://auth.test/service/token",service="harbor-registry",scope="repository:regstair/base:pull,push"`,
			}), nil
		}),
	}, WithBasicAuth("robot$regstair", "robot-secret"), WithAllowedTokenHosts("auth.test"))
	if err != nil {
		t.Fatalf("NewHTTPConnector() error = %v", err)
	}

	for range 2 {
		_, err = connector.ResolveManifest(context.Background(), "regstair/base", "1.0")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("ResolveManifest() error = %v, want ErrNotFound", err)
		}
	}
	if tokenRequests != 1 {
		t.Fatalf("token requests = %d, want 1", tokenRequests)
	}
	if registryRequests != 4 {
		t.Fatalf("registry requests = %d, want 4", registryRequests)
	}
}

func TestHTTPConnectorExchangesAnonymousBearerChallenge(t *testing.T) {
	var tokenAuthorization string
	connector, err := NewHTTPConnector("docker-hub", "https://registry.test", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "auth.test" {
				tokenAuthorization = r.Header.Get("Authorization")
				return response(http.StatusOK, []byte(`{"token":"anonymous-token","expires_in":300}`), nil), nil
			}
			if r.Header.Get("Authorization") == "Bearer anonymous-token" {
				return response(http.StatusOK, []byte(`{"schemaVersion":2}`), map[string]string{"Content-Type": "application/vnd.oci.image.manifest.v1+json"}), nil
			}
			return response(http.StatusUnauthorized, nil, map[string]string{
				"WWW-Authenticate": `Bearer realm="https://auth.test/token",service="registry.test",scope="repository:library/alpine:pull"`,
			}), nil
		}),
	}, WithAllowedTokenHosts("auth.test"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := connector.ResolveManifest(context.Background(), "library/alpine", "latest"); err != nil {
		t.Fatalf("ResolveManifest() error = %v", err)
	}
	if tokenAuthorization != "" {
		t.Fatalf("anonymous token request authorization = %q, want empty", tokenAuthorization)
	}
}

func TestHTTPConnectorRejectsUnapprovedBearerRealmWithoutSendingCredentials(t *testing.T) {
	credentialSent := false
	connector, err := NewHTTPConnector("registry", "https://registry.test", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "evil.test" {
			_, _, credentialSent = r.BasicAuth()
			return response(http.StatusOK, []byte(`{"token":"stolen"}`), nil), nil
		}
		return response(http.StatusUnauthorized, nil, map[string]string{"WWW-Authenticate": `Bearer realm="https://evil.test/token",service="registry",scope="repository:team/app:pull"`}), nil
	})}, WithBasicAuth("alice", "UPSTREAM-SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = connector.ResolveManifest(context.Background(), "team/app", "latest")
	if err == nil || credentialSent {
		t.Fatalf("ResolveManifest() error = %v, credentialSent = %v", err, credentialSent)
	}
}

func TestHTTPConnectorRejectsCrossOriginUploadLocationWithoutSendingCredentials(t *testing.T) {
	credentialSent := false
	connector, err := NewHTTPConnector("registry", "https://registry.test", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "evil.test" {
			_, _, credentialSent = r.BasicAuth()
			return response(http.StatusCreated, nil, nil), nil
		}
		return response(http.StatusAccepted, nil, map[string]string{"Location": "https://evil.test/upload/1"}), nil
	})}, WithBasicAuth("alice", "UPSTREAM-SECRET"), WithPreemptiveBasicAuth())
	if err != nil {
		t.Fatal(err)
	}
	_, err = connector.PutBlob(context.Background(), "team/app", httpConfigDigest, bytes.NewReader([]byte("config")))
	if err == nil || credentialSent {
		t.Fatalf("PutBlob() error = %v, credentialSent = %v", err, credentialSent)
	}
}

func newTestHTTPConnector(t *testing.T, distribution *testDistribution) *HTTPConnector {
	t.Helper()

	connector, err := NewHTTPConnector("source", "http://registry.test", &http.Client{Transport: distribution})
	if err != nil {
		t.Fatalf("NewHTTPConnector() error = %v", err)
	}
	return connector
}

const (
	httpConfigDigest   = "sha256:b79606fb3afea5bd1609ed40b622142f1c98125abcfe89a76a661b0e8e343910"
	httpBlobDigest     = "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"
	httpManifestDigest = "sha256:4902db1de977c33acbda4cfa8d00553482113d2bbefca635ad2a54c78352ea2e"
)

type testDistribution struct {
	manifests    map[string]Manifest
	contentTypes map[string]string
	blobs        map[string][]byte
}

func newTestDistribution() *testDistribution {
	return &testDistribution{
		manifests:    map[string]Manifest{},
		contentTypes: map[string]string{},
		blobs:        map[string][]byte{},
	}
}

func (d *testDistribution) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		return response(http.StatusOK, nil, nil), nil
	}

	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	switch {
	case strings.Contains(path, "/manifests/"):
		repository, reference, _ := strings.Cut(path, "/manifests/")
		return d.handleManifest(r, repository, reference)
	case strings.Contains(path, "/blobs/uploads"):
		return d.handleBlobUpload(r)
	case strings.Contains(path, "/blobs/"):
		_, digest, _ := strings.Cut(path, "/blobs/")
		return d.handleBlob(r, digest)
	default:
		return response(http.StatusNotFound, nil, nil), nil
	}
}

func (d *testDistribution) handleManifest(r *http.Request, repository string, reference string) (*http.Response, error) {
	switch r.Method {
	case http.MethodGet:
		manifest, ok := d.manifests[repository+"@"+reference]
		if !ok {
			return response(http.StatusNotFound, nil, nil), nil
		}
		contentType := manifest.MediaType
		if override := d.contentTypes[repository+"@"+reference]; override != "" {
			contentType = override
		}
		return response(http.StatusOK, manifest.Content, map[string]string{
			"Content-Type":          contentType,
			"Docker-Content-Digest": manifest.Digest,
		}), nil
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		manifest, err := ParseManifest(body)
		if err != nil {
			return response(http.StatusBadRequest, []byte(err.Error()), nil), nil
		}
		d.manifests[repository+"@"+reference] = manifest
		d.manifests[repository+"@"+manifest.Digest] = manifest
		return response(http.StatusCreated, nil, map[string]string{"Docker-Content-Digest": manifest.Digest}), nil
	default:
		return response(http.StatusMethodNotAllowed, nil, nil), nil
	}
}

func (d *testDistribution) handleBlobUpload(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost {
		return response(http.StatusAccepted, nil, map[string]string{"Location": "/v2/team-a/service/blobs/uploads/1"}), nil
	}
	if r.Method != http.MethodPut {
		return response(http.StatusMethodNotAllowed, nil, nil), nil
	}
	digest := r.URL.Query().Get("digest")
	if digest == "" {
		return response(http.StatusBadRequest, []byte("missing digest"), nil), nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	d.blobs[digest] = body
	return response(http.StatusCreated, nil, map[string]string{"Docker-Content-Digest": digest}), nil
}

func (d *testDistribution) handleBlob(r *http.Request, digest string) (*http.Response, error) {
	body, ok := d.blobs[digest]
	if !ok {
		return response(http.StatusNotFound, nil, nil), nil
	}
	return response(http.StatusOK, body, map[string]string{
		"Docker-Content-Digest": digest,
		"Content-Length":        fmt.Sprintf("%d", len(body)),
	}), nil
}

func response(status int, body []byte, headers map[string]string) *http.Response {
	resp := &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	for key, value := range headers {
		resp.Header.Set(key, value)
	}
	resp.ContentLength = int64(len(body))
	return resp
}

func testHTTPManifest() Manifest {
	body := []byte(`{"schemaVersion":2,"config":{"digest":"` + httpConfigDigest + `"},"layers":[{"digest":"` + httpBlobDigest + `"}]}`)
	return Manifest{
		Descriptor: Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    httpManifestDigest,
			Size:      int64(len(body)),
		},
		Content:     bytes.Clone(body),
		BlobDigests: []string{httpConfigDigest, httpBlobDigest},
	}
}

func sameStrings(a, b []string) bool {
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
