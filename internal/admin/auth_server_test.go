package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"regstair/internal/auth"
	"regstair/internal/config"
	"regstair/internal/metadata"
	"regstair/internal/registry"
	"regstair/internal/security"
)

type authServerFixture struct {
	server       *Server
	repo         *metadata.SQLiteRepository
	databasePath string
	admin        *metadata.User
	admins       *auth.AdminAccountService
}

func newAuthServerFixture(t *testing.T) authServerFixture {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "regstair.db")
	repo, err := metadata.NewSQLiteRepository(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	hasher := auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)
	accounts := auth.NewAccountService(repo, hasher)
	admin, err := accounts.BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewWebSessionService(repo, nil, nil, 30*time.Minute, 12*time.Hour)
	tokens := auth.NewDockerTokenService(repo, nil, nil)
	admins := auth.NewAdminAccountService(repo, hasher)
	credentialService := testCredentialService(t, repo, verifierFunc(func(context.Context, auth.VerificationRequest) error { return nil }))
	server := NewServer(Config{Config: testConfig(), Repo: repo, Auth: &AuthConfig{Accounts: accounts, Sessions: sessions, Admins: admins, Tokens: tokens, Credentials: credentialService}})
	return authServerFixture{server: server, repo: repo, databasePath: databasePath, admin: admin, admins: admins}
}

type verifierFunc func(context.Context, auth.VerificationRequest) error

func (f verifierFunc) Verify(ctx context.Context, request auth.VerificationRequest) error {
	return f(ctx, request)
}

