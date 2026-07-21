package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"regstair/internal/app"
	"regstair/internal/auth"
	"regstair/internal/metadata"
	"regstair/internal/tlsidentity"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "reset-password" {
		if err := runAdminResetPassword(os.Args[3:]); err != nil {
			slog.Error("administrator password recovery failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "rotate-credential-key" {
		if err := runAdminRotateCredentialKey(os.Args[3:]); err != nil {
			slog.Error("credential encryption key rotation failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "backup" {
		if err := runAdminBackup(os.Args[3:]); err != nil {
			slog.Error("backup failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "restore" {
		if err := runAdminRestore(os.Args[3:]); err != nil {
			slog.Error("restore failed", "error", err)
			os.Exit(1)
		}
		return
	}
	var options app.Options
	var tlsDir, tlsHosts string
	var generateCredentialKey bool
	flag.StringVar(&options.ConfigPath, "config", "config/regstair.example.yaml", "path to Regstair YAML config")
	flag.StringVar(&options.ContentRoot, "content-root", "/var/lib/regstair/content", "path to local content-addressed store")
	flag.StringVar(&options.MetadataPath, "metadata-path", "", "path to SQLite metadata database; defaults under content root")
	flag.StringVar(&options.ListenAddr, "listen", ":8080", "HTTP redirect and health-check listen address")
	flag.StringVar(&options.HTTPSListenAddr, "https-listen", ":8443", "HTTPS application and OCI listen address; empty disables HTTPS")
	flag.StringVar(&options.HTTPSRedirectPort, "https-public-port", "", "external HTTPS port used by HTTP redirects; defaults to the HTTPS listen port")
	flag.StringVar(&tlsDir, "tls-dir", "/var/lib/regstair/tls", "directory for generated persistent TLS identity")
	flag.StringVar(&tlsHosts, "tls-hosts", "localhost,127.0.0.1,::1,regstair", "comma-separated DNS names and IP addresses for a generated certificate")
	flag.StringVar(&options.TLSCertFile, "tls-cert-file", "", "operator-supplied TLS server certificate")
	flag.StringVar(&options.TLSKeyFile, "tls-key-file", "", "operator-supplied TLS server private key")
	flag.StringVar(&options.TLSCAFile, "tls-ca-file", "", "CA certificate offered to Regstair clients")
	flag.BoolVar(&options.StubSources, "stub-sources", false, "use in-memory stub registry connectors")
	flag.BoolVar(&options.StubFixtures, "stub-fixtures", false, "load demo fixtures into stub registry connectors")
	flag.StringVar(&options.CredentialKeyID, "credential-key-id", "", "active per-user registry credential encryption key id")
	flag.StringVar(&options.CredentialKeyFile, "credential-key-file", "", "path to mounted 32-byte per-user registry credential encryption key")
	flag.BoolVar(&generateCredentialKey, "generate-credential-key", false, "create the credential encryption key on first start when it does not exist")
	flag.Parse()
	if err := configureTLS(&options, tlsDir, tlsHosts); err != nil {
		slog.Error("configure TLS", "error", err)
		os.Exit(1)
	}
	if generateCredentialKey {
		if options.CredentialKeyID == "" || options.CredentialKeyFile == "" {
			slog.Error("generate credential key", "error", "credential-key-id and credential-key-file are required")
			os.Exit(1)
		}
		if err := ensureCredentialKey(options.CredentialKeyFile); err != nil {
			slog.Error("generate credential key", "error", err)
			os.Exit(1)
		}
	}

	if err := run(options); err != nil {
		slog.Error("regstair stopped", "error", err)
		os.Exit(1)
	}
}

func ensureCredentialKey(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("credential key path is not a regular file")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect credential key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate credential key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create credential key: %w", err)
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return fmt.Errorf("write credential key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close credential key: %w", err)
	}
	return nil
}

func configureTLS(options *app.Options, tlsDir, tlsHosts string) error {
	if options.HTTPSListenAddr == "" {
		return nil
	}
	provided := 0
	for _, path := range []string{options.TLSCertFile, options.TLSKeyFile, options.TLSCAFile} {
		if path != "" {
			provided++
		}
	}
	if provided > 0 {
		if provided != 3 {
			return fmt.Errorf("tls-cert-file, tls-key-file, and tls-ca-file must be supplied together")
		}
		if _, err := tls.LoadX509KeyPair(options.TLSCertFile, options.TLSKeyFile); err != nil {
			return fmt.Errorf("load operator TLS identity: %w", err)
		}
		return nil
	}
	identity, err := tlsidentity.Ensure(tlsDir, strings.Split(tlsHosts, ","))
	if err != nil {
		return err
	}
	options.TLSCertFile, options.TLSKeyFile, options.TLSCAFile = identity.CertFile, identity.KeyFile, identity.CACertFile
	return nil
}

func runAdminRotateCredentialKey(args []string) error {
	flags := flag.NewFlagSet("regstair admin rotate-credential-key", flag.ContinueOnError)
	var metadataPath, oldKeyID, oldKeyFile, newKeyID, newKeyFile string
	flags.StringVar(&metadataPath, "metadata-path", "/var/lib/regstair/content/metadata/regstair.db", "path to SQLite metadata database")
	flags.StringVar(&oldKeyID, "old-key-id", "", "key id currently referenced by stored credential envelopes")
	flags.StringVar(&oldKeyFile, "old-key-file", "", "path to the old 32-byte credential key")
	flags.StringVar(&newKeyID, "new-key-id", "", "new active credential key id")
	flags.StringVar(&newKeyFile, "new-key-file", "", "path to the new 32-byte credential key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if oldKeyID == "" || oldKeyFile == "" || newKeyID == "" || newKeyFile == "" || oldKeyID == newKeyID {
		return fmt.Errorf("distinct old and new key ids and files are required")
	}
	keyring, err := auth.LoadSecretKeyring(newKeyID, map[string]string{oldKeyID: oldKeyFile, newKeyID: newKeyFile}, nil)
	if err != nil {
		return err
	}
	newOnly, err := auth.LoadSecretKeyring(newKeyID, map[string]string{newKeyID: newKeyFile}, nil)
	if err != nil {
		return err
	}
	repo, err := metadata.NewSQLiteRepository(metadataPath)
	if err != nil {
		return err
	}
	defer repo.Close()
	users, err := repo.ListUsers(context.Background())
	if err != nil {
		return err
	}
	replacements := map[string]string{}
	for _, user := range users {
		credentials, err := repo.ListRegistryCredentialsForUser(context.Background(), user.ID)
		if err != nil {
			return err
		}
		for _, credential := range credentials {
			rotated, err := keyring.Reencrypt(credential.ID, credential.UserID, credential.SourceID, credential.EncryptedSecret)
			if err != nil {
				return fmt.Errorf("credential %q cannot be decrypted with the supplied old key", credential.ID)
			}
			plaintext, err := newOnly.Decrypt(credential.ID, credential.UserID, credential.SourceID, rotated)
			if err != nil {
				return fmt.Errorf("verify rotated credential %q: %w", credential.ID, err)
			}
			for i := range plaintext {
				plaintext[i] = 0
			}
			replacements[credential.ID] = rotated
		}
	}
	audit := metadata.AuditEvent{ActorRole: "system", Action: "credential.key_rotated", TargetType: "credential_key", TargetID: newKeyID, Outcome: "success", Details: map[string]string{"previous_key_id": oldKeyID, "new_key_id": newKeyID, "credential_count": strconv.Itoa(len(replacements))}}
	count, err := repo.RotateRegistryCredentialSecrets(context.Background(), replacements, audit)
	if err != nil {
		return err
	}
	slog.Info("credential encryption key rotation completed", "previous_key_id", oldKeyID, "new_key_id", newKeyID, "credential_count", count)
	return nil
}

func runAdminResetPassword(args []string) error {
	flags := flag.NewFlagSet("regstair admin reset-password", flag.ContinueOnError)
	var metadataPath, username, passwordFile string
	flags.StringVar(&metadataPath, "metadata-path", "/var/lib/regstair/content/metadata/regstair.db", "path to SQLite metadata database")
	flags.StringVar(&username, "username", "", "local administrator username")
	flags.StringVar(&passwordFile, "password-file", "", "path to a root-readable file containing the replacement password")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if username == "" || passwordFile == "" {
		return fmt.Errorf("username and password-file are required")
	}
	contents, err := os.ReadFile(passwordFile)
	if err != nil {
		return fmt.Errorf("read replacement password file: %w", err)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r")
	repo, err := metadata.NewSQLiteRepository(metadataPath)
	if err != nil {
		return err
	}
	defer repo.Close()
	user, err := repo.FindUserByUsername(context.Background(), username)
	if err != nil {
		return err
	}
	if user == nil || user.Access != metadata.UserAccessAdmin {
		return fmt.Errorf("enabled or disabled administrator %q was not found", username)
	}
	hash, err := auth.NewPasswordHasher(auth.DefaultPasswordParams, nil).Hash(password)
	if err != nil {
		return err
	}
	updated, err := repo.ChangeUserPassword(context.Background(), user.ID, user.UpdatedAt, hash, metadata.AuditEvent{ActorRole: "system", Action: "user.password_recovered", TargetType: "user", TargetID: user.ID, Outcome: "success"})
	if err != nil {
		return err
	}
	slog.Info("administrator password recovery completed", "username", updated.Username, "user_id", updated.ID)
	return nil
}

func run(options app.Options) error {
	application, err := app.New(options)
	if err != nil {
		return err
	}
	defer application.Close()

	server := application.Server()
	errc := make(chan error, 2)
	go func() {
		slog.Info("starting Regstair HTTP redirect and health listener", "listen", application.ListenAddr())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()
	var tlsServer *http.Server
	if application.HTTPSListenAddr() != "" {
		tlsServer = application.HTTPSServer()
		tlsServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		certFile, keyFile := application.TLSFiles()
		go func() {
			slog.Info("starting Regstair HTTPS application and OCI listener", "listen", application.HTTPSListenAddr())
			if err := tlsServer.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
				return
			}
			errc <- nil
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errc:
		return err
	case signal := <-stop:
		slog.Info("shutting down regstair", "signal", signal.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
		if tlsServer != nil {
			return tlsServer.Shutdown(ctx)
		}
		return nil
	}
}
