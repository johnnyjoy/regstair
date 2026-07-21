package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HTTPConnector struct {
	name              string
	endpoint          *url.URL
	client            *http.Client
	basic             *basicAuth
	preemptiveBasic   bool
	allowedTokenHosts map[string]struct{}
	tokens            map[string]cachedToken
	tokenMu           sync.Mutex
}

type HTTPOption func(*HTTPConnector)

type basicAuth struct {
	username string
	password string
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

func WithBasicAuth(username string, password string) HTTPOption {
	return func(c *HTTPConnector) {
		c.basic = &basicAuth{username: username, password: password}
	}
}

func WithPreemptiveBasicAuth() HTTPOption {
	return func(c *HTTPConnector) { c.preemptiveBasic = true }
}

func WithAllowedTokenHosts(hosts ...string) HTTPOption {
	return func(c *HTTPConnector) {
		for _, host := range hosts {
			c.allowedTokenHosts[strings.ToLower(host)] = struct{}{}
		}
	}
}

func NewHTTPConnector(name string, endpoint string, client *http.Client, options ...HTTPOption) (*HTTPConnector, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse registry endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("registry endpoint must include scheme and host")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	connector := &HTTPConnector{name: name, endpoint: parsed, client: client, tokens: map[string]cachedToken{}, allowedTokenHosts: map[string]struct{}{strings.ToLower(parsed.Host): {}}}
	for _, option := range options {
		option(connector)
	}
	return connector, nil
}

func (c *HTTPConnector) Name() string {
	return c.name
}

func (c *HTTPConnector) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/v2/"), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.doAuthenticated(req)
	if err != nil {
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrAuthorization) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if err := registryAccessError(resp.StatusCode); err != nil {
		return err
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrNotFound, resp.StatusCode)
	}
	return nil
}

