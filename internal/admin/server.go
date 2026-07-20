package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"regstair/internal/auth"
	"regstair/internal/config"
	"regstair/internal/content"
	"regstair/internal/metadata"
	"regstair/internal/registry"
	"regstair/internal/security"
)

type Config struct {
	Config       config.Config
	Repo         metadata.Repository
	Store        content.Store
	Auth         *AuthConfig
	Connectors   map[string]registry.Connector
	LoginLimiter *security.FailureLimiter
}

type AuthConfig struct {
	Accounts    *auth.AccountService
	Sessions    *auth.WebSessionService
	Admins      *auth.AdminAccountService
	Tokens      *auth.DockerTokenService
	Credentials *auth.RegistryCredentialService
}

type Server struct {
	config       config.Config
	repo         metadata.Repository
	store        content.Store
	auth         *AuthConfig
	connectors   map[string]registry.Connector
	setupToken   string
	loginLimiter *security.FailureLimiter
}

type SourcesResponse struct {
	Sources []SourceView `json:"sources"`
}

type SourceView struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Endpoint        string                 `json:"endpoint"`
	Type            string                 `json:"type"`
	Enabled         bool                   `json:"enabled"`
	Auth            AuthView               `json:"auth"`
	UserCredentials config.UserCredentials `json:"user_credentials"`
	Routes          []string               `json:"routes"`
}

type AuthView struct {
	Mode          string `json:"mode"`
	CredentialRef string `json:"credential_ref,omitempty"`
	Configured    bool   `json:"configured"`
}

type RoutesResponse struct {
	Routes []config.Route `json:"routes"`
}

