package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"regstair/internal/content"
	"regstair/internal/identity"
	"regstair/internal/policy"
	"regstair/internal/registry"
	"regstair/internal/resolution"
	"regstair/internal/security"
)

type Puller interface {
	Pull(ctx context.Context, request resolution.PullRequest) (resolution.PullResult, error)
}

type BlobPuller interface {
	OpenBlob(ctx context.Context, request resolution.BlobRequest) (resolution.BlobResult, error)
}

type Pusher interface {
	Push(ctx context.Context, request resolution.PushRequest) (resolution.PushResult, error)
}

type Authenticator interface {
	Issue(context.Context, string, string, string, []string) (string, time.Time, error)
	Authenticate(context.Context, string, string, string) (identity.Principal, error)
}

type Option func(*Server)

type Server struct {
	puller        Puller
	blobPuller    BlobPuller
	pusher        Pusher
	store         content.Store
	authenticator Authenticator
	authLimiter   *security.FailureLimiter
	uploads       map[string]*uploadSession
	nextID        uint64
	mu            sync.Mutex
}

type uploadSession struct {
	repository string
	body       bytes.Buffer
}

func NewServer(options ...Option) (*Server, error) {
	server := &Server{uploads: map[string]*uploadSession{}}
	for _, option := range options {
		option(server)
	}
	if server.store == nil {
		return nil, fmt.Errorf("content store is required")
	}
	return server, nil
}

func WithPuller(puller Puller) Option {
	return func(server *Server) {
		server.puller = puller
		if blobPuller, ok := puller.(BlobPuller); ok {
			server.blobPuller = blobPuller
		}
	}
}

func WithPusher(pusher Pusher) Option {
	return func(server *Server) {
		server.pusher = pusher
	}
}

func WithContentStore(store content.Store) Option {
	return func(server *Server) {
		server.store = store
	}
}

func WithAuthenticator(authenticator Authenticator) Option {
	return func(server *Server) {
		server.authenticator = authenticator
	}
}

func WithAuthenticationLimiter(limiter *security.FailureLimiter) Option {
	return func(server *Server) {
		server.authLimiter = limiter
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if !s.authenticate(w, r) {
		return
	}

	if r.URL.Path == "/v2/" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	repository, action, rest, ok := parseV2Path(r.URL.Path)
	if !ok {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "unsupported registry route")
		return
	}

	switch action {
	case "manifests":
		s.handleManifest(w, r, repository, rest)
	case "blobs":
		if rest == "uploads/" {
			s.handleStartUpload(w, r, repository)
			return
		}
		if strings.HasPrefix(rest, "uploads/") {
			s.handleUpload(w, r, repository, strings.TrimPrefix(rest, "uploads/"))
			return
		}
		s.handleBlob(w, r, repository, rest)
	default:
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "unsupported registry route")
	}
}

func (s *Server) ServeTokenHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if r.Method != http.MethodGet {
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	if service := r.URL.Query().Get("service"); service != "" && service != "regstair" {
		writeOCIError(w, http.StatusBadRequest, "DENIED", "invalid token service")
		return
	}
	repository, actions, ok := parseTokenScope(r.URL.Query().Get("scope"))
	if !ok {
		writeOCIError(w, http.StatusBadRequest, "DENIED", "invalid repository scope")
		return
	}
	username, secret, _ := r.BasicAuth()
	keys := gatewayAuthenticationRateKeys(r.RemoteAddr, username)
	if username != "" {
		if allowed, retry := s.authLimiter.Allow(keys...); !allowed {
			w.Header().Set("Retry-After", strconv.FormatInt(max(int64(retry.Round(time.Second)/time.Second), 1), 10))
			writeOCIError(w, http.StatusTooManyRequests, "TOOMANYREQUESTS", "authentication temporarily unavailable")
			return
		}
	}
	token, expiresAt, err := s.authenticator.Issue(r.Context(), username, secret, repository, actions)
	if err != nil {
		if username != "" {
			s.authLimiter.Failure(keys...)
		}
		writeAuthChallenge(w, r, repository, actions)
		return
	}
	if username != "" {
		s.authLimiter.Success(keys...)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "access_token": token, "expires_in": max(int64(time.Until(expiresAt).Seconds()), 1), "issued_at": time.Now().UTC().Format(time.RFC3339)})
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, repository string, reference string) {
	if reference == "" {
		writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest reference is required")
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if s.puller == nil {
			writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "pull resolver is not configured")
			return
		}
		principal := requestPrincipal(r.Context())
		result, err := s.puller.Pull(r.Context(), resolution.PullRequest{Repository: repository, Reference: reference, Principal: principal})
		if err != nil {
			writePullError(w, err)
			return
		}
		writeManifest(w, r, result.Manifest)
	case http.MethodPut:
		if s.pusher == nil {
			writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "push resolver is not configured")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeOCIError(w, http.StatusBadRequest, "MANIFEST_INVALID", "could not read manifest")
			return
		}
		manifest, err := registry.ParseManifest(body)
		if err != nil {
			writeOCIError(w, http.StatusBadRequest, "MANIFEST_INVALID", err.Error())
			return
		}
		principal := requestPrincipal(r.Context())
		result, err := s.pusher.Push(r.Context(), resolution.PushRequest{Repository: repository, Reference: reference, Manifest: manifest, Principal: principal})
		if err != nil {
			writePushError(w, err)
			return
		}
		w.Header().Set("Docker-Content-Digest", result.ManifestDigest)
		w.Header().Set("Location", "/v2/"+repository+"/manifests/"+reference)
		w.WriteHeader(http.StatusCreated)
	default:
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if s.authenticator == nil {
		return true
	}
	repository, actions := requestScope(r)
	action := ""
	if len(actions) > 0 {
		action = actions[0]
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeAuthChallenge(w, r, repository, actions)
		return false
	}
	principal, err := s.authenticator.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), repository, action)
	if err == nil && principal.Kind == identity.KindAnonymous {
		*r = *r.WithContext(context.WithValue(r.Context(), principalKey{}, principal))
		return true
	}
	keys := gatewayAuthenticationRateKeys(r.RemoteAddr, principal.Username)
	if allowed, retry := s.authLimiter.Allow(keys...); !allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(max(int64(retry.Round(time.Second)/time.Second), 1), 10))
		slog.Warn("authentication rate limit applied", "surface", "docker_auth")
		writeOCIError(w, http.StatusTooManyRequests, "TOOMANYREQUESTS", "authentication temporarily unavailable")
		return false
	}
	if err != nil {
		s.authLimiter.Failure(keys...)
		writeAuthChallenge(w, r, repository, actions)
		return false
	}
	s.authLimiter.Success(keys...)

	*r = *r.WithContext(context.WithValue(r.Context(), principalKey{}, principal))
	return true
}

