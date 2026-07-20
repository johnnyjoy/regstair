package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHarborCredentialVerifierChecksPullAndPushTokenScope(t *testing.T) {
	var pull, pushScope bool
	client := &http.Client{Transport: authRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/" && r.Header.Get("Authorization") == "" {
			return authResponse(http.StatusUnauthorized, map[string]string{"WWW-Authenticate": `Bearer realm="https://harbor.test/service/token",service="harbor-registry"`}), nil
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "alice" || password != "secret" {
			return authResponse(http.StatusUnauthorized, nil), nil
		}
		switch {
		case r.URL.Path == "/service/token":
			pushScope = r.URL.Query().Get("scope") == "repository:check/repo:pull,push"
			payload := base64.RawURLEncoding.EncodeToString([]byte(`{"access":[{"type":"repository","name":"check/repo","actions":["pull","push"]}]}`))
			return authBodyResponse(http.StatusOK, nil, `{"token":"e30.`+payload+`.signature","expires_in":300}`), nil
		case r.URL.Path == "/v2/":
			return authResponse(http.StatusOK, nil), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v2/check/repo/manifests/__regstair_credential_check__":
			pull = true
			return authResponse(http.StatusNotFound, nil), nil
		default:
			return authResponse(http.StatusNotFound, nil), nil
		}
	})}
	verifier := NewHarborCredentialVerifier(client)
	err := verifier.Verify(context.Background(), VerificationRequest{SourceID: "harbor", Endpoint: "https://harbor.test", Repository: "check/repo", Username: "alice", Secret: []byte("secret"), Pull: true, Push: true})
	if err != nil || !pull || !pushScope {
		t.Fatalf("Verify() = %v pull/scope=%v/%v", err, pull, pushScope)
	}
}

func TestHarborCredentialVerifierClassifiesAuthenticationAndPermission(t *testing.T) {
	for _, tt := range []struct {
		status int
		target error
	}{{http.StatusUnauthorized, ErrUpstreamCredentials}, {http.StatusForbidden, ErrUpstreamPermission}} {
		client := &http.Client{Transport: authRoundTripFunc(func(*http.Request) (*http.Response, error) { return authResponse(tt.status, nil), nil })}
		err := NewHarborCredentialVerifier(client).Verify(context.Background(), VerificationRequest{SourceID: "harbor", Endpoint: "https://harbor.test", Repository: "check/repo", Username: "alice", Secret: []byte("secret"), Pull: true})
		if !errors.Is(err, tt.target) {
			t.Fatalf("status %d error = %v, want %v", tt.status, err, tt.target)
		}
	}
}

type authRoundTripFunc func(*http.Request) (*http.Response, error)

func (f authRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func authResponse(status int, headers map[string]string) *http.Response {
	return authBodyResponse(status, headers, "")
}
func authBodyResponse(status int, headers map[string]string, body string) *http.Response {
	response := &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	for key, value := range headers {
		response.Header.Set(key, value)
	}
	return response
}