func TestFirstRunSetupReplacesLegacyDashboardAndCreatesAuthenticatedAdmin(t *testing.T) {
	repo, err := metadata.NewSQLiteRepository(filepath.Join(t.TempDir(), "regstair.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	hasher := auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)
	accounts := auth.NewAccountService(repo, hasher)
	sessions := auth.NewWebSessionService(repo, nil, nil, 30*time.Minute, 12*time.Hour)
	server := NewServer(Config{Config: testConfig(), Repo: repo, Auth: &AuthConfig{Accounts: accounts, Sessions: sessions, Admins: auth.NewAdminAccountService(repo, hasher), Tokens: auth.NewDockerTokenService(repo, nil, nil)}})

	dashboard := httptest.NewRecorder()
	server.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/", nil))
	if dashboard.Code != http.StatusSeeOther || dashboard.Header().Get("Location") != "/setup" {
		t.Fatalf("pre-setup dashboard = %d location=%q", dashboard.Code, dashboard.Header().Get("Location"))
	}
	operations := httptest.NewRecorder()
	server.ServeHTTP(operations, httptest.NewRequest(http.MethodGet, "/admin/api/routes", nil))
	if operations.Code != http.StatusPreconditionRequired {
		t.Fatalf("pre-setup operations = %d %s", operations.Code, operations.Body.String())
	}

	page := httptest.NewRecorder()
	server.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte("Create the first administrator")) || !bytes.Contains(page.Body.Bytes(), []byte(`minlength="15" maxlength="128"`)) || !bytes.Contains(page.Body.Bytes(), []byte("15 to 128 characters")) || bytes.Contains(page.Body.Bytes(), []byte("Recent requests")) {
		t.Fatalf("setup page = %d %s", page.Code, page.Body.String())
	}

	status := httptest.NewRecorder()
	server.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/admin/api/setup", nil))
	var setup struct {
		Required bool   `json:"required"`
		Token    string `json:"setup_token"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if status.Code != http.StatusOK || !setup.Required || setup.Token == "" {
		t.Fatalf("setup status = %d %s", status.Code, status.Body.String())
	}

	missingToken := requestJSON(t, server, http.MethodPost, "/admin/api/setup", map[string]any{"username": "admin", "password": "correct horse battery staple"}, nil, "")
	if missingToken.Code != http.StatusForbidden {
		t.Fatalf("missing setup token = %d %s", missingToken.Code, missingToken.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"username": "admin", "password": "correct horse battery staple", "display_name": "Primary administrator"})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/setup", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Regstair-Setup-Token", setup.Token)
	request.Header.Set("Origin", "http://example.com")
	request.Host = "regstair.example.com"
	wrongOrigin := httptest.NewRecorder()
	server.ServeHTTP(wrongOrigin, request)
	if wrongOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin setup = %d %s", wrongOrigin.Code, wrongOrigin.Body.String())
	}

	created := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "https://regstair.example.com/admin/api/setup", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Regstair-Setup-Token", setup.Token)
	request.Header.Set("Origin", "https://regstair.example.com")
	server.ServeHTTP(created, request)
	if created.Code != http.StatusCreated || created.Header().Get("Set-Cookie") == "" {
		t.Fatalf("setup create = %d %s", created.Code, created.Body.String())
	}
	var login loginResponse
	if err := json.Unmarshal(created.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.User.Access != metadata.UserAccessAdmin || login.CSRFToken == "" {
		t.Fatalf("setup response = %#v", login)
	}

	repeated := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "https://regstair.example.com/admin/api/setup", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Regstair-Setup-Token", setup.Token)
	request.Header.Set("Origin", "https://regstair.example.com")
	server.ServeHTTP(repeated, request)
	if repeated.Code != http.StatusConflict {
		t.Fatalf("repeated setup = %d %s", repeated.Code, repeated.Body.String())
	}
}

func testCredentialService(t *testing.T, repo *metadata.SQLiteRepository, verifier auth.CredentialVerifier) *auth.RegistryCredentialService {
	t.Helper()
	keyring, err := auth.NewSecretKeyring("test", map[string][]byte{"test": bytes.Repeat([]byte{3}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sources := []config.Source{{ID: "harbor", Endpoint: "https://harbor.example", Enabled: true, UserCredentials: config.UserCredentials{Approved: true, Pull: true, Push: true, VerificationRepository: "check/repo"}}}
	return auth.NewRegistryCredentialService(repo, keyring, verifier, sources)
}

func TestRegistryCredentialHTTPPreservesClassificationsAndNeverReturnsSecrets(t *testing.T) {
	fixture := newAuthServerFixture(t)
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	path := "/admin/api/account/registry-credentials/harbor/verify-and-save"
	withoutCSRF := requestJSON(t, fixture.server, http.MethodPost, path, map[string]any{"username": "robot", "secret": "SECRET-FIXTURE"}, login.cookie, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d", withoutCSRF.Code)
	}
	fixture.server.auth.Credentials = testCredentialService(t, fixture.repo, verifierFunc(func(context.Context, auth.VerificationRequest) error {
		return errors.Join(auth.ErrUpstreamPermission, errors.New("SECRET-FIXTURE upstream detail"))
	}))
	rejected := requestJSON(t, fixture.server, http.MethodPost, path, map[string]any{"username": "robot", "secret": "SECRET-FIXTURE"}, login.cookie, login.csrf)
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte(`"code":"insufficient_permission"`)) || bytes.Contains(rejected.Body.Bytes(), []byte("SECRET-FIXTURE")) || bytes.Contains(rejected.Body.Bytes(), []byte("upstream detail")) {
		t.Fatalf("rejected response = %d %s", rejected.Code, rejected.Body.String())
	}
	fixture.server.auth.Credentials = testCredentialService(t, fixture.repo, verifierFunc(func(context.Context, auth.VerificationRequest) error { return nil }))
	saved := requestJSON(t, fixture.server, http.MethodPost, path, map[string]any{"username": "robot", "secret": "SECRET-FIXTURE"}, login.cookie, login.csrf)
	if saved.Code != http.StatusOK || bytes.Contains(saved.Body.Bytes(), []byte("SECRET-FIXTURE")) || bytes.Contains(saved.Body.Bytes(), []byte("encrypted")) {
		t.Fatalf("saved response = %d %s", saved.Code, saved.Body.String())
	}
	listed := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account/registry-credentials", nil, login.cookie, "")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(`"source_id":"harbor"`)) || bytes.Contains(listed.Body.Bytes(), []byte("SECRET-FIXTURE")) {
		t.Fatalf("list response = %d %s", listed.Code, listed.Body.String())
	}
	unconfirmed := requestJSON(t, fixture.server, http.MethodDelete, "/admin/api/account/registry-credentials/harbor", map[string]any{"confirm": false}, login.cookie, login.csrf)
	if unconfirmed.Code != http.StatusBadRequest || !bytes.Contains(unconfirmed.Body.Bytes(), []byte(`"code":"confirmation_required"`)) {
		t.Fatalf("unconfirmed delete = %d %s", unconfirmed.Code, unconfirmed.Body.String())
	}
	removed := requestJSON(t, fixture.server, http.MethodDelete, "/admin/api/account/registry-credentials/harbor", map[string]any{"confirm": true}, login.cookie, login.csrf)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", removed.Code, removed.Body.String())
	}
}

func TestFailedAuthenticationLogsAndResponseContainNoSubmittedPassword(t *testing.T) {
	fixture := newAuthServerFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	response := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/login", map[string]any{"username": "admin", "password": "WEB-PASSWORD-CANARY"}, nil, "")
	combined := logs.String() + response.Body.String()
	if response.Code != http.StatusUnauthorized || !bytes.Contains(response.Body.Bytes(), []byte("invalid username or password")) {
		t.Fatalf("failed login = %d %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"WEB-PASSWORD-CANARY", "Authorization", "argon2"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("failed login leaked %q: %s", secret, combined)
		}
	}
}

func TestCredentialCanaryAbsentFromAPIAuditAndSQLiteAfterFailedAndSuccessfulFlows(t *testing.T) {
	fixture := newAuthServerFixture(t)
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	failedSecret := "FAILED-UPSTREAM-CANARY"
	successSecret := "SUCCESS-UPSTREAM-CANARY"
	fixture.server.auth.Credentials = testCredentialService(t, fixture.repo, verifierFunc(func(context.Context, auth.VerificationRequest) error {
		return errors.Join(auth.ErrUpstreamUnavailable, errors.New("forced upstream failure "+failedSecret))
	}))
	failed := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/account/registry-credentials/harbor/verify-and-save", map[string]any{"username": "robot", "secret": failedSecret}, login.cookie, login.csrf)
	fixture.server.auth.Credentials = testCredentialService(t, fixture.repo, verifierFunc(func(context.Context, auth.VerificationRequest) error { return nil }))
	succeeded := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/account/registry-credentials/harbor/verify-and-save", map[string]any{"username": "robot", "secret": successSecret}, login.cookie, login.csrf)
	if failed.Code != http.StatusBadRequest || succeeded.Code != http.StatusOK {
		t.Fatalf("credential responses failed=%d %s success=%d %s", failed.Code, failed.Body.String(), succeeded.Code, succeeded.Body.String())
	}
	events, err := fixture.repo.ListAuditEvents(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	auditJSON, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	database := []byte{}
	for _, suffix := range []string{"", "-wal"} {
		contents, readErr := os.ReadFile(fixture.databasePath + suffix)
		if readErr == nil {
			database = append(database, contents...)
		}
	}
	combined := append(append(append([]byte{}, failed.Body.Bytes()...), succeeded.Body.Bytes()...), auditJSON...)
	combined = append(combined, database...)
	for _, secret := range []string{failedSecret, successSecret, "forced upstream failure"} {
		if bytes.Contains(combined, []byte(secret)) {
			t.Fatalf("credential flow leaked %q", secret)
		}
	}
}

func TestRegistryCredentialHTTPFailsClosedWithoutEncryptionKey(t *testing.T) {
	fixture := newAuthServerFixture(t)
	fixture.server.auth.Credentials = nil
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	response := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account/registry-credentials", nil, login.cookie, "")
	if response.Code != http.StatusServiceUnavailable || bytes.Contains(response.Body.Bytes(), []byte("key")) {
		t.Fatalf("unavailable response = %d %s", response.Code, response.Body.String())
	}
}

func TestDockerTokenHTTPIsOneTimeAndAuthenticatesLocally(t *testing.T) {
	fixture := newAuthServerFixture(t)
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	response := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/account/docker-tokens", map[string]any{"label": "workstation", "expires_in_days": 7}, login.cookie, login.csrf)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue token = %d %s", response.Code, response.Body.String())
	}
	var issued auth.IssuedDockerToken
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Secret == "" || len(issued.Token.TokenHash) != 0 {
		t.Fatalf("issued token = %#v", issued)
	}
	authenticator := auth.NewGatewayAuthenticator(nil, auth.NewDockerTokenService(fixture.repo, nil, nil))
	identity, ok := authenticator.AuthenticateBasic("admin", issued.Secret)
	if !ok || identity.ID != fixture.admin.ID {
		t.Fatalf("local docker authentication = %q/%v", identity, ok)
	}
	listed := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account/docker-tokens", nil, login.cookie, "")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(issued.Token.ID)) || bytes.Contains(listed.Body.Bytes(), []byte(issued.Secret)) || bytes.Contains(listed.Body.Bytes(), []byte("token_hash")) {
		t.Fatalf("token list = %d %s", listed.Code, listed.Body.String())
	}
	revoked := requestJSON(t, fixture.server, http.MethodDelete, "/admin/api/account/docker-tokens/"+issued.Token.ID, nil, login.cookie, login.csrf)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke token = %d %s", revoked.Code, revoked.Body.String())
	}
	if _, ok := authenticator.AuthenticateBasic("admin", issued.Secret); ok {
		t.Fatal("revoked token still authenticates")
	}
	audit, _ := fixture.repo.ListAuditEvents(context.Background(), 20)
	actions := map[string]bool{}
	for _, event := range audit {
		actions[event.Action] = true
	}
	if !actions["docker_token.created"] || !actions["docker_token.revoked"] {
		t.Fatalf("token audit actions = %#v", actions)
	}
}

func TestLastAdministratorRequiresOwnershipTransfer(t *testing.T) {
	fixture := newAuthServerFixture(t)
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	userAccess := metadata.UserAccessUser
	demote := requestJSON(t, fixture.server, http.MethodPatch, "/admin/api/users/"+fixture.admin.ID, auth.UserEdit{Access: &userAccess, UpdatedAt: fixture.admin.UpdatedAt}, login.cookie, login.csrf)
	if demote.Code != http.StatusBadRequest || !strings.Contains(demote.Body.String(), "last enabled administrator") {
		t.Fatalf("last-admin demotion = %d %s", demote.Code, demote.Body.String())
	}
	second, err := fixture.admins.Create(context.Background(), fixture.admin.ID, auth.NewUser{Username: "owner-two", Password: "another correct battery staple", Access: metadata.UserAccessAdmin, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	current, _ := fixture.repo.FindUserByID(context.Background(), fixture.admin.ID)
	demote = requestJSON(t, fixture.server, http.MethodPatch, "/admin/api/users/"+fixture.admin.ID, auth.UserEdit{Access: &userAccess, UpdatedAt: current.UpdatedAt}, login.cookie, login.csrf)
	if demote.Code != http.StatusOK {
		t.Fatalf("transferred demotion = %d %s second=%s", demote.Code, demote.Body.String(), second.ID)
	}
	events, _ := fixture.repo.ListAuditEvents(context.Background(), 20)
	actions := map[string]bool{}
	for _, event := range events {
		actions[event.Action] = true
	}
	if !actions["user.created"] || !actions["user.access_changed"] {
		t.Fatalf("ownership-transfer audit actions = %#v", actions)
	}
}

func TestApprovedRegistryProjectionIsAuthenticatedAndRouteRelevant(t *testing.T) {
	fixture := newAuthServerFixture(t)
	fixture.server.config.Sources = []config.Source{
		{ID: "harbor", Name: "Harbor", Endpoint: "https://harbor.example", Enabled: true, UserCredentials: config.UserCredentials{Approved: true, Pull: true, Push: true, VerificationRepository: "check/repo"}},
		{ID: "unused", Endpoint: "https://unused.example", Enabled: true, UserCredentials: config.UserCredentials{Approved: true, Pull: true, VerificationRepository: "check/repo"}},
		{ID: "disabled", Endpoint: "https://disabled.example", Enabled: false, UserCredentials: config.UserCredentials{Approved: true, Pull: true, VerificationRepository: "check/repo"}},
		{ID: "unapproved", Endpoint: "https://unapproved.example", Enabled: true},
	}
	fixture.server.config.Routes = []config.Route{{Name: "team", Pull: config.Pull{Sources: []string{"harbor", "unapproved"}}, Push: config.Push{Destination: "harbor"}}}
	unauth := httptest.NewRecorder()
	fixture.server.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/admin/api/registries", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated registries = %d", unauth.Code)
	}
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	response := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/registries", nil, login.cookie, "")
	if response.Code != http.StatusOK {
		t.Fatalf("registries = %d %s", response.Code, response.Body.String())
	}
	var projection ApprovedRegistriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Registries) != 1 || projection.Registries[0].ID != "harbor" || !projection.Registries[0].Pull || !projection.Registries[0].Push {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestNormalUserCannotInspectUnapprovedOperationalSources(t *testing.T) {
	fixture := newAuthServerFixture(t)
	_, err := fixture.admins.Create(context.Background(), fixture.admin.ID, auth.NewUser{Username: "alice", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	login := loginHTTP(t, fixture.server, "alice", "another correct battery staple")
	response := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/sources", nil, login.cookie, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("normal user sources status = %d", response.Code)
	}
}

func TestAdminSourceHealthIsRedacted(t *testing.T) {
	fixture := newAuthServerFixture(t)
	healthy := registry.NewFakeConnector("internal-curated")
	unavailable := registry.NewFakeConnector("external-registry")
	unavailable.SetAvailable(false)
	fixture.server.connectors = map[string]registry.Connector{"internal-curated": healthy, "external-registry": unavailable}
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	response := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/source-health", nil, login.cookie, "")
	if response.Code != http.StatusOK {
		t.Fatalf("source health = %d %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"status":"healthy"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"unavailable"`)) || bytes.Contains(response.Body.Bytes(), []byte("registry unavailable")) {
		t.Fatalf("source health body = %s", response.Body.String())
	}
}