func (c *HTTPConnector) ResolveManifest(ctx context.Context, repository string, reference string) (Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/v2/"+repository+"/manifests/"+reference), nil)
	if err != nil {
		return Manifest{}, err
	}
	c.authorize(req)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	}, ", "))

	resp, err := c.doAuthenticated(req)
	if err != nil {
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrAuthorization) {
			return Manifest{}, err
		}
		return Manifest{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if err := registryAccessError(resp.StatusCode); err != nil {
		return Manifest{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return Manifest{}, fmt.Errorf("%w: manifest %s:%s", ErrNotFound, repository, reference)
	}
	if resp.StatusCode >= 500 {
		return Manifest{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return Manifest{}, fmt.Errorf("resolve manifest status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest response: %w", err)
	}
	manifest, err := ParseManifest(body)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest response: %w", err)
	}
	if digest := resp.Header.Get("Docker-Content-Digest"); digest != "" {
		manifest.Digest = digest
	}
	return manifest, nil
}

func (c *HTTPConnector) OpenBlob(ctx context.Context, repository string, digest string) (io.ReadCloser, Descriptor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/v2/"+repository+"/blobs/"+digest), nil)
	if err != nil {
		return nil, Descriptor{}, err
	}
	c.authorize(req)
	resp, err := c.doAuthenticated(req)
	if err != nil {
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrAuthorization) {
			return nil, Descriptor{}, err
		}
		return nil, Descriptor{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := registryAccessError(resp.StatusCode); err != nil {
		resp.Body.Close()
		return nil, Descriptor{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, Descriptor{}, fmt.Errorf("%w: blob %s", ErrNotFound, digest)
	}
	if resp.StatusCode >= 500 {
		resp.Body.Close()
		return nil, Descriptor{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, Descriptor{}, fmt.Errorf("open blob status %d", resp.StatusCode)
	}

	size := resp.ContentLength
	if size < 0 {
		if header := resp.Header.Get("Content-Length"); header != "" {
			if parsed, err := strconv.ParseInt(header, 10, 64); err == nil {
				size = parsed
			}
		}
	}
	return resp.Body, Descriptor{Digest: digest, Size: size}, nil
}

func (c *HTTPConnector) PutBlob(ctx context.Context, repository string, digest string, body io.Reader) (Descriptor, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read blob: %w", err)
	}
	if got := digestBytes(content); got != digest {
		return Descriptor{}, fmt.Errorf("%w: got %s want %s", ErrDigestMismatch, got, digest)
	}

	location := c.url("/v2/" + repository + "/blobs/uploads/")
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, location, nil)
	if err != nil {
		return Descriptor{}, err
	}
	c.authorize(startReq)
	startResp, err := c.doAuthenticated(startReq)
	if err != nil {
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrAuthorization) {
			return Descriptor{}, err
		}
		return Descriptor{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer startResp.Body.Close()
	if err := registryAccessError(startResp.StatusCode); err != nil {
		return Descriptor{}, err
	}
	if startResp.StatusCode >= 500 {
		return Descriptor{}, fmt.Errorf("%w: status %d", ErrUnavailable, startResp.StatusCode)
	}
	if startResp.StatusCode >= 400 {
		return Descriptor{}, fmt.Errorf("start blob upload status %d", startResp.StatusCode)
	}

	uploadURL := startResp.Header.Get("Location")
	if uploadURL == "" {
		uploadURL = location
	}
	uploadURL, err = c.resolveLocation(uploadURL)
	if err != nil {
		return Descriptor{}, err
	}
	separator := "?"
	if strings.Contains(uploadURL, "?") {
		separator = "&"
	}
	uploadURL += separator + "digest=" + url.QueryEscape(digest)

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return Descriptor{}, err
	}
	c.authorize(putReq)
	putResp, err := c.doAuthenticated(putReq)
	if err != nil {
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrAuthorization) {
			return Descriptor{}, err
		}
		return Descriptor{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer putResp.Body.Close()
	if err := registryAccessError(putResp.StatusCode); err != nil {
		return Descriptor{}, err
	}
	if putResp.StatusCode >= 500 {
		return Descriptor{}, fmt.Errorf("%w: status %d", ErrUnavailable, putResp.StatusCode)
	}
	if putResp.StatusCode >= 400 {
		return Descriptor{}, fmt.Errorf("complete blob upload status %d", putResp.StatusCode)
	}
	return Descriptor{Digest: digest, Size: int64(len(content))}, nil
}

func (c *HTTPConnector) VerifyPushScope(ctx context.Context, repository string) error {
	if c.basic == nil {
		return fmt.Errorf("%w: credentials are required", ErrAuthentication)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/v2/"), nil)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("registry did not provide an authentication challenge for scope verification")
	}
	realm, service, _, ok := parseBearerChallenge(response.Header.Get("WWW-Authenticate"))
	if !ok {
		return fmt.Errorf("registry did not provide a Bearer challenge for scope verification")
	}
	realmURL, err := url.Parse(realm)
	if err != nil {
		return fmt.Errorf("parse registry token realm: %w", err)
	}
	scope := "repository:" + repository + ":pull,push"
	token, err := c.bearerToken(ctx, realmURL.String(), service, scope)
	if err != nil {
		return err
	}
	granted, err := tokenGrantsRepositoryAction(token, repository, "push")
	if err != nil {
		return fmt.Errorf("inspect registry token scope: %w", err)
	}
	if !granted {
		return fmt.Errorf("%w: push scope was not granted", ErrAuthorization)
	}
	return nil
}

func tokenGrantsRepositoryAction(token, repository, action string) (bool, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false, fmt.Errorf("registry token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Access []struct {
			Type    string   `json:"type"`
			Name    string   `json:"name"`
			Actions []string `json:"actions"`
		} `json:"access"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false, fmt.Errorf("decode JWT claims: %w", err)
	}
	for _, access := range claims.Access {
		if access.Type == "repository" && access.Name == repository {
			for _, granted := range access.Actions {
				if granted == action {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func registryAccessError(status int) error {
	if status == http.StatusUnauthorized {
		return fmt.Errorf("%w: status %d", ErrAuthentication, status)
	}
	if status == http.StatusForbidden {
		return fmt.Errorf("%w: status %d", ErrAuthorization, status)
	}
	return nil
}

func (c *HTTPConnector) PutManifest(ctx context.Context, repository string, reference string, manifest Manifest) (Descriptor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url("/v2/"+repository+"/manifests/"+reference), bytes.NewReader(manifest.Content))
	if err != nil {
		return Descriptor{}, err
	}
	c.authorize(req)
	mediaType := manifest.MediaType
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}
	req.Header.Set("Content-Type", mediaType)

	resp, err := c.doAuthenticated(req)
	if err != nil {
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrAuthorization) {
			return Descriptor{}, err
		}
		return Descriptor{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if err := registryAccessError(resp.StatusCode); err != nil {
		return Descriptor{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return Descriptor{}, fmt.Errorf("%w: manifest target %s:%s", ErrNotFound, repository, reference)
	}
	if resp.StatusCode >= 500 {
		return Descriptor{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return Descriptor{}, fmt.Errorf("put manifest status %d", resp.StatusCode)
	}

	desc := manifest.Descriptor
	if digest := resp.Header.Get("Docker-Content-Digest"); digest != "" {
		desc.Digest = digest
	}
	return desc, nil
}

func (c *HTTPConnector) resolveLocation(location string) (string, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		if !sameURLOrigin(parsed, c.endpoint) {
			return "", fmt.Errorf("registry upload location changed origin")
		}
		return parsed.String(), nil
	}
	base := *c.endpoint
	base.Path = path.Join(base.Path, parsed.Path)
	base.RawQuery = parsed.RawQuery
	return base.String(), nil
}

func (c *HTTPConnector) doAuthenticated(req *http.Request) (*http.Response, error) {
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	realm, service, scope, bearer := parseBearerChallenge(challenge)
	if !bearer && (c.basic == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(challenge)), "basic")) {
		return resp, nil
	}
	resp.Body.Close()
	retry, err := cloneRequest(req)
	if err != nil {
		return nil, err
	}
	if bearer {
		token, err := c.bearerToken(req.Context(), realm, service, scope)
		if err != nil {
			return nil, err
		}
		retry.Header.Set("Authorization", "Bearer "+token)
	} else {
		retry.SetBasicAuth(c.basic.username, c.basic.password)
	}
	return c.client.Do(retry)
}

func parseBearerChallenge(challenge string) (realm string, service string, scope string, ok bool) {
	challenge = strings.TrimSpace(challenge)
	if len(challenge) < len("Bearer") || !strings.EqualFold(challenge[:len("Bearer")], "Bearer") {
		return "", "", "", false
	}
	params, ok := parseChallengeParams(challenge[len("Bearer"):])
	if !ok || params["realm"] == "" {
		return "", "", "", false
	}
	return params["realm"], params["service"], params["scope"], true
}

func parseChallengeParams(value string) (map[string]string, bool) {
	params := map[string]string{}
	for index := 0; index < len(value); {
		for index < len(value) && (value[index] == ' ' || value[index] == ',') {
			index++
		}
		keyStart := index
		for index < len(value) && value[index] != '=' {
			index++
		}
		if index == len(value) {
			return nil, false
		}
		key := strings.ToLower(strings.TrimSpace(value[keyStart:index]))
		index++
		if key == "" || index == len(value) {
			return nil, false
		}

		var parsed strings.Builder
		if value[index] == '"' {
			index++
			closed := false
			for index < len(value) {
				if value[index] == '"' {
					index++
					closed = true
					break
				}
				if value[index] == '\\' && index+1 < len(value) {
					index++
				}
				parsed.WriteByte(value[index])
				index++
			}
			if !closed {
				return nil, false
			}
		} else {
			start := index
			for index < len(value) && value[index] != ',' {
				index++
			}
			parsed.WriteString(strings.TrimSpace(value[start:index]))
		}
		params[key] = parsed.String()
	}
	return params, true
}

func (c *HTTPConnector) bearerToken(ctx context.Context, realm string, service string, scope string) (string, error) {
	cacheKey := realm + "\x00" + service + "\x00" + scope
	c.tokenMu.Lock()
	if cached, ok := c.tokens[cacheKey]; ok && time.Now().Add(15*time.Second).Before(cached.expiresAt) {
		c.tokenMu.Unlock()
		return cached.value, nil
	}
	c.tokenMu.Unlock()

	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parse registry token realm: %w", err)
	}
	if tokenURL.Scheme == "" || tokenURL.Host == "" || tokenURL.User != nil || tokenURL.Fragment != "" {
		return "", fmt.Errorf("registry token realm must be an absolute URL without user info or fragment")
	}
	if c.endpoint.Scheme == "https" && tokenURL.Scheme != "https" {
		return "", fmt.Errorf("registry token realm cannot downgrade HTTPS")
	}
	if _, ok := c.allowedTokenHosts[strings.ToLower(tokenURL.Host)]; !ok {
		return "", fmt.Errorf("registry token realm host is not approved")
	}
	query := tokenURL.Query()
	if service != "" {
		query.Set("service", service)
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	tokenURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	if c.basic != nil {
		req.SetBasicAuth(c.basic.username, c.basic.password)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: request registry token: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: token service status %d", ErrAuthentication, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%w: token service status %d", ErrUnavailable, resp.StatusCode)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode registry token: %w", err)
	}
	if payload.Token == "" {
		payload.Token = payload.AccessToken
	}
	if payload.Token == "" {
		return "", fmt.Errorf("registry token response did not contain a token")
	}
	if payload.ExpiresIn <= 0 {
		payload.ExpiresIn = 300
	}
	c.tokenMu.Lock()
	c.tokens[cacheKey] = cachedToken{value: payload.Token, expiresAt: time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)}
	c.tokenMu.Unlock()
	return payload.Token, nil
}

func sameURLOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	retry := req.Clone(req.Context())
	if req.Body == nil {
		return retry, nil
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("registry request body cannot be retried after authentication challenge")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("recreate registry request body: %w", err)
	}
	retry.Body = body
	return retry, nil
}

func (c *HTTPConnector) url(suffix string) string {
	base := *c.endpoint
	base.Path = strings.TrimRight(base.Path, "/") + suffix
	return base.String()
}

func (c *HTTPConnector) authorize(req *http.Request) {
	if c.basic == nil || !c.preemptiveBasic {
		return
	}
	req.SetBasicAuth(c.basic.username, c.basic.password)
}

var _ Connector = (*HTTPConnector)(nil)
