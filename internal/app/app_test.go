package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"regstair/internal/auth"
	"regstair/internal/metadata"
)

func TestNewLoadsConfigAndServesHealthAndGateway(t *testing.T) {
	app, err := New(Options{
		ConfigPath:   filepath.Join("..", "..", "config", "regstair.example.yaml"),
		ContentRoot:  t.TempDir(),
		ListenAddr:   "127.0.0.1:0",
		StubSources:  true,
		StubFixtures: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "health", path: "/healthz", want: http.StatusOK},
		{name: "ready", path: "/readyz", want: http.StatusOK},
		{name: "gateway ping", path: "/v2/", want: http.StatusOK},
		{name: "pre-bootstrap application", path: "/", want: http.StatusSeeOther},
		{name: "pre-bootstrap admin sources", path: "/admin/api/sources", want: http.StatusPreconditionRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			app.Handler().ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d body %s", response.Code, tt.want, response.Body.String())
			}
			if strings.HasPrefix(tt.path, "/admin") && response.Header().Get("X-Regstair-Bootstrap-Required") != "true" {
				t.Fatal("pre-bootstrap admin response does not advertise bootstrap requirement")
			}
		})
	}
}

func TestNewRequiresConfigPath(t *testing.T) {
	_, err := New(Options{ContentRoot: t.TempDir()})
	if err == nil {
		t.Fatal("New() error = nil, want missing config path error")
	}
}