func TestAuthenticatedAdminHTTPBoundary(t *testing.T) {
	fixture := newAuthServerFixture(t)
	unauthenticated := httptest.NewRecorder()
	fixture.server.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/admin/api/account", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}

	wrong := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/login", map[string]any{"username": "admin", "password": "incorrect password value"}, nil, "")
	if wrong.Code != http.StatusUnauthorized || len(wrong.Result().Cookies()) != 0 {
		t.Fatalf("wrong login status/cookies = %d/%v", wrong.Code, wrong.Result().Cookies())
	}

	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	if !login.cookie.HttpOnly || !login.cookie.Secure || login.cookie.SameSite != http.SameSiteStrictMode || login.cookie.Path != "/" {
		t.Fatalf("session cookie attributes = %#v", login.cookie)
	}
	if login.csrfCookie == nil || login.csrfCookie.HttpOnly || !login.csrfCookie.Secure || login.csrfCookie.SameSite != http.SameSiteStrictMode || login.csrfCookie.Path != "/" || login.csrfCookie.Value != login.csrf {
		t.Fatalf("CSRF cookie attributes = %#v", login.csrfCookie)
	}

	account := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account", nil, login.cookie, "")
	if account.Code != http.StatusOK || bytes.Contains(account.Body.Bytes(), []byte("password_hash")) {
		t.Fatalf("account response = %d %s", account.Code, account.Body.String())
	}

	withoutCSRF := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/users", map[string]any{"username": "alice", "password": "another correct battery staple", "access": "user", "enabled": true}, login.cookie, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d body=%s", withoutCSRF.Code, withoutCSRF.Body.String())
	}
	created := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/users", map[string]any{"username": "alice", "password": "another correct battery staple", "access": "user", "enabled": true}, login.cookie, login.csrf)
	if created.Code != http.StatusCreated || bytes.Contains(created.Body.Bytes(), []byte("another correct")) {
		t.Fatalf("create user = %d %s", created.Code, created.Body.String())
	}

	logout := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/logout", nil, login.cookie, login.csrf)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d body=%s", logout.Code, logout.Body.String())
	}
	afterLogout := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account", nil, login.cookie, "")
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("after logout status = %d", afterLogout.Code)
	}
}

