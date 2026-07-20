package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"regstair/internal/auth"
	"regstair/internal/metadata"
)

func TestRunAdminResetPasswordRevokesIdentityAndAuditsRecovery(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "regstair.db")
	newFile := filepath.Join(dir, "new")
	repo, err := metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewAccountService(repo, auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)).BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewDockerTokenService(repo, nil, nil)
	issued, err := tokens.Issue(context.Background(), user.ID, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewWebSessionService(repo, nil, nil, time.Hour, time.Hour)
	session, err := sessions.Create(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.Close()
	if err := os.WriteFile(newFile, []byte("replacement correct battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runAdminResetPassword([]string{"-metadata-path", database, "-username", "admin", "-password-file", newFile}); err != nil {
		t.Fatal(err)
	}
	repo, err = metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := auth.NewDockerTokenService(repo, nil, nil).Authenticate(context.Background(), "admin", issued.Secret); err == nil {
		t.Fatal("recovery left Docker token active")
	}
	if _, err := auth.NewWebSessionService(repo, nil, nil, time.Hour, time.Hour).Authenticate(context.Background(), session.Secret); err == nil {
		t.Fatal("recovery left web session active")
	}
	if _, err := auth.NewAccountService(repo, auth.NewPasswordHasher(auth.DefaultPasswordParams, nil)).AuthenticateWeb(context.Background(), "admin", "replacement correct battery staple"); err != nil {
		t.Fatalf("replacement password failed: %v", err)
	}
	events, _ := repo.ListAuditEvents(context.Background(), 10)
	found := false
	for _, event := range events {
		if event.Action == "user.password_recovered" {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery audit event missing")
	}
}

func TestRunAdminRotateCredentialKeyIsAtomicAndSupportsNewKeyOnlyRestore(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "regstair.db")
	oldFile := filepath.Join(dir, "old.key")
	newFile := filepath.Join(dir, "new.key")
	wrongFile := filepath.Join(dir, "wrong.key")
	oldKey, newKey := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	for path, key := range map[string][]byte{oldFile: oldKey, newFile: newKey, wrongFile: bytes.Repeat([]byte{3}, 32)} {
		if err := os.WriteFile(path, key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	oldRing, err := auth.NewSecretKeyring("old", map[string][]byte{"old": oldKey}, bytes.NewReader(bytes.Repeat([]byte{4}, 12)))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "ROTATION-UPSTREAM-SECRET"
	encrypted, err := oldRing.Encrypt("credential-1", user.ID, "harbor", []byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveRegistryCredential(context.Background(), metadata.RegistryCredential{ID: "credential-1", UserID: user.ID, SourceID: "harbor", Username: "robot", EncryptedSecret: encrypted}, metadata.AuditEvent{ActorUserID: user.ID, ActorRole: "user", Action: "credential.created", TargetType: "registry_credential", TargetID: "credential-1", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	args := []string{"-metadata-path", database, "-old-key-id", "old", "-old-key-file", oldFile, "-new-key-id", "new", "-new-key-file", newFile}
	if err := runAdminRotateCredentialKey(args); err != nil {
		t.Fatalf("runAdminRotateCredentialKey() error = %v", err)
	}
	repo, err = metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := repo.FindRegistryCredential(context.Background(), user.ID, "harbor")
	if err != nil {
		t.Fatal(err)
	}
	newOnly, err := auth.NewSecretKeyring("new", map[string][]byte{"new": newKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := newOnly.Decrypt(rotated.ID, rotated.UserID, rotated.SourceID, rotated.EncryptedSecret)
	if err != nil || string(decrypted) != plaintext || !bytes.Contains([]byte(rotated.EncryptedSecret), []byte(`"kid":"new"`)) {
		t.Fatalf("rotated credential did not restore with new key only: %q %v", decrypted, err)
	}
	events, err := repo.ListAuditEvents(context.Background(), 10)
	if err != nil || events[0].Action != "credential.key_rotated" || events[0].Details["credential_count"] != "1" {
		t.Fatalf("rotation audit = %#v, %v", events, err)
	}
	beforeFailedRotation := rotated.EncryptedSecret
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	wrongArgs := []string{"-metadata-path", database, "-old-key-id", "new", "-old-key-file", wrongFile, "-new-key-id", "newer", "-new-key-file", oldFile}
	if err := runAdminRotateCredentialKey(wrongArgs); err == nil {
		t.Fatal("rotation with wrong old key succeeded")
	}
	repo, err = metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	afterFailedRotation, _ := repo.FindRegistryCredential(context.Background(), user.ID, "harbor")
	if afterFailedRotation.EncryptedSecret != beforeFailedRotation {
		t.Fatal("failed rotation modified stored ciphertext")
	}
	missingArgs := []string{"-metadata-path", database, "-old-key-id", "new", "-old-key-file", filepath.Join(dir, "missing.key"), "-new-key-id", "newer", "-new-key-file", oldFile}
	if err := runAdminRotateCredentialKey(missingArgs); err == nil {
		t.Fatal("rotation with missing old key succeeded")
	}
}
