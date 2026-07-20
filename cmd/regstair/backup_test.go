package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"regstair/internal/auth"
	"regstair/internal/metadata"
)

func TestAdminBackupRestoreIncludesConfigSQLiteAndContentButExcludesKeys(t *testing.T) {
	dir := t.TempDir()
	contentRoot := filepath.Join(dir, "source-content")
	configPath := filepath.Join(dir, "regstair.yaml")
	keyPath := filepath.Join(dir, "credential.key")
	archive := filepath.Join(dir, "backup.tar.gz")
	for path, contents := range map[string][]byte{
		filepath.Join(contentRoot, "metadata", "regstair.db"):     []byte("SQLITE-METADATA"),
		filepath.Join(contentRoot, "metadata", "regstair.db-wal"): []byte("SQLITE-WAL"),
		filepath.Join(contentRoot, "blobs", "sha256", "abc"):      []byte("OCI-CONTENT"),
		configPath: []byte("version: 1\n"),
		keyPath:    []byte("CREDENTIAL-KEY-CANARY"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runAdminBackup([]string{"-content-root", contentRoot, "-config", configPath, "-output", archive}); err != nil {
		t.Fatalf("runAdminBackup() error = %v", err)
	}
	archiveBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(archiveBytes, []byte("CREDENTIAL-KEY-CANARY")) {
		t.Fatal("backup archive included credential encryption key")
	}

	restoredContent := filepath.Join(dir, "restored-content")
	restoredConfig := filepath.Join(dir, "restored", "regstair.yaml")
	if err := runAdminRestore([]string{"-archive", archive, "-content-root", restoredContent, "-config-output", restoredConfig}); err != nil {
		t.Fatalf("runAdminRestore() error = %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(restoredContent, "metadata", "regstair.db"):     "SQLITE-METADATA",
		filepath.Join(restoredContent, "metadata", "regstair.db-wal"): "SQLITE-WAL",
		filepath.Join(restoredContent, "blobs", "sha256", "abc"):      "OCI-CONTENT",
		restoredConfig: "version: 1\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restored %s = %q, %v", path, got, err)
		}
	}
	if err := runAdminRestore([]string{"-archive", archive, "-content-root", restoredContent, "-config-output", filepath.Join(dir, "second.yaml")}); err == nil {
		t.Fatal("restore accepted a non-empty content root")
	}
	if err := runAdminBackup([]string{"-content-root", contentRoot, "-config", configPath, "-output", archive}); err == nil {
		t.Fatal("backup overwrote an existing archive")
	}
}

func TestRestoredCredentialRequiresOriginalEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	contentRoot := filepath.Join(dir, "source-content")
	database := filepath.Join(contentRoot, "metadata", "regstair.db")
	repo, err := metadata.NewSQLiteRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	originalKey := bytes.Repeat([]byte{7}, 32)
	ring, err := auth.NewSecretKeyring("original", map[string][]byte{"original": originalKey}, bytes.NewReader(bytes.Repeat([]byte{8}, 12)))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "RESTORE-CREDENTIAL-CANARY"
	encrypted, err := ring.Encrypt("credential-1", user.ID, "harbor", []byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveRegistryCredential(context.Background(), metadata.RegistryCredential{ID: "credential-1", UserID: user.ID, SourceID: "harbor", Username: "robot", EncryptedSecret: encrypted}, metadata.AuditEvent{ActorUserID: user.ID, ActorRole: "user", Action: "credential.created", TargetType: "registry_credential", TargetID: "credential-1", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "regstair.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "backup.tar.gz")
	if err := runAdminBackup([]string{"-content-root", contentRoot, "-config", configPath, "-output", archive}); err != nil {
		t.Fatal(err)
	}
	restoredRoot := filepath.Join(dir, "restored-content")
	if err := runAdminRestore([]string{"-archive", archive, "-content-root", restoredRoot, "-config-output", filepath.Join(dir, "restored.yaml")}); err != nil {
		t.Fatal(err)
	}
	restoredRepo, err := metadata.NewSQLiteRepository(filepath.Join(restoredRoot, "metadata", "regstair.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredRepo.Close()
	credential, err := restoredRepo.FindRegistryCredential(context.Background(), user.ID, "harbor")
	if err != nil || credential == nil {
		t.Fatalf("restored credential = %#v, %v", credential, err)
	}
	got, err := ring.Decrypt(credential.ID, credential.UserID, credential.SourceID, credential.EncryptedSecret)
	if err != nil || string(got) != plaintext {
		t.Fatalf("correct-key restore = %q, %v", got, err)
	}
	for name, keys := range map[string]map[string][]byte{
		"wrong key bytes under original id": {"original": bytes.Repeat([]byte{9}, 32)},
		"missing historical key id":         {"other": bytes.Repeat([]byte{9}, 32)},
	} {
		active := "original"
		if name == "missing historical key id" {
			active = "other"
		}
		unusable, err := auth.NewSecretKeyring(active, keys, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := unusable.Decrypt(credential.ID, credential.UserID, credential.SourceID, credential.EncryptedSecret); !errors.Is(err, auth.ErrSecretUnavailable) {
			t.Fatalf("%s decrypt error = %v", name, err)
		}
	}
}