func gatewayAuthenticationRateKeys(remoteAddress, username string) []string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	return []string{"docker:address:" + host, "docker:account:" + strings.ToLower(strings.TrimSpace(username))}
}

func writeAuthChallenge(w http.ResponseWriter, r *http.Request, repository string, actions []string) {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	challenge := fmt.Sprintf(`Bearer realm="%s://%s/auth/token",service="regstair"`, scheme, r.Host)
	if repository != "" && len(actions) > 0 {
		challenge += fmt.Sprintf(`,scope="repository:%s:%s"`, repository, strings.Join(actions, ","))
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeOCIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
}

func requestScope(r *http.Request) (string, []string) {
	if r.URL.Path == "/v2/" {
		return "", nil
	}
	repository, _, _, ok := parseV2Path(r.URL.Path)
	if !ok {
		return "", nil
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return repository, []string{"pull"}
	}
	return repository, []string{"push"}
}

func parseTokenScope(scope string) (string, []string, bool) {
	if scope == "" {
		return "", nil, true
	}
	parts := strings.Split(scope, ":")
	if len(parts) != 3 || parts[0] != "repository" || parts[1] == "" {
		return "", nil, false
	}
	actions := []string{}
	for _, action := range strings.Split(parts[2], ",") {
		if action != "pull" && action != "push" {
			return "", nil, false
		}
		actions = append(actions, action)
	}
	return parts[1], actions, len(actions) > 0
}

type principalKey struct{}

func requestPrincipal(ctx context.Context) identity.Principal {
	principal, ok := ctx.Value(principalKey{}).(identity.Principal)
	if !ok {
		return identity.Anonymous()
	}
	return principal
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, repository string, digest string) {
	if digest == "" {
		writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob digest is required")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}

	if s.blobPuller == nil {
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "repository-aware blob resolver is not configured")
		return
	}
	result, err := s.blobPuller.OpenBlob(r.Context(), resolution.BlobRequest{Repository: repository, Digest: digest, Principal: requestPrincipal(r.Context())})
	if errors.Is(err, content.ErrBlobNotFound) {
		writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob not found")
		return
	}
	if err != nil {
		writePullError(w, err)
		return
	}
	defer result.Content.Close()

	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", "application/octet-stream")
	if result.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.Size, 10))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = io.Copy(w, result.Content)
}

func (s *Server) blobSize(ctx context.Context, digest string) (int64, bool) {
	blobs, err := s.store.ListBlobs(ctx)
	if err != nil {
		return 0, false
	}
	for _, blob := range blobs {
		if blob.Digest == digest {
			return blob.Size, true
		}
	}
	return 0, false
}

