package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"regstair/internal/admin"
	"regstair/internal/auth"
	"regstair/internal/config"
	"regstair/internal/content"
	"regstair/internal/gateway"
	"regstair/internal/metadata"
	"regstair/internal/policy"
	"regstair/internal/registry"
	"regstair/internal/resolution"
	"regstair/internal/security"
)

type Options struct {
	ConfigPath        string
	ContentRoot       string
	MetadataPath      string
	ListenAddr        string
	StubSources       bool
	StubFixtures      bool
	CredentialKeyID   string
	CredentialKeyFile string
}

type App struct {
	listenAddr        string
	handler           http.Handler
	store             content.Store
	repo              metadata.Repository
	metadataPath      string
	closeMetadata     func() error
	credentialKeyring *auth.SecretKeyring
}

func New(options Options) (*App, error) {
	if options.ConfigPath == "" {
		return nil, fmt.Errorf("config path is required")
	}
	if options.ContentRoot == "" {
		return nil, fmt.Errorf("content root is required")
	}
	if options.ListenAddr == "" {
		options.ListenAddr = ":8080"
	}

	fileConfig, err := config.LoadFile(options.ConfigPath)
	if err != nil {
		return nil, err
	}
	policyConfig, err := fileConfig.PolicyConfig()
	if err != nil {
		return nil, err
	}
	policyEngine, err := policy.NewEngine(policyConfig)
	if err != nil {
		return nil, err
	}

	store, err := content.NewFileStore(options.ContentRoot)
	if err != nil {
		return nil, err
	}
	metadataRepo, err := metadata.NewSQLiteRepository(metadataPath(options))
	if err != nil {
		return nil, err
	}
	users, err := metadataRepo.ListUsers(context.Background())
	if err != nil {
		return nil, fmt.Errorf("inspect local account bootstrap state: %w", err)
	}
	if len(users) == 0 {
		slog.Warn("first-run setup required; operations remain closed until the first administrator is created at /setup")
	}
	passwordHasher := auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)
	accountService := auth.NewAccountService(metadataRepo, passwordHasher)
	sessionService := auth.NewWebSessionService(metadataRepo, nil, nil, 30*time.Minute, 12*time.Hour)
	adminAccountService := auth.NewAdminAccountService(metadataRepo, passwordHasher)
	dockerTokenService := auth.NewDockerTokenService(metadataRepo, nil, nil)
	ociTokenService, err := auth.NewOCITokenService(dockerTokenService, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize OCI token service: %w", err)
	}
	loginLimiter := security.NewFailureLimiter(5, 5*time.Minute, 15*time.Minute, nil)
	dockerLimiter := security.NewFailureLimiter(5, 5*time.Minute, 15*time.Minute, nil)
	var registryCredentialService *auth.RegistryCredentialService
	var credentialKeyring *auth.SecretKeyring
	if options.CredentialKeyID != "" {
		if options.CredentialKeyFile == "" {
			return nil, fmt.Errorf("credential key file is required when credential key id is configured")
		}
		keyring, err := auth.LoadSecretKeyring(options.CredentialKeyID, map[string]string{options.CredentialKeyID: options.CredentialKeyFile}, nil)
		if err != nil {
			return nil, err
		}
		registryCredentialService = auth.NewRegistryCredentialService(metadataRepo, keyring, auth.NewHarborCredentialVerifier(nil), fileConfig.Sources)
		credentialKeyring = keyring
	} else {
		for _, source := range fileConfig.Sources {
			if source.Enabled && source.UserCredentials.Approved {
				return nil, fmt.Errorf("source %q uses current-user authentication but no credential encryption key is configured", source.ID)
			}
		}
		slog.Warn("per-user registry credential APIs unavailable; no credential encryption key file configured")
	}
	connectors, err := buildConnectors(*fileConfig, options)
	if err != nil {
		return nil, err
	}

	resolverOptions := []resolution.ResolverOption{resolution.WithAuthorizer(auth.RouteAuthorizer{})}
	if credentialKeyring != nil {
		selector := auth.NewRuntimeCredentialSelector(metadataRepo, credentialKeyring, fileConfig.Sources, connectors, nil)
		resolverOptions = append(resolverOptions, resolution.WithConnectorProvider(selector))
	}
	pullResolver := resolution.NewPullResolver(policyEngine, store, metadataRepo, connectors, resolverOptions...)
	pushResolver := resolution.NewPushResolver(policyEngine, store, metadataRepo, connectors, resolverOptions...)
	gatewayOptions := []gateway.Option{
		gateway.WithContentStore(store),
		gateway.WithPuller(pullResolver),
		gateway.WithPusher(pushResolver),
	}
	gatewayOptions = append(gatewayOptions, gateway.WithAuthenticator(auth.NewGatewayAuthenticator(ociTokenService)))
	gatewayOptions = append(gatewayOptions, gateway.WithAuthenticationLimiter(dockerLimiter))
	gatewayServer, err := gateway.NewServer(gatewayOptions...)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	adminServer := admin.NewServer(admin.Config{
		Config:       *fileConfig,
		Repo:         metadataRepo,
		Store:        store,
		Auth:         &admin.AuthConfig{Accounts: accountService, Sessions: sessionService, Admins: adminAccountService, Tokens: dockerTokenService, Credentials: registryCredentialService},
		Connectors:   connectors,
		LoginLimiter: loginLimiter,
	})
	app := &App{listenAddr: options.ListenAddr, handler: security.RecoverHTTP(mux, nil), store: store, repo: metadataRepo, metadataPath: metadataPath(options), closeMetadata: metadataRepo.Close, credentialKeyring: credentialKeyring}
	mux.Handle("/v2/", gatewayServer)
	mux.HandleFunc("/auth/token", gatewayServer.ServeTokenHTTP)
	mux.Handle("/admin/api/", adminServer)
	mux.Handle("/admin/", adminServer)
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.HandleFunc("/readyz", app.handleReady)
	mux.Handle("/", adminServer)

	return app, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) ListenAddr() string {
	return a.listenAddr
}