type ApprovedRegistriesResponse struct {
	Registries []ApprovedRegistryView `json:"registries"`
}
type ApprovedRegistryView struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Endpoint               string   `json:"endpoint"`
	Pull                   bool     `json:"pull"`
	Push                   bool     `json:"push"`
	VerificationRepository string   `json:"verification_repository"`
	Guidance               string   `json:"guidance,omitempty"`
	Routes                 []string `json:"routes"`
}
type SourceHealthResponse struct {
	Sources   []SourceHealthView `json:"sources"`
	CheckedAt time.Time          `json:"checked_at"`
}
type SourceHealthView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type RequestsResponse struct {
	Requests   []metadata.RequestEvent `json:"requests"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type RequestDetailResponse struct {
	Request    RequestDetailView          `json:"request"`
	Provenance *metadata.ProvenanceRecord `json:"provenance,omitempty"`
}

type RequestDetailView struct {
	ID                  int64                  `json:"id"`
	Timestamp           time.Time              `json:"timestamp"`
	Operation           metadata.Operation     `json:"operation"`
	LogicalReference    string                 `json:"logical_reference"`
	Status              metadata.RequestStatus `json:"status"`
	ClientIdentity      string                 `json:"client_identity,omitempty"`
	MatchedRoute        string                 `json:"matched_route,omitempty"`
	SourceOrDestination string                 `json:"source_or_destination,omitempty"`
	CacheResult         metadata.CacheResult   `json:"cache_result,omitempty"`
	Credential          string                 `json:"credential"`
	DurationMillis      float64                `json:"duration_ms"`
	BytesTransferred    int64                  `json:"bytes_transferred"`
	ErrorClassification string                 `json:"error_classification,omitempty"`
	Explanation         []string               `json:"explanation,omitempty"`
}

type ProvenanceResponse struct {
	Provenance *metadata.ProvenanceRecord `json:"provenance"`
}

type ArtifactsResponse struct {
	Artifacts []Artifact `json:"artifacts"`
}

type CacheResponse struct {
	Blobs []content.Descriptor `json:"blobs"`
}

type Artifact struct {
	LogicalReference string              `json:"logical_reference"`
	Mapping          metadata.TagMapping `json:"mapping"`
}

type dashboardData struct {
	StylesheetURL        string
	ScriptURL            string
	Page                 string
	PageTitle            string
	PageSubtitle         string
	SourceCount          int
	RouteCount           int
	ArtifactCount        int
	BlobCount            int
	RequestCount         int
	TotalRequestCount    int
	CacheHits            int
	CacheMisses          int
	BlockedCount         int
	PushCount            int
	Sources              []SourceView
	Routes               []config.Route
	Requests             []metadata.RequestEvent
	Artifacts            []Artifact
	GeneratedAtUTC       string
	Filters              requestFilterView
	NextURL              string
	PreviousURL          string
	HasFilters           bool
	User                 *metadata.User
	IsAdmin              bool
	LegacyView           bool
	CredentialRows       []credentialRow
	CredentialsAvailable bool
	Users                []metadata.User
	DockerTokens         []metadata.DockerToken
	AuditEvents          []metadata.AuditEvent
	HealthItems          []dashboardHealthItem
	AttentionItems       []dashboardAttentionItem
	RecentActivity       []metadata.RequestEvent
	DashboardWindow      string
	FailureRate          string
	CacheHitRatio        string
	AverageDuration      string
	P95Duration          string
	RequestDetail        *requestDetailPage
	RequestProvenance    *metadata.ProvenanceRecord
	FilterChips          []requestFilterChip
	AuditRows            []auditRow
	AuditFilters         auditFilterView
	AuditActionOptions   []auditActionOption
	AuditMatchCount      int
}

type auditRow struct {
	Event       metadata.AuditEvent
	ActionLabel string
	ActorLabel  string
	TargetLabel string
	Detail      string
}

type auditFilterView struct{ Action, Outcome, Actor, Target, Correlation string }
type auditActionOption struct{ Action, Label string }

type requestFilterChip struct{ Label, URL string }

type requestDetailPage struct {
	Event            metadata.RequestEvent
	Credential       string
	Duration         string
	BytesTransferred string
}

type dashboardHealthItem struct {
	Name   string
	Status string
	Detail string
	Tone   string
}

type dashboardAttentionItem struct {
	Title  string
	Detail string
	URL    string
	Count  int
	Tone   string
}

type credentialRow struct {
	Registry   ApprovedRegistryView
	Credential *auth.RegistryCredentialView
}

type auditRepository interface {
	ListAuditEvents(context.Context, int) ([]metadata.AuditEvent, error)
}

type requestFilterView struct {
	Reference           string
	ClientIdentity      string
	Route               string
	Operation           string
	Source              string
	Status              string
	ErrorClassification string
	CacheResult         string
	Sort                string
	CredentialSource    string
	Window              string
	After               string
	Before              string
	Limit               int
	Routes              []string
	Sources             []string
}

func NewServer(cfg Config) *Server {
	setupToken := make([]byte, 32)
	if _, err := rand.Read(setupToken); err != nil {
		panic("generate first-run setup token: " + err.Error())
	}
	return &Server{config: cfg.Config, repo: cfg.Repo, store: cfg.Store, auth: cfg.Auth, connectors: cfg.Connectors, setupToken: base64.RawURLEncoding.EncodeToString(setupToken), loginLimiter: cfg.LoginLimiter}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if target, ok := legacyPageRedirect(r.URL.Path); ok {
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
		return
	}
	setupRequired := s.auth != nil && s.bootstrapRequired(r.Context())
	if setupRequired {
		w.Header().Set("X-Regstair-Bootstrap-Required", "true")
		if s.handleFirstRun(w, r) {
			return
		}
	} else if r.URL.Path == "/setup" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	} else if r.URL.Path == "/admin/api/setup" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			writeJSON(w, http.StatusOK, map[string]bool{"required": false})
			return
		}
		writeError(w, http.StatusConflict, "first-run setup has already completed")
		return
	}
	if s.auth != nil && r.URL.Path == "/admin/api/login" {
		s.handleLogin(w, r)
		return
	}
	if r.URL.Path == "/login" {
		s.handleLoginPage(w, r)
		return
	}
	if s.auth != nil && s.requiresSession(r) {
		if isApplicationPagePath(r.URL.Path) && r.Method == http.MethodGet {
			if _, err := r.Cookie(sessionCookieName); err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		r = r.WithContext(withCurrentUser(r.Context(), user))
		if isApplicationPagePath(r.URL.Path) && user.Access != metadata.UserAccessAdmin && r.URL.Path != "/account" {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		if adminOnlyPath(r.URL.Path) && user.Access != metadata.UserAccessAdmin {
			writeError(w, http.StatusForbidden, "administrator access required")
			return
		}
	}
	if !isAllowedMutationPath(r.URL.Path) && r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/users/") {
		s.handleUserMutation(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/account/registry-credentials/") {
		s.handleRegistryCredentialMutation(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/account/docker-tokens/") {
		s.handleDockerTokenMutation(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/requests/") {
		s.handleRequestDetail(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/requests/") {
		s.handleRequestDetailPage(w, r)
		return
	}

	switch r.URL.Path {
	case "/", "/requests", "/routes", "/sources", "/cache", "/account", "/admin/users", "/admin/audit":
		s.handleDashboard(w, r)
	case "/admin/static/admin.css":
		s.handleStatic(w, r, "static/admin.css", "text/css; charset=utf-8")
	case "/admin/static/admin.js":
		s.handleStatic(w, r, "static/admin.js", "text/javascript; charset=utf-8")
	case "/admin/api/health":
		if !readMethod(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "/admin/api/account":
		s.handleAccount(w, r)
	case "/admin/api/logout":
		s.handleLogout(w, r)
	case "/admin/api/account/password":
		s.handlePasswordChange(w, r)
	case "/admin/api/account/docker-tokens":
		s.handleDockerTokens(w, r)
	case "/admin/api/account/registry-credentials":
		s.handleRegistryCredentials(w, r)
	case "/admin/api/users":
		s.handleUsers(w, r)
	case "/admin/api/sources":
		writeJSON(w, http.StatusOK, SourcesResponse{Sources: sourceViews(s.config)})
	case "/admin/api/routes":
		writeJSON(w, http.StatusOK, RoutesResponse{Routes: append([]config.Route(nil), s.config.Routes...)})
	case "/admin/api/registries":
		if !readMethod(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, ApprovedRegistriesResponse{Registries: approvedRegistryViews(s.config)})
	case "/admin/api/source-health":
		s.handleSourceHealth(w, r)
	case "/admin/api/requests":
		s.handleRequests(w, r)
	case "/admin/api/provenance":
		s.handleProvenance(w, r)
	case "/admin/api/artifacts":
		s.handleArtifacts(w, r)
	case "/admin/api/cache":
		s.handleCache(w, r)
	default:
		writeError(w, http.StatusNotFound, "admin route not found")
	}
}

func isApplicationPagePath(path string) bool {
	if strings.HasPrefix(path, "/requests/") {
		return true
	}
	switch path {
	case "/", "/requests", "/routes", "/sources", "/cache", "/account", "/admin/users", "/admin/audit":
		return true
	default:
		return false
	}
}

func legacyPageRedirect(path string) (string, bool) {
	redirects := map[string]string{
		"/admin":          "/",
		"/admin/":         "/",
		"/admin/requests": "/requests",
		"/admin/routes":   "/routes",
		"/admin/sources":  "/sources",
		"/admin/cache":    "/cache",
		"/admin/account":  "/account",
		"/admin/login":    "/login",
		"/admin/setup":    "/setup",
	}
	target, ok := redirects[path]
	return target, ok
}

func (s *Server) handleFirstRun(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/", "/login":
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return true
	case "/setup":
		s.handleSetupPage(w, r)
		return true
	case "/admin/api/setup":
		s.handleSetup(w, r)
		return true
	case "/admin/static/admin.css", "/admin/static/admin.js", "/admin/api/health":
		return false
	default:
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": map[string]string{"code": "setup_required", "message": "Complete first-run setup before using the control plane."}})
			return true
		}
	}
	return false
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var rendered bytes.Buffer
	data := map[string]string{"StylesheetURL": adminStylesheetURL, "ScriptURL": adminScriptURL, "SetupToken": s.setupToken}
	if err := setupTemplate.ExecuteTemplate(&rendered, "setup.html", data); err != nil {
		writeError(w, http.StatusInternalServerError, "render setup")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = w.Write(rendered.Bytes())
	}
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		writeJSON(w, http.StatusOK, map[string]any{"required": true, "setup_token": s.setupToken})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Header.Get("X-Regstair-Setup-Token") != s.setupToken || !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "invalid first-run setup request")
		return
	}
	var input struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	user, err := s.auth.Accounts.BootstrapAdminWithProfile(r.Context(), input.Username, input.Password, input.DisplayName, input.Email)
	input.Password = ""
	if err != nil {
		if errors.Is(err, metadata.ErrConflict) {
			writeError(w, http.StatusConflict, "first-run setup has already completed")
			return
		}
		if errors.Is(err, auth.ErrPasswordPolicy) {
			writeError(w, http.StatusBadRequest, "password must contain 15 to 128 characters")
			return
		}
		writeError(w, http.StatusBadRequest, "administrator details were not accepted")
		return
	}
	issued, err := s.auth.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "administrator created; sign in to continue")
		return
	}
	setSessionCookie(w, issued.Secret)
	setCSRFCookie(w, issued.CSRFToken)
	writeJSON(w, http.StatusCreated, loginResponse{User: *user, CSRFToken: issued.CSRFToken})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host) && parsed.Scheme == requestScheme(r)
}

func requestScheme(r *http.Request) string {
	if r.URL.Scheme != "" {
		return r.URL.Scheme
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded != "" {
		return forwarded
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Server) handleSourceHealth(w http.ResponseWriter, r *http.Request) {
	if !readMethod(w, r) {
		return
	}
	response := SourceHealthResponse{CheckedAt: time.Now().UTC(), Sources: make([]SourceHealthView, 0, len(s.config.Sources))}
	for _, source := range s.config.Sources {
		status := "disabled"
		if source.Enabled {
			connector, ok := s.connectors[source.ID]
			if !ok {
				status = "not_configured"
			} else {
				ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				err := connector.Health(ctx)
				cancel()
				if err == nil {
					status = "healthy"
				} else {
					status = "unavailable"
				}
			}
		}
		response.Sources = append(response.Sources, SourceHealthView{ID: source.ID, Status: status})
	}
	writeJSON(w, http.StatusOK, response)
}

func adminOnlyPath(path string) bool {
	if path == "/" || path == "/account" || path == "/admin/api/account" || strings.HasPrefix(path, "/admin/api/account/") || path == "/admin/api/registries" || path == "/admin/api/logout" {
		return false
	}
	return true
}

func (s *Server) bootstrapRequired(ctx context.Context) bool {
	users, err := s.repo.ListUsers(ctx)
	return err == nil && len(users) == 0
}

func (s *Server) requiresSession(r *http.Request) bool {
	if r.URL.Path == "/login" || r.URL.Path == "/admin/api/login" || strings.HasPrefix(r.URL.Path, "/admin/static/") {
		return false
	}
	if r.URL.Path == "/admin/api/account" || strings.HasPrefix(r.URL.Path, "/admin/api/account/") || r.URL.Path == "/admin/api/users" || strings.HasPrefix(r.URL.Path, "/admin/api/users/") || r.URL.Path == "/admin/api/registries" {
		return true
	}
	users, err := s.repo.ListUsers(r.Context())
	return err != nil || len(users) > 0
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var rendered bytes.Buffer
	data := map[string]string{"StylesheetURL": adminStylesheetURL, "ScriptURL": adminScriptURL}
	if err := loginTemplate.ExecuteTemplate(&rendered, "login.html", data); err != nil {
		writeError(w, http.StatusInternalServerError, "render login")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = w.Write(rendered.Bytes())
	}
}

func approvedRegistryViews(cfg config.Config) []ApprovedRegistryView {
	views := []ApprovedRegistryView{}
	for _, source := range cfg.Sources {
		policy := source.UserCredentials
		if !source.Enabled || !policy.Approved {
			continue
		}
		routes := []string{}
		pullRelevant, pushRelevant := false, false
		for _, route := range cfg.Routes {
			used := false
			if policy.Pull {
				for _, sourceID := range route.Pull.Sources {
					if sourceID == source.ID {
						pullRelevant, used = true, true
						break
					}
				}
			}
			if policy.Push && route.Push.Destination == source.ID && !route.Push.Deny {
				pushRelevant, used = true, true
			}
			if used {
				routes = append(routes, route.Name)
			}
		}
		if !pullRelevant && !pushRelevant {
			continue
		}
		views = append(views, ApprovedRegistryView{ID: source.ID, Name: source.Name, Endpoint: source.Endpoint, Pull: pullRelevant, Push: pushRelevant, VerificationRepository: policy.VerificationRepository, Guidance: policy.Guidance, Routes: routes})
	}
	return views
}

const sessionCookieName = "regstair_session"
const csrfCookieName = "regstair_csrf"

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type loginResponse struct {
	User      metadata.User `json:"user"`
	CSRFToken string        `json:"csrf_token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input loginRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	keys := authenticationRateKeys("admin", r.RemoteAddr, input.Username)
	if allowed, retry := s.loginLimiter.Allow(keys...); !allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(max(int64(retry.Seconds()), 1), 10))
		slog.Warn("authentication rate limit applied", "surface", "admin_login")
		writeError(w, http.StatusTooManyRequests, "invalid username or password")
		return
	}
	user, err := s.auth.Accounts.AuthenticateWeb(r.Context(), input.Username, input.Password)
	if err != nil {
		s.loginLimiter.Failure(keys...)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.loginLimiter.Success(keys...)
	issued, err := s.auth.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	setSessionCookie(w, issued.Secret)
	setCSRFCookie(w, issued.CSRFToken)
	writeJSON(w, http.StatusOK, loginResponse{User: *user, CSRFToken: issued.CSRFToken})
}