func (s *Server) handleStartUpload(w http.ResponseWriter, r *http.Request, repository string) {
	if r.Method != http.MethodPost {
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}

	id := strconv.FormatUint(atomic.AddUint64(&s.nextID, 1), 10)
	s.mu.Lock()
	s.uploads[id] = &uploadSession{repository: repository}
	s.mu.Unlock()

	location := "/v2/" + repository + "/blobs/uploads/" + id
	w.Header().Set("Location", location)
	w.Header().Set("Range", "0-0")
	w.Header().Set("Docker-Upload-UUID", id)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, repository string, id string) {
	session := s.upload(id)
	if session == nil || session.repository != repository {
		writeOCIError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload session not found")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		if _, err := io.Copy(&session.body, r.Body); err != nil {
			writeOCIError(w, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "could not append upload content")
			return
		}
		w.Header().Set("Location", r.URL.Path)
		w.Header().Set("Range", uploadRange(session.body.Len()))
		w.Header().Set("Docker-Upload-UUID", id)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		digest := r.URL.Query().Get("digest")
		if digest == "" {
			writeOCIError(w, http.StatusBadRequest, "DIGEST_INVALID", "digest query parameter is required")
			return
		}
		if _, err := io.Copy(&session.body, r.Body); err != nil {
			writeOCIError(w, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "could not complete upload")
			return
		}
		if _, err := s.store.PutBlob(r.Context(), digest, bytes.NewReader(session.body.Bytes())); err != nil {
			writeOCIError(w, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
			return
		}
		s.deleteUpload(id)
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Location", "/v2/"+repository+"/blobs/"+digest)
		w.WriteHeader(http.StatusCreated)
	default:
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

func (s *Server) upload(id string) *uploadSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploads[id]
}

func (s *Server) deleteUpload(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.uploads, id)
}

func writeManifest(w http.ResponseWriter, r *http.Request, manifest registry.Manifest) {
	mediaType := manifest.MediaType
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", manifest.Digest)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(manifest.Content)), 10))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(manifest.Content)
	}
}

func parseV2Path(path string) (repository string, action string, rest string, ok bool) {
	if !strings.HasPrefix(path, "/v2/") {
		return "", "", "", false
	}
	trimmed := strings.TrimPrefix(path, "/v2/")
	for _, marker := range []string{"/manifests/", "/blobs/"} {
		index := strings.Index(trimmed, marker)
		if index < 0 {
			continue
		}
		repository = trimmed[:index]
		action = strings.Trim(marker, "/")
		rest = trimmed[index+len(marker):]
		return repository, action, rest, repository != "" && rest != ""
	}
	return "", "", "", false
}

func writePullError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrCredentialRequired):
		writeOCIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "a credential for the selected registry is required")
	case errors.Is(err, registry.ErrCredentialUnavailable):
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "the selected registry credential is unavailable")
	case errors.Is(err, registry.ErrAuthentication):
		writeOCIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "the selected registry rejected the credential")
	case errors.Is(err, registry.ErrAuthorization):
		writeOCIError(w, http.StatusForbidden, "DENIED", "the selected registry denied the requested operation")
	case errors.Is(err, resolution.ErrUnauthorized):
		writeOCIError(w, http.StatusForbidden, "DENIED", err.Error())
	case errors.Is(err, resolution.ErrResolutionNotFound), errors.Is(err, policy.ErrNoRoute):
		writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", err.Error())
	case errors.Is(err, resolution.ErrSourceUnavailable):
		writeOCIError(w, http.StatusBadGateway, "UNAVAILABLE", err.Error())
	default:
		writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
	}
}

func writePushError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrCredentialRequired):
		writeOCIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "a credential for the selected registry is required")
	case errors.Is(err, registry.ErrCredentialUnavailable):
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "the selected registry credential is unavailable")
	case errors.Is(err, registry.ErrAuthentication):
		writeOCIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "the selected registry rejected the credential")
	case errors.Is(err, registry.ErrAuthorization):
		writeOCIError(w, http.StatusForbidden, "DENIED", "the selected registry denied the requested operation")
	case errors.Is(err, policy.ErrPushDenied), errors.Is(err, resolution.ErrUnauthorized):
		writeOCIError(w, http.StatusForbidden, "DENIED", err.Error())
	case errors.Is(err, resolution.ErrStagedBlobMissing):
		writeOCIError(w, http.StatusBadRequest, "BLOB_UNKNOWN", err.Error())
	case errors.Is(err, resolution.ErrManifestDigestMismatch):
		writeOCIError(w, http.StatusBadRequest, "MANIFEST_INVALID", err.Error())
	case errors.Is(err, resolution.ErrSourceUnavailable):
		writeOCIError(w, http.StatusBadGateway, "UNAVAILABLE", err.Error())
	default:
		writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
	}
}

func writeOCIError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{
			"code":    code,
			"message": message,
		}},
	})
}

func uploadRange(size int) string {
	if size <= 0 {
		return "0-0"
	}
	return "0-" + strconv.Itoa(size-1)
}