func (a *App) Server() *http.Server {
	return &http.Server{
		Addr:              a.listenAddr,
		Handler:           a.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func (a *App) Close() error {
	if a.closeMetadata == nil {
		return nil
	}
	return a.closeMetadata()
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.repo == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "metadata"})
		return
	}
	users, err := a.repo.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "metadata"})
		return
	}
	securityRepo, ok := a.repo.(metadata.SecurityRepository)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "metadata"})
		return
	}
	for _, user := range users {
		credentials, err := securityRepo.ListRegistryCredentialsForUser(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "metadata"})
			return
		}
		for _, credential := range credentials {
			if a.credentialKeyring == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "credential_key"})
				return
			}
			plaintext, err := a.credentialKeyring.Decrypt(credential.ID, credential.UserID, credential.SourceID, credential.EncryptedSecret)
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "credential_key"})
				return
			}
			for i := range plaintext {
				plaintext[i] = 0
			}
		}
	}
	if _, err := a.store.HasBlob(r.Context(), zeroDigest()); err != nil && err != context.Canceled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "component": "content"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func buildConnectors(cfg config.Config, options Options) (map[string]registry.Connector, error) {
	connectors := map[string]registry.Connector{}
	for _, source := range cfg.Sources {
		if !source.Enabled {
			continue
		}
		if options.StubSources {
			connector := registry.NewFakeConnector(source.ID)
			if options.StubFixtures {
				addStubFixtures(connector)
			}
			connectors[source.ID] = connector
			continue
		}
		httpOptions := []registry.HTTPOption{}
		if len(source.Auth.TokenHosts) > 0 {
			httpOptions = append(httpOptions, registry.WithAllowedTokenHosts(source.Auth.TokenHosts...))
		}
		connector, err := registry.NewHTTPConnector(source.ID, source.Endpoint, nil, httpOptions...)
		if err != nil {
			return nil, fmt.Errorf("create connector %q: %w", source.ID, err)
		}
		connectors[source.ID] = connector
	}
	return connectors, nil
}

func metadataPath(options Options) string {
	if options.MetadataPath != "" {
		return options.MetadataPath
	}
	return filepath.Join(options.ContentRoot, "metadata", "regstair.db")
}

func addStubFixtures(connector *registry.FakeConnector) {
	blob := []byte("hello regstair")
	blobDigest := digestBytes(blob)
	connector.AddBlob(blobDigest, blob)

	body := []byte(`{"schemaVersion":2,"config":{"digest":"` + blobDigest + `"},"layers":[{"digest":"` + blobDigest + `"}]}`)
	connector.AddManifest("library/nginx", "1.27", registry.Manifest{
		Descriptor: registry.Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    digestBytes(body),
			Size:      int64(len(body)),
		},
		Content:     bytes.Clone(body),
		BlobDigests: []string{blobDigest},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func zeroDigest() string {
	return "sha256:0000000000000000000000000000000000000000000000000000000000000000"
}
