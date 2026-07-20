package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"regstair/internal/app"
	"regstair/internal/auth"
	"regstair/internal/metadata"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "bootstrap" {
		if err := runAdminBootstrap(os.Args[3:]); err != nil {
			slog.Error("administrator bootstrap failed", "error", err)
			os.Exit(1)
		}
		return
	}
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
	flag.StringVar(&options.ConfigPath, "config", "config/regstair.example.yaml", "path to Regstair YAML config")
	flag.StringVar(&options.ContentRoot, "content-root", "/var/lib/regstair/content", "path to local content-addressed store")
	flag.StringVar(&options.MetadataPath, "metadata-path", "", "path to SQLite metadata database; defaults under content root")
	flag.StringVar(&options.ListenAddr, "listen", ":8080", "HTTP listen address")
	flag.BoolVar(&options.StubSources, "stub-sources", false, "use in-memory stub registry connectors")
	flag.BoolVar(&options.StubFixtures, "stub-fixtures", false, "load demo fixtures into stub registry connectors")
	flag.StringVar(&options.CredentialKeyID, "credential-key-id", "", "active per-user registry credential encryption key id")
	flag.StringVar(&options.CredentialKeyFile, "credential-key-file", "", "path to mounted 32-byte per-user registry credential encryption key")
	flag.Parse()

	if err := run(options); err != nil {
		slog.Error("regstair stopped", "error", err)
		os.Exit(1)
	}
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

func runAdminBootstrap(args []string) error {
	flags := flag.NewFlagSet("regstair admin bootstrap", flag.ContinueOnError)
	var metadataPath, username, passwordFile string
	flags.StringVar(&metadataPath, "metadata-path", "/var/lib/regstair/content/metadata/regstair.db", "path to SQLite metadata database")
	flags.StringVar(&username, "username", "", "local administrator username")
	flags.StringVar(&passwordFile, "password-file", "", "path to a root-readable file containing the administrator password")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if username == "" || passwordFile == "" {
		return fmt.Errorf("username and password-file are required")
	}
	contents, err := os.ReadFile(passwordFile)
	if err != nil {
		return fmt.Errorf("read administrator password file: %w", err)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r")
	repo, err := metadata.NewSQLiteRepository(metadataPath)
	if err != nil {
		return err
	}
	defer repo.Close()
	service := auth.NewAccountService(repo, auth.NewPasswordHasher(auth.DefaultPasswordParams, nil))
	user, err := service.BootstrapAdmin(context.Background(), username, password)
	if err != nil {
		return err
	}
	slog.Info("administrator bootstrap completed", "username", user.Username, "user_id", user.ID)
	return nil
}

func run(options app.Options) error {
	application, err := app.New(options)
	if err != nil {
		return err
	}
	defer application.Close()

	server := application.Server()
	errc := make(chan error, 1)
	go func() {
		slog.Info("starting regstair", "listen", application.ListenAddr())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errc:
		return err
	case signal := <-stop:
		slog.Info("shutting down regstair", "signal", signal.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}
