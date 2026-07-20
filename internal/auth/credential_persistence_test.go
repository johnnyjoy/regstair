package auth

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"regstair/internal/metadata"
)

func TestEncryptedRegistryCredentialPersistenceContainsNoPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regstair.db")
	repo, err := metadata.NewSQLiteRepository(path)
	if err != nil {
		t.Fatalf("NewSQLiteRepository() error = %v", err)
	}
	ctx := context.Background()
	if _, err := repo.CreateUser(ctx, metadata.User{ID: "user-1", Username: "alice", PasswordHash: "argon2id-fixture", Access: metadata.UserAccessUser, Enabled: true}); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	keyring, err := NewSecretKeyring("key-1", map[string][]byte{"key-1": bytes.Repeat([]byte{7}, 32)}, bytes.NewReader(bytes.Repeat([]byte{8}, 12)))
	if err != nil {
		t.Fatalf("NewSecretKeyring() error = %v", err)
	}
	plaintext := "DO-NOT-PERSIST-REGISTRY-PASSWORD"
	encrypted, err := keyring.Encrypt("credential-1", "user-1", "harbor", []byte(plaintext))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	credential := metadata.RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice-upstream", EncryptedSecret: encrypted}
	audit := metadata.AuditEvent{ActorUserID: "user-1", ActorRole: "user", Action: "credential.created", TargetType: "registry_credential", TargetID: credential.ID, Outcome: "success", Details: map[string]string{"source_id": "harbor"}}
	if _, err := repo.SaveRegistryCredential(ctx, credential, audit); err != nil {
		t.Fatalf("SaveRegistryCredential() error = %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	database, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(database) error = %v", err)
	}
	if bytes.Contains(database, []byte(plaintext)) {
		t.Fatal("SQLite database contains plaintext registry password")
	}
}

func TestSQLitePersistsSecurityStateWithoutRecoverableAuthenticationSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regstair.db")
	repo, err := metadata.NewSQLiteRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	password := "WEB-PASSWORD-CANARY-123"
	hasher := NewPasswordHasher(DefaultPasswordParams, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	admin, err := NewAccountService(repo, hasher).BootstrapAdmin(context.Background(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 15, 0, 0, 0, time.UTC)
	token, err := NewDockerTokenService(repo, func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{2}, 128))).Issue(context.Background(), admin.ID, "release-check", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewWebSessionService(repo, func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{3}, 128)), 30*time.Minute, 12*time.Hour).Create(context.Background(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	upstreamSecret := "UPSTREAM-CREDENTIAL-CANARY"
	keyring, err := NewSecretKeyring("key-1", map[string][]byte{"key-1": bytes.Repeat([]byte{4}, 32)}, bytes.NewReader(bytes.Repeat([]byte{5}, 12)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := keyring.Encrypt("credential-1", admin.ID, "harbor", []byte(upstreamSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveRegistryCredential(context.Background(), metadata.RegistryCredential{ID: "credential-1", UserID: admin.ID, SourceID: "harbor", Username: "robot", EncryptedSecret: encrypted}, metadata.AuditEvent{ActorUserID: admin.ID, ActorRole: "admin", Action: "credential.created", TargetType: "registry_credential", TargetID: "credential-1", Outcome: "success", Details: map[string]string{"source_id": "harbor"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	persisted := []byte{}
	for _, suffix := range []string{"", "-wal"} {
		contents, readErr := os.ReadFile(path + suffix)
		if readErr == nil {
			persisted = append(persisted, contents...)
		}
	}
	for name, secret := range map[string]string{"web password": password, "Docker token": token.Secret, "session": session.Secret, "CSRF token": session.CSRFToken, "upstream credential": upstreamSecret} {
		if bytes.Contains(persisted, []byte(secret)) {
			t.Fatalf("SQLite contains recoverable %s", name)
		}
	}
	for name, marker := range map[string][]byte{"Argon2id password hash": []byte("$argon2id$"), "encrypted credential envelope": []byte(`"ciphertext"`)} {
		if !bytes.Contains(persisted, marker) {
			t.Fatalf("SQLite is missing expected %s", name)
		}
	}
}