func TestNewCredentialEncryptionKeyConfiguration(t *testing.T) {
	base := Options{ConfigPath: filepath.Join("..", "..", "config", "regstair.example.yaml"), ContentRoot: t.TempDir(), ListenAddr: "127.0.0.1:0", StubSources: true}
	base.CredentialKeyID = "key-1"
	if _, err := New(base); err == nil {
		t.Fatal("New() accepted key id without key file")
	}
	keyFile := filepath.Join(t.TempDir(), "credential-key")
	if err := os.WriteFile(keyFile, bytes.Repeat([]byte{5}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	base.CredentialKeyFile = keyFile
	if _, err := New(base); err != nil {
		t.Fatalf("New() with mounted credential key error = %v", err)
	}
}

func TestNewRejectsCurrentUserSourceWithoutCredentialKey(t *testing.T) {
	configPath := writeAppConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: https://harbor.example
    enabled: true
    auth:
      mode: current_user
    user_credentials:
      approved: true
      pull: true
      push: true
      verification_repository: regstair/check
routes: []
`)
	_, err := New(Options{ConfigPath: configPath, ContentRoot: t.TempDir(), ListenAddr: "127.0.0.1:0", StubSources: true})
	if err == nil || !strings.Contains(err.Error(), "no credential encryption key") {
		t.Fatalf("New() error = %v, want fail-closed credential key error", err)
	}
}

func TestNewCreatesSQLiteMetadataRepository(t *testing.T) {
	contentRoot := t.TempDir()

	_, err := New(Options{
		ConfigPath:  filepath.Join("..", "..", "config", "regstair.example.yaml"),
		ContentRoot: contentRoot,
		ListenAddr:  "127.0.0.1:0",
		StubSources: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(contentRoot, "metadata", "regstair.db")); err != nil {
		t.Fatalf("metadata sqlite db was not created: %v", err)
	}
}

func TestReadinessDetectsClosedMetadataWithoutLeakingInternalError(t *testing.T) {
	application, err := New(Options{ConfigPath: filepath.Join("..", "..", "config", "regstair.example.yaml"), ContentRoot: t.TempDir(), ListenAddr: "127.0.0.1:0", StubSources: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"component":"metadata"`) {
		t.Fatalf("readiness = %d %s", response.Code, response.Body.String())
	}
	for _, leaked := range []string{"database is closed", "sql:", "sqlite", filepath.Base(application.metadataPath)} {
		if leaked != "" && strings.Contains(strings.ToLower(response.Body.String()), strings.ToLower(leaked)) {
			t.Fatalf("readiness leaked %q: %s", leaked, response.Body.String())
		}
	}
}

func TestRestoredCredentialReadinessRequiresCorrectEncryptionKey(t *testing.T) {
	originalKey := bytes.Repeat([]byte{6}, 32)
	wrongKey := bytes.Repeat([]byte{7}, 32)
	for _, tt := range []struct {
		name       string
		configured bool
		key        []byte
		want       int
	}{
		{name: "correct key", configured: true, key: originalKey, want: http.StatusOK},
		{name: "missing key", want: http.StatusServiceUnavailable},
		{name: "wrong key", configured: true, key: wrongKey, want: http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			contentRoot := t.TempDir()
			database := filepath.Join(contentRoot, "metadata", "regstair.db")
			repo, err := metadata.NewSQLiteRepository(database)
			if err != nil {
				t.Fatal(err)
			}
			user, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			ring, err := auth.NewSecretKeyring("restored", map[string][]byte{"restored": originalKey}, bytes.NewReader(bytes.Repeat([]byte{8}, 12)))
			if err != nil {
				t.Fatal(err)
			}
			encrypted, err := ring.Encrypt("credential-1", user.ID, "harbor-team-a", []byte("RESTORED-UPSTREAM-CANARY"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.SaveRegistryCredential(context.Background(), metadata.RegistryCredential{ID: "credential-1", UserID: user.ID, SourceID: "harbor-team-a", Username: "robot", EncryptedSecret: encrypted}, metadata.AuditEvent{ActorUserID: user.ID, ActorRole: "user", Action: "credential.created", TargetType: "registry_credential", TargetID: "credential-1", Outcome: "success"}); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			options := Options{ConfigPath: filepath.Join("..", "..", "config", "regstair.example.yaml"), ContentRoot: contentRoot, ListenAddr: "127.0.0.1:0", StubSources: true}
			if tt.configured {
				keyFile := filepath.Join(t.TempDir(), "credential.key")
				if err := os.WriteFile(keyFile, tt.key, 0o600); err != nil {
					t.Fatal(err)
				}
				options.CredentialKeyID, options.CredentialKeyFile = "restored", keyFile
			}
			application, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			defer application.Close()
			response := httptest.NewRecorder()
			application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != tt.want {
				t.Fatalf("readiness = %d %s, want %d", response.Code, response.Body.String(), tt.want)
			}
			if tt.want != http.StatusOK && (!strings.Contains(response.Body.String(), `"component":"credential_key"`) || strings.Contains(response.Body.String(), "RESTORED-UPSTREAM-CANARY")) {
				t.Fatalf("credential-key readiness = %s", response.Body.String())
			}
		})
	}
}

func TestNewAppliesConfiguredClientAuthToGatewayOnly(t *testing.T) {
	t.Setenv("REGSTAIR_CLIENT_CI_USERNAME", "ci")
	t.Setenv("REGSTAIR_CLIENT_CI_PASSWORD", "secret")
	configPath := writeAppConfig(t, `
version: 1
clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
sources: []
routes: []
`)

	app, err := New(Options{
		ConfigPath:  configPath,
		ContentRoot: t.TempDir(),
		ListenAddr:  "127.0.0.1:0",
		StubSources: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	health := httptest.NewRecorder()
	app.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got, want := health.Code, http.StatusOK; got != want {
		t.Fatalf("health status = %d, want %d", got, want)
	}

	unauthorized := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if got, want := unauthorized.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("unauthorized status = %d, want %d", got, want)
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	request.SetBasicAuth("ci", "secret")
	app.Handler().ServeHTTP(authorized, request)
	if got, want := authorized.Code, http.StatusOK; got != want {
		t.Fatalf("authorized status = %d, want %d body %s", got, want, authorized.Body.String())
	}
}

func TestNewLocalDockerTokenAuthenticationPreservesAnonymousGateway(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "regstair.db")
	repo, err := metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	hasher := auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)
	user, err := auth.NewAccountService(repo, hasher).BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := auth.NewDockerTokenService(repo, nil, nil).Issue(context.Background(), user.ID, "docker", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	application, err := New(Options{ConfigPath: filepath.Join("..", "..", "config", "regstair.example.yaml"), ContentRoot: dir, MetadataPath: database, ListenAddr: "127.0.0.1:0", StubSources: true})
	if err != nil {
		t.Fatal(err)
	}
	protectedAdmin := httptest.NewRecorder()
	application.Handler().ServeHTTP(protectedAdmin, httptest.NewRequest(http.MethodGet, "/admin/api/sources", nil))
	if protectedAdmin.Code != http.StatusUnauthorized {
		t.Fatalf("post-bootstrap admin status = %d", protectedAdmin.Code)
	}

	anonymous := httptest.NewRecorder()
	application.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous gateway status = %d body=%s", anonymous.Code, anonymous.Body.String())
	}
	valid := httptest.NewRecorder()
	validRequest := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	validRequest.SetBasicAuth("admin", issued.Secret)
	application.Handler().ServeHTTP(valid, validRequest)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid local token status = %d body=%s", valid.Code, valid.Body.String())
	}
	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	invalidRequest.SetBasicAuth("admin", "invalid-token")
	application.Handler().ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid local token status = %d", invalid.Code)
	}
}

func TestDockerCLILoginWithLocalToken(t *testing.T) {
	if os.Getenv("REGSTAIR_DOCKER_CLI_TEST") != "1" {
		t.Skip("set REGSTAIR_DOCKER_CLI_TEST=1 to run Docker CLI compatibility")
	}
	dir := t.TempDir()
	database := filepath.Join(dir, "regstair.db")
	repo, err := metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewAccountService(repo, auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)).BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := auth.NewDockerTokenService(repo, nil, nil).Issue(context.Background(), user.ID, "docker-cli", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	application, err := New(Options{ConfigPath: filepath.Join("..", "..", "config", "regstair.example.yaml"), ContentRoot: dir, MetadataPath: database, ListenAddr: "127.0.0.1:0", StubSources: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	dockerConfig := filepath.Join(dir, "docker-config")
	if err := os.Mkdir(dockerConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), "docker", "login", host, "--username", "admin", "--password-stdin")
	command.Env = append(os.Environ(), "DOCKER_CONFIG="+dockerConfig)
	command.Stdin = strings.NewReader(issued.Secret + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker login error = %v output=%s", err, output)
	}
}

func writeAppConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "regstair.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write app config: %v", err)
	}
	return path
}