func authenticationRateKeys(surface, remoteAddress, username string) []string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	return []string{surface + ":address:" + host, surface + ":account:" + strings.ToLower(strings.TrimSpace(username))}
}

func setSessionCookie(w http.ResponseWriter, secret string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: secret, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
}

func setCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: token, Path: "/", HttpOnly: false, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*metadata.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	user, err := s.auth.Sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		clearSessionCookie(w)
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return user, true
}

func (s *Server) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if _, err := s.auth.Sessions.Validate(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")); err != nil {
		writeError(w, http.StatusForbidden, "invalid CSRF token")
		return false
	}
	return true
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if !readMethod(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, currentUser(r.Context()))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	_ = s.auth.Sessions.Revoke(r.Context(), cookie.Value)
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	var input struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	user := currentUser(r.Context())
	if err := s.auth.Accounts.ChangePassword(r.Context(), user.ID, input.Current, input.New); err != nil {
		writeError(w, http.StatusBadRequest, "password change failed")
		return
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDockerTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		tokens, err := s.auth.Tokens.List(r.Context(), currentUser(r.Context()).ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list Docker tokens")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	var input struct {
		Label         string `json:"label"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if input.ExpiresInDays == 0 {
		input.ExpiresInDays = 30
	}
	user := currentUser(r.Context())
	issued, err := s.auth.Tokens.Issue(r.Context(), user.ID, input.Label, time.Duration(input.ExpiresInDays)*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Docker token request was not accepted")
		return
	}
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) handleDockerTokenMutation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/api/account/docker-tokens/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	if err := s.auth.Tokens.RevokeForUser(r.Context(), currentUser(r.Context()).ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke Docker token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRegistryCredentials(w http.ResponseWriter, r *http.Request) {
	if !readMethod(w, r) {
		return
	}
	if s.auth.Credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "registry credential storage is unavailable")
		return
	}
	views, err := s.auth.Credentials.List(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list registry credentials")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": views})
}

func (s *Server) handleRegistryCredentialMutation(w http.ResponseWriter, r *http.Request) {
	if s.auth.Credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "registry credential storage is unavailable")
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	remainder := strings.TrimPrefix(r.URL.Path, "/admin/api/account/registry-credentials/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "verify-and-save" && r.Method == http.MethodPost {
		var input struct {
			Username string `json:"username"`
			Secret   string `json:"secret"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		secret := []byte(input.Secret)
		input.Secret = ""
		defer clear(secret)
		view, err := s.auth.Credentials.VerifyAndSave(r.Context(), currentUser(r.Context()).ID, parts[0], input.Username, secret)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
		var input struct {
			Confirm bool `json:"confirm"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		if err := s.auth.Credentials.Remove(r.Context(), currentUser(r.Context()).ID, parts[0], input.Confirm); err != nil {
			writeCredentialError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeCredentialError(w http.ResponseWriter, err error) {
	var public *security.PublicError
	if errors.As(err, &public) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": public.Code, "message": public.Message}})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "internal_error", "message": "The credential operation could not be completed."}})
}

func writeAccountMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "administrator access required")
	case errors.Is(err, auth.ErrLastAdministrator):
		writeError(w, http.StatusBadRequest, "the last enabled administrator cannot be changed; create or promote another administrator first")
	case errors.Is(err, auth.ErrPasswordPolicy):
		writeError(w, http.StatusBadRequest, "password must contain 15 to 128 characters")
	case errors.Is(err, metadata.ErrConflict):
		writeError(w, http.StatusBadRequest, "the account changed or conflicts with an existing account")
	case errors.Is(err, metadata.ErrInvalidRecord):
		writeError(w, http.StatusBadRequest, "account details were not accepted")
	default:
		writeError(w, http.StatusInternalServerError, "the account operation could not be completed")
	}
}

func clear(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r.Context())
	if actor == nil || actor.Access != metadata.UserAccessAdmin {
		writeError(w, http.StatusForbidden, "administrator access required")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		users, err := s.auth.Admins.List(r.Context(), actor.ID)
		if err != nil {
			writeError(w, http.StatusForbidden, "administrator access required")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	case http.MethodPost:
		if !s.requireCSRF(w, r) {
			return
		}
		var input auth.NewUser
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		user, err := s.auth.Admins.Create(r.Context(), actor.ID, input)
		if err != nil {
			writeAccountMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, user)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleUserMutation(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r.Context())
	if actor == nil || actor.Access != metadata.UserAccessAdmin {
		writeError(w, http.StatusForbidden, "administrator access required")
		return
	}
	remainder := strings.TrimPrefix(r.URL.Path, "/admin/api/users/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, http.StatusNotFound, "admin route not found")
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPatch {
		var input auth.UserEdit
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		user, err := s.auth.Admins.Edit(r.Context(), actor.ID, parts[0], input)
		if err != nil {
			writeAccountMutationError(w, err)
			return
		}
		if user == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, user)
		return
	}
	if len(parts) == 2 && parts[1] == "password" && r.Method == http.MethodPost {
		var input struct {
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		user, err := s.auth.Admins.ResetPassword(r.Context(), actor.ID, parts[0], input.Password)
		if err != nil {
			writeAccountMutationError(w, err)
			return
		}
		if user == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, user)
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func isAllowedMutationPath(path string) bool {
	return path == "/admin/api/setup" || path == "/admin/api/login" || path == "/admin/api/logout" || path == "/admin/api/account/password" || strings.HasPrefix(path, "/admin/api/account/docker-tokens") || strings.HasPrefix(path, "/admin/api/account/registry-credentials/") || path == "/admin/api/users" || strings.HasPrefix(path, "/admin/api/users/")
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func readMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", HttpOnly: false, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

type currentUserKey struct{}

func withCurrentUser(ctx context.Context, user *metadata.User) context.Context {
	return context.WithValue(ctx, currentUserKey{}, user)
}
func currentUser(ctx context.Context) *metadata.User {
	user, _ := ctx.Value(currentUserKey{}).(*metadata.User)
	return user
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	isAdmin := user == nil || user.Access == metadata.UserAccessAdmin
	pageID, title, subtitle := adminPage(r.URL.Path)
	if !isAdmin {
		pageID, title, subtitle = "account", "Account", "Docker access and upstream registry credentials"
		data := dashboardData{StylesheetURL: adminStylesheetURL, ScriptURL: adminScriptURL, Page: pageID, PageTitle: title, PageSubtitle: subtitle, User: user, CredentialsAvailable: s.auth != nil && s.auth.Credentials != nil, GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339)}
		data.CredentialRows = s.credentialRows(r.Context(), user.ID)
		data.DockerTokens, _ = s.auth.Tokens.List(r.Context(), user.ID)
		audit := s.listAuditEvents(r.Context(), 50)
		for _, event := range audit {
			if event.ActorUserID == user.ID {
				data.AuditEvents = append(data.AuditEvents, event)
			}
		}
		s.renderDashboard(w, r, data)
		return
	}
	data := dashboardData{
		StylesheetURL:        adminStylesheetURL,
		ScriptURL:            adminScriptURL,
		Page:                 pageID,
		PageTitle:            title,
		PageSubtitle:         subtitle,
		GeneratedAtUTC:       time.Now().UTC().Format(time.RFC3339),
		User:                 user,
		IsAdmin:              true,
		LegacyView:           user == nil,
		CredentialsAvailable: s.auth != nil && s.auth.Credentials != nil,
	}

	switch pageID {
	case "overview":
		windowStart := time.Now().UTC().Add(-24 * time.Hour)
		summary, err := s.repo.SummarizeRequestEvents(r.Context(), windowStart)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not summarize requests")
			return
		}
		page, err := s.repo.QueryRequestEvents(r.Context(), metadata.RequestEventQuery{Filter: metadata.RequestEventFilter{After: windowStart}, Limit: 6})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not query recent activity")
			return
		}
		s.populateDashboardOverview(&data, summary, page.Events)
	case "requests":
		query, err := parseRequestEventQuery(r, 25)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		page, err := s.repo.QueryRequestEvents(r.Context(), query)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not query requests")
			return
		}
		data.Requests = page.Events
		data.RequestCount = len(page.Events)
		data.TotalRequestCount, err = s.repo.CountRequestEvents(r.Context(), query.Filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not count requests")
			return
		}
		data.Filters = requestFiltersFromRequest(r, s.config, query.Limit)
		data.HasFilters = hasRequestFilters(r.URL.Query())
		data.FilterChips = requestFilterChips(r.URL.Query())
		data.PreviousURL, data.NextURL = requestPageURLs(r.URL.Query(), page.Next)
	case "routes":
		data.Routes = append([]config.Route(nil), s.config.Routes...)
	case "sources":
		data.Sources = sourceViews(s.config)
	case "cache":
		mappings, err := s.repo.ListTagMappings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list cached tag mappings")
			return
		}
		data.Artifacts = artifactsFromMappings(mappings)
		data.ArtifactCount = len(data.Artifacts)
		if s.store != nil {
			blobs, _ := s.store.ListBlobs(r.Context())
			data.BlobCount = len(blobs)
		}
	case "account":
		if user != nil {
			data.CredentialRows = s.credentialRows(r.Context(), user.ID)
			data.DockerTokens, _ = s.auth.Tokens.List(r.Context(), user.ID)
		}
	case "users":
		if user != nil && s.auth != nil && s.auth.Admins != nil {
			data.Users, _ = s.auth.Admins.List(r.Context(), user.ID)
		}
	case "audit":
		if err := s.populateAuditWorkspace(r, &data); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.renderDashboard(w, r, data)
}

func (s *Server) populateAuditWorkspace(r *http.Request, data *dashboardData) error {
	values := r.URL.Query()
	filters := auditFilterView{Action: values.Get("action"), Outcome: values.Get("outcome"), Actor: values.Get("actor"), Target: values.Get("target"), Correlation: values.Get("correlation")}
	for name, value := range map[string]string{"action": filters.Action, "outcome": filters.Outcome, "actor": filters.Actor, "target": filters.Target, "correlation": filters.Correlation} {
		if len(value) > 256 {
			return fmt.Errorf("%s filter is too long", name)
		}
	}
	if filters.Outcome != "" && filters.Outcome != "success" && filters.Outcome != "failure" {
		return fmt.Errorf("invalid audit outcome")
	}
	data.AuditFilters = filters
	for _, action := range []string{"user.bootstrap", "user.created", "user.updated", "user.disabled", "user.access_changed", "user.password_changed", "user.password_reset", "docker_token.created", "docker_token.revoked", "credential.created", "credential.replaced", "credential.deleted", "credential.verification_failed"} {
		data.AuditActionOptions = append(data.AuditActionOptions, auditActionOption{Action: action, Label: auditActionLabel(action)})
	}
	events := s.listAuditEvents(r.Context(), 100)
	users, _ := s.repo.ListUsers(r.Context())
	userLabels := map[string]string{}
	for _, user := range users {
		label := user.DisplayName
		if label == "" {
			label = user.Username
		}
		userLabels[user.ID] = label
	}
	for _, event := range events {
		if filters.Action != "" && event.Action != filters.Action {
			continue
		}
		if filters.Outcome != "" && event.Outcome != filters.Outcome {
			continue
		}
		if filters.Actor != "" && event.ActorUserID != filters.Actor {
			continue
		}
		if filters.Target != "" && event.TargetID != filters.Target {
			continue
		}
		if filters.Correlation != "" && event.CorrelationID != filters.Correlation {
			continue
		}
		actor := userLabels[event.ActorUserID]
		if actor == "" {
			actor = event.ActorRole
		}
		target := userLabels[event.TargetID]
		if target == "" {
			target = event.TargetType + " " + event.TargetID
		}
		data.AuditRows = append(data.AuditRows, auditRow{Event: event, ActionLabel: auditActionLabel(event.Action), ActorLabel: actor, TargetLabel: target, Detail: auditSafeDetail(event)})
	}
	data.AuditMatchCount = len(data.AuditRows)
	return nil
}

func auditActionLabel(action string) string {
	labels := map[string]string{"user.bootstrap": "Created first administrator", "user.created": "Created user", "user.updated": "Updated user", "user.disabled": "Disabled user", "user.access_changed": "Changed user access", "user.password_changed": "Changed own password", "user.password_reset": "Reset user password", "docker_token.created": "Created Docker token", "docker_token.revoked": "Revoked Docker token", "credential.created": "Saved registry credential", "credential.replaced": "Replaced registry credential", "credential.deleted": "Removed registry credential", "credential.verification_failed": "Credential verification failed"}
	if label := labels[action]; label != "" {
		return label
	}
	return "Recorded security event"
}

func auditSafeDetail(event metadata.AuditEvent) string {
	if classification := event.Details["error_classification"]; classification != "" {
		return classification
	}
	parts := []string{}
	if previous, next := event.Details["previous_access"], event.Details["new_access"]; previous != "" && next != "" && previous != next {
		parts = append(parts, "Access: "+previous+" to "+next)
	} else if next != "" {
		parts = append(parts, "Access: "+next)
	}
	if enabled := event.Details["enabled"]; enabled != "" {
		parts = append(parts, "Enabled: "+enabled)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "; ")
	}
	if source := event.Details["source_id"]; source != "" {
		return "Registry " + source
	}
	return ""
}

func adminPage(path string) (page, title, subtitle string) {
	if strings.HasPrefix(path, "/requests/") {
		return "request-detail", "Request investigation", "Outcome, routing decision, provenance, and safe operational context"
	}
	switch path {
	case "/requests":
		return "requests", "Requests", "Investigate pulls, pushes, routing decisions, and failures"
	case "/routes":
		return "routes", "Routes", "Policy precedence, destinations, fallback, and rewriting"
	case "/sources":
		return "sources", "Sources", "Configured registries, capabilities, and authentication mode"
	case "/cache":
		return "cache", "Cache", "Cached artifacts, digests, and provenance"
	case "/admin/users":
		return "users", "Users", "Local identities, roles, and immediate access controls"
	case "/admin/audit":
		return "audit", "Audit", "Security and account activity"
	case "/account":
		return "account", "Account", "Docker access and upstream registry credentials"
	default:
		return "overview", "Overview", "Registry gateway health and recent operating state"
	}
}

func (s *Server) handleRequestDetailPage(w http.ResponseWriter, r *http.Request) {
	if !readMethod(w, r) {
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/requests/")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	event, err := s.repo.FindRequestEventByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load request detail")
		return
	}
	if event == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	provenance, err := s.repo.FindProvenanceByLogicalReference(r.Context(), event.LogicalReference)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load request provenance")
		return
	}
	pageID, title, subtitle := adminPage(r.URL.Path)
	data := dashboardData{StylesheetURL: adminStylesheetURL, ScriptURL: adminScriptURL, Page: pageID, PageTitle: title, PageSubtitle: subtitle, User: currentUser(r.Context()), IsAdmin: true, GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339), RequestProvenance: provenance}
	data.RequestDetail = &requestDetailPage{Event: *event, Credential: credentialDisplay(event.CredentialSource), Duration: formatDashboardDuration(event.Duration), BytesTransferred: formatBytes(event.BytesTransferred)}
	s.renderDashboard(w, r, data)
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	for _, unit := range units {
		size /= 1024
		if size < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.2f %s", size, unit)
		}
	}
	return fmt.Sprintf("%d B", value)
}

func (s *Server) populateDashboardOverview(data *dashboardData, summary metadata.RequestEventSummary, events []metadata.RequestEvent) {
	data.DashboardWindow = "Last 24 hours"
	data.SourceCount = len(s.config.Sources)
	data.RouteCount = len(s.config.Routes)
	data.RequestCount = summary.Total
	data.HealthItems = []dashboardHealthItem{
		{Name: "Regstair", Status: "Operational", Detail: "Request metadata is available", Tone: "success"},
		{Name: "Metadata", Status: "Operational", Detail: "Request history query succeeded", Tone: "success"},
	}
	if s.store == nil {
		data.HealthItems = append(data.HealthItems, dashboardHealthItem{Name: "Content store", Status: "Not assessed", Detail: "No content store is attached to this server", Tone: "neutral"})
	} else {
		data.HealthItems = append(data.HealthItems, dashboardHealthItem{Name: "Content store", Status: "Operational", Detail: "Content store is attached", Tone: "success"})
	}
	if s.auth == nil {
		data.HealthItems = append(data.HealthItems, dashboardHealthItem{Name: "Credential storage", Status: "Not enabled", Detail: "Authentication services are not configured", Tone: "neutral"})
	} else if s.auth.Credentials == nil {
		data.HealthItems = append(data.HealthItems, dashboardHealthItem{Name: "Credential storage", Status: "Unavailable", Detail: "Credential encryption is not configured", Tone: "warning"})
		data.AttentionItems = append(data.AttentionItems, dashboardAttentionItem{Title: "Credential storage unavailable", Detail: "Per-user upstream credentials cannot be saved until encryption is configured.", URL: "/account", Tone: "warning"})
	} else {
		data.HealthItems = append(data.HealthItems, dashboardHealthItem{Name: "Credential storage", Status: "Operational", Detail: "Encrypted credential storage is available", Tone: "success"})
	}

	unavailableSources := 0
	for _, source := range s.config.Sources {
		_, connected := s.connectors[source.ID]
		if !source.Enabled || !connected {
			unavailableSources++
		}
	}
	sourceStatus := fmt.Sprintf("%d configured", len(s.config.Sources))
	sourceTone := "success"
	if unavailableSources > 0 {
		sourceStatus = fmt.Sprintf("%d need attention", unavailableSources)
		sourceTone = "warning"
		data.AttentionItems = append(data.AttentionItems, dashboardAttentionItem{Title: "Sources need attention", Detail: fmt.Sprintf("%d configured source(s) are disabled or do not have a connector.", unavailableSources), URL: "/sources", Count: unavailableSources, Tone: "warning"})
	}
	data.HealthItems = append(data.HealthItems, dashboardHealthItem{Name: "Sources", Status: sourceStatus, Detail: "Configuration state; open Sources for live checks", Tone: sourceTone})

	data.CacheHits = summary.CacheHits
	data.CacheMisses = summary.CacheMisses
	data.RecentActivity = append(data.RecentActivity, events...)
	data.FailureRate = percentage(summary.Errors, summary.Total)
	data.CacheHitRatio = percentage(summary.CacheHits, summary.CacheHits+summary.CacheMisses)
	data.AverageDuration = formatDashboardDuration(summary.Average)
	data.P95Duration = formatDashboardDuration(summary.P95)
	if summary.Errors > 0 {
		data.AttentionItems = append(data.AttentionItems, dashboardAttentionItem{Title: "Failed requests", Detail: "Requests ended in an operational error during the last 24 hours.", URL: "/requests?status=error", Count: summary.Errors, Tone: "danger"})
	}
	if summary.DeniedPushes > 0 {
		data.AttentionItems = append(data.AttentionItems, dashboardAttentionItem{Title: "Denied pushes", Detail: "Pushes were rejected by Regstair policy during the last 24 hours.", URL: "/requests?operation=push&status=denied", Count: summary.DeniedPushes, Tone: "warning"})
	}
	if summary.AuthFailures > 0 {
		data.AttentionItems = append(data.AttentionItems, dashboardAttentionItem{Title: "Upstream authentication failures", Detail: "An upstream registry rejected authentication during the last 24 hours.", URL: "/requests?error_classification=upstream_authentication_failed", Count: summary.AuthFailures, Tone: "danger"})
	}
}

func percentage(numerator, denominator int) string {
	if denominator == 0 {
		return "Not available"
	}
	return fmt.Sprintf("%.1f%%", float64(numerator)*100/float64(denominator))
}

func formatDashboardDuration(duration time.Duration) string {
	if duration == 0 {
		return "Not available"
	}
	if duration < time.Second {
		return fmt.Sprintf("%d ms", duration.Milliseconds())
	}
	return fmt.Sprintf("%.2f s", duration.Seconds())
}

func (s *Server) listAuditEvents(ctx context.Context, limit int) []metadata.AuditEvent {
	repo, ok := s.repo.(auditRepository)
	if !ok {
		return nil
	}
	events, _ := repo.ListAuditEvents(ctx, limit)
	return events
}

func (s *Server) credentialRows(ctx context.Context, userID string) []credentialRow {
	registries := approvedRegistryViews(s.config)
	bySource := map[string]auth.RegistryCredentialView{}
	if s.auth != nil && s.auth.Credentials != nil {
		credentials, _ := s.auth.Credentials.List(ctx, userID)
		for _, credential := range credentials {
			bySource[credential.SourceID] = credential
		}
	}
	rows := make([]credentialRow, 0, len(registries))
	for _, registryView := range registries {
		row := credentialRow{Registry: registryView}
		if credential, ok := bySource[registryView.ID]; ok {
			value := credential
			row.Credential = &value
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) renderDashboard(w http.ResponseWriter, r *http.Request, data dashboardData) {
	var rendered bytes.Buffer
	if err := dashboardTemplate.ExecuteTemplate(&rendered, "dashboard.html", data); err != nil {
		writeError(w, http.StatusInternalServerError, "render admin dashboard")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(rendered.Bytes())
	}
}

func requestFiltersFromRequest(r *http.Request, cfg config.Config, limit int) requestFilterView {
	values := r.URL.Query()
	view := requestFilterView{
		Reference:           values.Get("reference"),
		ClientIdentity:      values.Get("client_identity"),
		Route:               values.Get("route"),
		Operation:           values.Get("operation"),
		Source:              values.Get("source"),
		Status:              values.Get("status"),
		ErrorClassification: values.Get("error_classification"),
		CacheResult:         values.Get("cache"),
		Sort:                values.Get("sort"),
		CredentialSource:    values.Get("credential"),
		Window:              values.Get("window"),
		After:               values.Get("after"),
		Before:              values.Get("before"),
		Limit:               limit,
	}
	for _, route := range cfg.Routes {
		view.Routes = append(view.Routes, route.Name)
	}
	for _, source := range cfg.Sources {
		view.Sources = append(view.Sources, source.ID)
	}
	return view
}

func hasRequestFilters(values url.Values) bool {
	for _, name := range []string{"reference", "client_identity", "route", "operation", "source", "status", "cache", "credential", "error_classification", "window", "after", "before", "sort"} {
		if values.Get(name) != "" {
			return true
		}
	}
	return false
}

func requestFilterChips(values url.Values) []requestFilterChip {
	labels := map[string]string{"reference": "Reference", "client_identity": "Client", "route": "Route", "operation": "Operation", "source": "Source", "status": "Status", "cache": "Cache", "credential": "Credential", "error_classification": "Classification", "window": "Window", "after": "After", "before": "Before", "sort": "Sort"}
	order := []string{"reference", "client_identity", "route", "operation", "source", "status", "cache", "credential", "error_classification", "window", "after", "before", "sort"}
	chips := []requestFilterChip{}
	for _, key := range order {
		value := values.Get(key)
		if value == "" {
			continue
		}
		remaining := cloneValues(values)
		remaining.Del(key)
		remaining.Del("cursor")
		remaining.Del("trail")
		labelValue := value
		if key != "reference" && key != "client_identity" {
			labelValue = strings.ToUpper(value[:1]) + value[1:]
		}
		url := "/requests"
		if encoded := remaining.Encode(); encoded != "" {
			url += "?" + encoded
		}
		chips = append(chips, requestFilterChip{Label: labels[key] + ": " + labelValue, URL: url})
	}
	return chips
}

func requestPageURLs(values url.Values, next *metadata.RequestEventCursor) (previousURL string, nextURL string) {
	current := values.Get("cursor")
	trail := parseCursorTrail(values.Get("trail"))
	if current != "" {
		previous := cloneValues(values)
		if len(trail) == 0 || trail[len(trail)-1] == "first" {
			previous.Del("cursor")
		} else {
			previous.Set("cursor", trail[len(trail)-1])
		}
		if len(trail) > 0 {
			trail = trail[:len(trail)-1]
		}
		setCursorTrail(previous, trail)
		previousURL = "/requests?" + previous.Encode()
	}
	if next != nil {
		nextValues := cloneValues(values)
		nextValues.Set("cursor", encodeRequestCursor(*next))
		nextTrail := parseCursorTrail(values.Get("trail"))
		if current == "" {
			nextTrail = append(nextTrail, "first")
		} else {
			nextTrail = append(nextTrail, current)
		}
		if len(nextTrail) > 20 {
			nextTrail = nextTrail[len(nextTrail)-20:]
		}
		setCursorTrail(nextValues, nextTrail)
		nextURL = "/requests?" + nextValues.Encode()
	}
	return previousURL, nextURL
}

func parseCursorTrail(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ".")
	valid := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "first" {
			valid = append(valid, part)
			continue
		}
		if _, err := decodeRequestCursor(part); err == nil {
			valid = append(valid, part)
		}
	}
	return valid
}

func setCursorTrail(values url.Values, trail []string) {
	if len(trail) == 0 {
		values.Del("trail")
		return
	}
	values.Set("trail", strings.Join(trail, "."))
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request, assetPath string, contentType string) {
	body, err := adminAssets.ReadFile(assetPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "admin asset not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func sourceViews(cfg config.Config) []SourceView {
	credentialIDs := map[string]struct{}{}
	for _, credential := range cfg.Credentials {
		credentialIDs[credential.ID] = struct{}{}
	}

	views := make([]SourceView, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		mode := source.Auth.Mode
		if mode == "" {
			mode = config.AuthModeNone
		}
		configured := mode == config.AuthModeNone
		if mode == config.AuthModeProxy {
			_, configured = credentialIDs[source.Auth.CredentialRef]
		}
		view := SourceView{
			ID:       source.ID,
			Name:     source.Name,
			Endpoint: source.Endpoint,
			Type:     source.Type,
			Enabled:  source.Enabled,
			Auth: AuthView{
				Mode:          mode,
				CredentialRef: source.Auth.CredentialRef,
				Configured:    configured,
			},
			UserCredentials: source.UserCredentials,
			Routes:          sourceRouteNames(cfg, source.ID),
		}
		views = append(views, view)
	}
	return views
}

func sourceRouteNames(cfg config.Config, sourceID string) []string {
	routes := []string{}
	for _, route := range cfg.Routes {
		used := route.Push.Destination == sourceID
		for _, id := range route.Pull.Sources {
			if id == sourceID {
				used = true
				break
			}
		}
		if used {
			routes = append(routes, route.Name)
		}
	}
	return routes
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	query, err := parseRequestEventQuery(r, 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.repo.QueryRequestEvents(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not query requests")
		return
	}
	response := RequestsResponse{Requests: page.Events}
	if page.Next != nil {
		response.NextCursor = encodeRequestCursor(*page.Next)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleRequestDetail(w http.ResponseWriter, r *http.Request) {
	if !readMethod(w, r) {
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/admin/api/requests/")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	event, err := s.repo.FindRequestEventByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load request detail")
		return
	}
	if event == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	response := RequestDetailResponse{Request: requestDetailView(*event)}
	response.Provenance, err = s.repo.FindProvenanceByLogicalReference(r.Context(), event.LogicalReference)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load request provenance")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func requestDetailView(event metadata.RequestEvent) RequestDetailView {
	return RequestDetailView{ID: event.ID, Timestamp: event.Timestamp, Operation: event.Operation, LogicalReference: event.LogicalReference, Status: event.Status, ClientIdentity: event.ClientIdentity, MatchedRoute: event.MatchedRoute, SourceOrDestination: event.SourceOrDestination, CacheResult: event.CacheResult, Credential: credentialDisplay(event.CredentialSource), DurationMillis: float64(event.Duration.Microseconds()) / 1000, BytesTransferred: event.BytesTransferred, ErrorClassification: event.ErrorClassification, Explanation: append([]string(nil), event.Explanation...)}
}

func credentialDisplay(source string) string {
	switch source {
	case "", "anonymous":
		return "No upstream credential"
	case "proxy":
		return "Shared registry credential"
	case "current_user":
		return "Current user credential"
	default:
		return "Credential policy"
	}
}

func parseRequestEventQuery(r *http.Request, defaultLimit int) (metadata.RequestEventQuery, error) {
	values := r.URL.Query()
	query := metadata.RequestEventQuery{
		Limit: defaultLimit,
		Filter: metadata.RequestEventFilter{
			ClientIdentity:      values.Get("client_identity"),
			CredentialSource:    values.Get("credential"),
			Route:               values.Get("route"),
			SourceOrDestination: values.Get("source"),
			ErrorClassification: values.Get("error_classification"),
			ReferenceContains:   values.Get("reference"),
		},
	}
	for name, value := range map[string]string{
		"client_identity":      query.Filter.ClientIdentity,
		"credential":           query.Filter.CredentialSource,
		"route":                query.Filter.Route,
		"source":               query.Filter.SourceOrDestination,
		"error_classification": query.Filter.ErrorClassification,
		"reference":            query.Filter.ReferenceContains,
	} {
		if len(value) > 256 {
			return metadata.RequestEventQuery{}, fmt.Errorf("%s is too long", name)
		}
	}

	if raw := values.Get("operation"); raw != "" {
		query.Filter.Operation = metadata.Operation(raw)
		if query.Filter.Operation != metadata.OperationPull && query.Filter.Operation != metadata.OperationPush {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid operation")
		}
	}
	if raw := values.Get("status"); raw != "" {
		query.Filter.Status = metadata.RequestStatus(raw)
		if query.Filter.Status != metadata.StatusSuccess && query.Filter.Status != metadata.StatusDenied && query.Filter.Status != metadata.StatusError {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid status")
		}
	}
	if raw := values.Get("cache"); raw != "" {
		query.Filter.CacheResult = metadata.CacheResult(raw)
		if query.Filter.CacheResult != metadata.CacheHit && query.Filter.CacheResult != metadata.CacheMiss && query.Filter.CacheResult != metadata.CacheBypassed {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid cache result")
		}
	}
	if raw := values.Get("credential"); raw != "" && raw != "anonymous" && raw != "current_user" && raw != "proxy" {
		return metadata.RequestEventQuery{}, fmt.Errorf("invalid credential source")
	}
	if raw := values.Get("window"); raw != "" {
		if values.Get("after") != "" || values.Get("before") != "" {
			return metadata.RequestEventQuery{}, fmt.Errorf("relative and absolute time filters cannot be combined")
		}
		windows := map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}
		duration, ok := windows[raw]
		if !ok {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid relative time window")
		}
		query.Filter.After = time.Now().UTC().Add(-duration)
	}
	if raw := values.Get("sort"); raw != "" {
		if raw != "oldest" {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid sort order")
		}
		query.OldestFirst = true
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid limit")
		}
		query.Limit = limit
	}
	if raw := values.Get("after"); raw != "" {
		parsed, err := parseFilterTime(raw)
		if err != nil {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid after timestamp")
		}
		query.Filter.After = parsed
	}
	if raw := values.Get("before"); raw != "" {
		parsed, err := parseFilterTime(raw)
		if err != nil {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid before timestamp")
		}
		query.Filter.Before = parsed
	}
	if !query.Filter.After.IsZero() && !query.Filter.Before.IsZero() && !query.Filter.After.Before(query.Filter.Before) {
		return metadata.RequestEventQuery{}, fmt.Errorf("after timestamp must be before before timestamp")
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeRequestCursor(raw)
		if err != nil {
			return metadata.RequestEventQuery{}, fmt.Errorf("invalid cursor")
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func parseFilterTime(raw string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	return time.ParseInLocation("2006-01-02T15:04", raw, time.UTC)
}

func encodeRequestCursor(cursor metadata.RequestEventCursor) string {
	buffer := make([]byte, 16)
	binary.BigEndian.PutUint64(buffer[:8], uint64(cursor.Timestamp.UnixNano()))
	binary.BigEndian.PutUint64(buffer[8:], uint64(cursor.ID))
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func decodeRequestCursor(raw string) (metadata.RequestEventCursor, error) {
	if len(raw) > 64 || strings.ContainsAny(raw, "\r\n") {
		return metadata.RequestEventCursor{}, fmt.Errorf("invalid cursor encoding")
	}
	buffer, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(buffer) != 16 {
		return metadata.RequestEventCursor{}, fmt.Errorf("invalid cursor encoding")
	}
	timestampNanos := int64(binary.BigEndian.Uint64(buffer[:8]))
	id := int64(binary.BigEndian.Uint64(buffer[8:]))
	if timestampNanos <= 0 || id <= 0 {
		return metadata.RequestEventCursor{}, fmt.Errorf("invalid cursor values")
	}
	return metadata.RequestEventCursor{Timestamp: time.Unix(0, timestampNanos).UTC(), ID: id}, nil
}

func (s *Server) handleProvenance(w http.ResponseWriter, r *http.Request) {
	reference := r.URL.Query().Get("reference")
	if reference == "" {
		writeError(w, http.StatusBadRequest, "reference is required")
		return
	}

	record, err := s.repo.FindProvenanceByLogicalReference(r.Context(), reference)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not find provenance")
		return
	}
	if record == nil {
		writeError(w, http.StatusNotFound, "provenance not found")
		return
	}
	writeJSON(w, http.StatusOK, ProvenanceResponse{Provenance: record})
}

func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	mappings, err := s.repo.ListTagMappings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list artifacts")
		return
	}

	writeJSON(w, http.StatusOK, ArtifactsResponse{Artifacts: artifactsFromMappings(mappings)})
}

func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "content store is not configured")
		return
	}

	blobs, err := s.store.ListBlobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list cached content")
		return
	}
	writeJSON(w, http.StatusOK, CacheResponse{Blobs: blobs})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func artifactsFromMappings(mappings []metadata.TagMapping) []Artifact {
	artifacts := make([]Artifact, 0, len(mappings))
	for _, mapping := range mappings {
		artifacts = append(artifacts, Artifact{
			LogicalReference: mapping.LogicalRepository + ":" + mapping.Tag,
			Mapping:          mapping,
		})
	}
	return artifacts
}