func TestLoginRateLimitBlocksExpensiveAuthenticationAndRecovers(t *testing.T) {
	fixture := newAuthServerFixture(t)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	fixture.server.loginLimiter = security.NewFailureLimiter(2, time.Minute, 10*time.Minute, func() time.Time { return now })

	for range 2 {
		response := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/login", map[string]any{"username": "admin", "password": "wrong password value"}, nil, "")
		if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "rate") {
			t.Fatalf("failed login = %d %s", response.Code, response.Body.String())
		}
	}
	blocked := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/login", map[string]any{"username": "admin", "password": "correct horse battery staple"}, nil, "")
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" || !strings.Contains(blocked.Body.String(), "invalid username or password") {
		t.Fatalf("blocked login = %d retry=%q %s", blocked.Code, blocked.Header().Get("Retry-After"), blocked.Body.String())
	}

	now = now.Add(10 * time.Minute)
	recovered := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/login", map[string]any{"username": "admin", "password": "correct horse battery staple"}, nil, "")
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovered login = %d %s", recovered.Code, recovered.Body.String())
	}
}

func TestBrowserLoginAndNormalUserCredentialDashboard(t *testing.T) {
	fixture := newAuthServerFixture(t)
	fixture.server.config.Sources = []config.Source{{ID: "harbor", Name: "Team Harbor", Endpoint: "https://harbor.example", Enabled: true, UserCredentials: config.UserCredentials{Approved: true, Pull: true, Push: true, VerificationRepository: "check/repo", Guidance: "Use your Harbor account."}}}
	fixture.server.config.Routes = []config.Route{{Name: "team", Pull: config.Pull{Sources: []string{"harbor"}}, Push: config.Push{Destination: "harbor"}}}
	_, err := fixture.admins.Create(context.Background(), fixture.admin.ID, auth.NewUser{Username: "alice", Password: "another correct battery staple", DisplayName: "Alice Example", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	loginPage := httptest.NewRecorder()
	fixture.server.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/login", nil))
	if loginPage.Code != http.StatusOK || !bytes.Contains(loginPage.Body.Bytes(), []byte(`id="login-form"`)) {
		t.Fatalf("login page = %d %s", loginPage.Code, loginPage.Body.String())
	}
	redirect := httptest.NewRecorder()
	fixture.server.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/", nil))
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "/login" {
		t.Fatalf("dashboard redirect = %d %q", redirect.Code, redirect.Header().Get("Location"))
	}

	login := loginHTTP(t, fixture.server, "alice", "another correct battery staple")
	landing := httptest.NewRecorder()
	landingRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	landingRequest.AddCookie(login.cookie)
	fixture.server.ServeHTTP(landing, landingRequest)
	if landing.Code != http.StatusSeeOther || landing.Header().Get("Location") != "/account" {
		t.Fatalf("normal-user landing = %d %q, want redirect to /account", landing.Code, landing.Header().Get("Location"))
	}
	dashboard := requestJSON(t, fixture.server, http.MethodGet, "/account", nil, login.cookie, "")
	body := dashboard.Body.String()
	for _, required := range []string{"Account", "Registry credentials", "Team Harbor", "Use your Harbor account.", "Add credential", `id="token-copy"`, `id="token-retained"`, `id="confirm-dialog"`, `id="mutation-notice"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("normal-user dashboard missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{"Recent requests", "Cached tag mappings", "SECRET-FIXTURE", "encrypted_secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("normal-user dashboard exposed %q", forbidden)
		}
	}
}

func TestAdministratorDashboardRendersUserManagementWithoutSecrets(t *testing.T) {
	fixture := newAuthServerFixture(t)
	_, err := fixture.admins.Create(context.Background(), fixture.admin.ID, auth.NewUser{Username: "alice", Password: "another correct battery staple", DisplayName: "Alice Example", Email: "alice@example.test", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	dashboard := requestJSON(t, fixture.server, http.MethodGet, "/admin/users", nil, login.cookie, "")
	body := dashboard.Body.String()
	for _, required := range []string{`id="logout"`, `id="users"`, `id="user-create-dialog"`, `id="user-reset-dialog"`, "Alice Example", "alice@example.test", `data-mtime="`, `data-current-access="`, "Administrator continuity", "Review changes"} {
		if !strings.Contains(body, required) {
			t.Fatalf("admin dashboard missing %q", required)
		}
	}
	for _, forbidden := range []string{"another correct battery staple", "correct horse battery staple", "password_hash", "token_hash"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("admin dashboard leaked %q", forbidden)
		}
	}
}

func TestAdminHTTPRoleAndImmediateDisableEnforcement(t *testing.T) {
	fixture := newAuthServerFixture(t)
	user, err := fixture.admins.Create(context.Background(), fixture.admin.ID, auth.NewUser{Username: "alice", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	login := loginHTTP(t, fixture.server, "alice", "another correct battery staple")
	users := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/users", nil, login.cookie, "")
	if users.Code != http.StatusForbidden {
		t.Fatalf("non-admin users status = %d", users.Code)
	}
	user.Enabled = false
	if _, err := fixture.admins.Update(context.Background(), fixture.admin.ID, *user, user.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	account := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account", nil, login.cookie, "")
	if account.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user's existing session status = %d", account.Code)
	}
}

func TestPasswordChangeRequiresCSRFAndImmediatelyRevokesSession(t *testing.T) {
	fixture := newAuthServerFixture(t)
	login := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	body := map[string]any{"current_password": "correct horse battery staple", "new_password": "replacement correct battery staple"}
	withoutCSRF := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/account/password", body, login.cookie, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("password change without CSRF = %d", withoutCSRF.Code)
	}
	changed := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/account/password", body, login.cookie, login.csrf)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("password change = %d %s", changed.Code, changed.Body.String())
	}
	account := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account", nil, login.cookie, "")
	if account.Code != http.StatusUnauthorized {
		t.Fatalf("old session after password change = %d", account.Code)
	}
	newLogin := loginHTTP(t, fixture.server, "admin", "replacement correct battery staple")
	if newLogin.cookie == nil {
		t.Fatal("new password login did not issue cookie")
	}
}

func TestAdminUserEditAndResetImmediatelyInvalidateRoleSession(t *testing.T) {
	fixture := newAuthServerFixture(t)
	user, err := fixture.admins.Create(context.Background(), fixture.admin.ID, auth.NewUser{Username: "alice", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	adminLogin := loginHTTP(t, fixture.server, "admin", "correct horse battery staple")
	userLogin := loginHTTP(t, fixture.server, "alice", "another correct battery staple")
	newAccess := metadata.UserAccessAdmin
	edit := requestJSON(t, fixture.server, http.MethodPatch, "/admin/api/users/"+user.ID, auth.UserEdit{Access: &newAccess, UpdatedAt: user.UpdatedAt}, adminLogin.cookie, adminLogin.csrf)
	if edit.Code != http.StatusOK {
		t.Fatalf("access edit = %d %s", edit.Code, edit.Body.String())
	}
	staleRoleSession := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account", nil, userLogin.cookie, "")
	if staleRoleSession.Code != http.StatusUnauthorized {
		t.Fatalf("role-changed session = %d", staleRoleSession.Code)
	}

	promotedLogin := loginHTTP(t, fixture.server, "alice", "another correct battery staple")
	users := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/users", nil, promotedLogin.cookie, "")
	if users.Code != http.StatusOK {
		t.Fatalf("promoted user list = %d %s", users.Code, users.Body.String())
	}
	reset := requestJSON(t, fixture.server, http.MethodPost, "/admin/api/users/"+user.ID+"/password", map[string]any{"password": "reset correct battery staple"}, adminLogin.cookie, adminLogin.csrf)
	if reset.Code != http.StatusOK {
		t.Fatalf("password reset = %d %s", reset.Code, reset.Body.String())
	}
	afterReset := requestJSON(t, fixture.server, http.MethodGet, "/admin/api/account", nil, promotedLogin.cookie, "")
	if afterReset.Code != http.StatusUnauthorized {
		t.Fatalf("password-reset session = %d", afterReset.Code)
	}
	loginHTTP(t, fixture.server, "alice", "reset correct battery staple")
}

type httpLogin struct {
	cookie     *http.Cookie
	csrfCookie *http.Cookie
	csrf       string
}

func loginHTTP(t *testing.T, handler http.Handler, username, password string) httpLogin {
	t.Helper()
	response := requestJSON(t, handler, http.MethodPost, "/admin/api/login", map[string]any{"username": username, "password": password}, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil || body.CSRF == "" {
		t.Fatalf("login cookies/csrf = %v/%q", cookies, body.CSRF)
	}
	return httpLogin{cookie: sessionCookie, csrfCookie: csrfCookie, csrf: body.CSRF}
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &encoded)
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
