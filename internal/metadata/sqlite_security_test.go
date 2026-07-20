package metadata

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLiteSecurityRepositoryPersistsTokensSessionsAndAudit(t *testing.T) {
	repo := openSecurityRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }
	createSecurityTestUser(t, repo)

	tokenHash := bytes.Repeat([]byte{1}, 32)
	token, err := repo.CreateDockerToken(ctx, DockerToken{ID: "token-1", UserID: "user-1", Label: "laptop", TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreateDockerToken() error = %v", err)
	}
	if !token.CreatedAt.Equal(now) {
		t.Fatalf("token ctime = %v", token.CreatedAt)
	}
	foundToken, err := repo.FindDockerTokenByHash(ctx, tokenHash)
	if err != nil || foundToken == nil || foundToken.ID != "token-1" {
		t.Fatalf("FindDockerTokenByHash() = %#v, %v", foundToken, err)
	}
	if err := repo.RevokeDockerToken(ctx, token.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("RevokeDockerToken() error = %v", err)
	}
	foundToken, _ = repo.FindDockerTokenByHash(ctx, tokenHash)
	if foundToken.RevokedAt.IsZero() {
		t.Fatal("revoked token has zero revoked time")
	}

	sessionHash := bytes.Repeat([]byte{2}, 32)
	session, err := repo.CreateWebSession(ctx, WebSession{ID: "session-1", UserID: "user-1", TokenHash: sessionHash, CSRFTokenHash: bytes.Repeat([]byte{3}, 32), IdleExpiresAt: now.Add(30 * time.Minute), AbsoluteExpiresAt: now.Add(12 * time.Hour)})
	if err != nil {
		t.Fatalf("CreateWebSession() error = %v", err)
	}
	foundSession, err := repo.FindWebSessionByHash(ctx, sessionHash)
	if err != nil || foundSession == nil || foundSession.ID != session.ID {
		t.Fatalf("FindWebSessionByHash() = %#v, %v", foundSession, err)
	}
	if err := repo.RevokeWebSessionsForUser(ctx, "user-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("RevokeWebSessionsForUser() error = %v", err)
	}
	foundSession, _ = repo.FindWebSessionByHash(ctx, sessionHash)
	if foundSession.RevokedAt.IsZero() {
		t.Fatal("revoked session has zero revoked time")
	}

	event, err := repo.RecordAuditEvent(ctx, securityAudit("login.failed", "user", "user-1"))
	if err != nil || event.ID == 0 {
		t.Fatalf("RecordAuditEvent() = %#v, %v", event, err)
	}
	events, err := repo.ListAuditEvents(ctx, 10)
	if err != nil || len(events) != 1 || events[0].Action != "login.failed" {
		t.Fatalf("ListAuditEvents() = %#v, %v", events, err)
	}
}

func TestSQLiteRegistryCredentialSaveReplaceDeleteIsAuditedAndAtomic(t *testing.T) {
	repo := openSecurityRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }
	createSecurityTestUser(t, repo)
	credential := RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice", EncryptedSecret: `{"ciphertext":"opaque-one"}`}
	created, err := repo.SaveRegistryCredential(ctx, credential, securityAudit("credential.created", "registry_credential", credential.ID))
	if err != nil {
		t.Fatalf("SaveRegistryCredential() error = %v", err)
	}
	if !created.CreatedAt.Equal(now) || !created.UpdatedAt.Equal(now) {
		t.Fatalf("created timestamps = %v/%v", created.CreatedAt, created.UpdatedAt)
	}

	now = now.Add(time.Minute)
	credential.EncryptedSecret = `{"ciphertext":"opaque-two"}`
	replaced, err := repo.SaveRegistryCredential(ctx, credential, securityAudit("credential.replaced", "registry_credential", credential.ID))
	if err != nil {
		t.Fatalf("replace SaveRegistryCredential() error = %v", err)
	}
	if !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.Equal(now) {
		t.Fatalf("replaced timestamps = %v/%v", replaced.CreatedAt, replaced.UpdatedAt)
	}

	if _, err := repo.SaveRegistryCredential(ctx, RegistryCredential{ID: "other-id", UserID: "user-1", SourceID: "harbor", Username: "alice", EncryptedSecret: "opaque"}, securityAudit("credential.replaced", "registry_credential", "other-id")); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched replacement error = %v, want ErrConflict", err)
	}
	events, _ := repo.ListAuditEvents(ctx, 10)
	if len(events) != 2 {
		t.Fatalf("audit count after rolled-back replacement = %d, want 2", len(events))
	}

	if err := repo.DeleteRegistryCredential(ctx, "user-1", "harbor", securityAudit("credential.deleted", "registry_credential", credential.ID)); err != nil {
		t.Fatalf("DeleteRegistryCredential() error = %v", err)
	}
	found, err := repo.FindRegistryCredential(ctx, "user-1", "harbor")
	if err != nil || found != nil {
		t.Fatalf("FindRegistryCredential(after delete) = %#v, %v", found, err)
	}
	events, _ = repo.ListAuditEvents(ctx, 10)
	if len(events) != 3 || events[0].Action != "credential.deleted" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestSQLiteRegistryCredentialConcurrentReplacementKeepsOneRecord(t *testing.T) {
	repo := openSecurityRepository(t)
	ctx := context.Background()
	createSecurityTestUser(t, repo)
	base := RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice", EncryptedSecret: "initial"}
	if _, err := repo.SaveRegistryCredential(ctx, base, securityAudit("credential.created", "registry_credential", base.ID)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errorsSeen := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := base
			candidate.EncryptedSecret = "replacement"
			_, err := repo.SaveRegistryCredential(ctx, candidate, securityAudit("credential.replaced", "registry_credential", base.ID))
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent replacement error = %v", err)
		}
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM registry_credentials WHERE user_id = ? AND source_id = ?`, "user-1", "harbor").Scan(&count); err != nil || count != 1 {
		t.Fatalf("credential count = %d, %v", count, err)
	}
}

func TestSQLiteCredentialMutationRollsBackWhenAuditIsUnsafe(t *testing.T) {
	repo := openSecurityRepository(t)
	createSecurityTestUser(t, repo)
	credential := RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice", EncryptedSecret: "opaque"}
	audit := securityAudit("credential.created", "registry_credential", credential.ID)
	audit.Details = map[string]string{"password": "must-not-persist"}
	if _, err := repo.SaveRegistryCredential(context.Background(), credential, audit); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("SaveRegistryCredential() error = %v, want ErrInvalidRecord", err)
	}
	found, err := repo.FindRegistryCredential(context.Background(), "user-1", "harbor")
	if err != nil || found != nil {
		t.Fatalf("credential after rejected audit = %#v, %v", found, err)
	}
}

func TestSQLiteRegistryCredentialConcurrentSaveAndDeleteRemainAtomic(t *testing.T) {
	repo := openSecurityRepository(t)
	createSecurityTestUser(t, repo)
	credential := RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice", EncryptedSecret: "opaque"}
	if _, err := repo.SaveRegistryCredential(context.Background(), credential, securityAudit("credential.created", "registry_credential", credential.ID)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errorsSeen := make(chan error, 10)
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := repo.SaveRegistryCredential(context.Background(), credential, securityAudit("credential.replaced", "registry_credential", credential.ID))
			errorsSeen <- err
		}()
		go func() {
			defer wg.Done()
			errorsSeen <- repo.DeleteRegistryCredential(context.Background(), "user-1", "harbor", securityAudit("credential.deleted", "registry_credential", credential.ID))
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent mutation error = %v", err)
		}
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM registry_credentials WHERE user_id = ? AND source_id = ?`, "user-1", "harbor").Scan(&count); err != nil || count > 1 {
		t.Fatalf("credential count = %d, %v", count, err)
	}
	var auditCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&auditCount); err != nil || auditCount != 11 {
		t.Fatalf("audit count = %d, %v, want 11 committed mutations", auditCount, err)
	}
}

func TestSQLiteRecordsSchemaMigrationVersion(t *testing.T) {
	repo := openSecurityRepository(t)
	var version int
	if err := repo.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("schema version = %d, %v, want 1", version, err)
	}
}

func TestSQLiteSecurityTablesDoNotContainPlaintextSecret(t *testing.T) {
	repo := openSecurityRepository(t)
	createSecurityTestUser(t, repo)
	secret := "DO-NOT-PERSIST-PLAINTEXT"
	credential := RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice", EncryptedSecret: `{"ciphertext":"safe-value"}`}
	if _, err := repo.SaveRegistryCredential(context.Background(), credential, securityAudit("credential.created", "registry_credential", credential.ID)); err != nil {
		t.Fatal(err)
	}
	var dump string
	if err := repo.db.QueryRow(`SELECT group_concat(sql, ' ') FROM sqlite_master`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, secret) || strings.Contains(credential.EncryptedSecret, secret) {
		t.Fatal("plaintext secret found in persisted data")
	}
}

func TestSQLiteAuditRejectsSensitiveDetails(t *testing.T) {
	repo := openSecurityRepository(t)
	event := securityAudit("credential.failed", "registry_credential", "credential-1")
	event.Details = map[string]string{"password": "DO-NOT-STORE"}
	if _, err := repo.RecordAuditEvent(context.Background(), event); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("RecordAuditEvent(sensitive details) error = %v", err)
	}
	events, err := repo.ListAuditEvents(context.Background(), 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("audit events = %#v, %v", events, err)
	}
}

func TestSQLiteUserSecurityUpdateAtomicallyRevokesSessionsAndTokens(t *testing.T) {
	repo := openSecurityRepository(t)
	ctx := context.Background()
	createSecurityTestUser(t, repo)
	if _, err := repo.CreateUser(ctx, User{ID: "admin-1", Username: "admin", PasswordHash: "hash", Access: UserAccessAdmin, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	tokenHash := bytes.Repeat([]byte{7}, 32)
	if _, err := repo.CreateDockerToken(ctx, DockerToken{ID: "token-1", UserID: "user-1", TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	sessionHash := bytes.Repeat([]byte{8}, 32)
	if _, err := repo.CreateWebSession(ctx, WebSession{ID: "session-1", UserID: "user-1", TokenHash: sessionHash, CSRFTokenHash: bytes.Repeat([]byte{9}, 32), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(12 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	user, _ := repo.FindUserByID(ctx, "user-1")
	user.Enabled = false
	audit := securityAudit("user.disabled", "user", user.ID)
	audit.ActorUserID, audit.ActorRole = "admin-1", "admin"
	updated, err := repo.UpdateUserSecurity(ctx, *user, user.UpdatedAt, true, audit)
	if err != nil || updated.Enabled {
		t.Fatalf("UpdateUserSecurity() = %#v, %v", updated, err)
	}
	token, _ := repo.FindDockerTokenByHash(ctx, tokenHash)
	session, _ := repo.FindWebSessionByHash(ctx, sessionHash)
	if token.RevokedAt.IsZero() || session.RevokedAt.IsZero() {
		t.Fatalf("revocations token=%v session=%v", token.RevokedAt, session.RevokedAt)
	}
	events, _ := repo.ListAuditEvents(ctx, 10)
	if len(events) != 1 || events[0].Action != "user.disabled" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestSQLiteUserSecurityUpdateProtectsLastEnabledAdmin(t *testing.T) {
	repo := openSecurityRepository(t)
	ctx := context.Background()
	admin := User{ID: "admin-1", Username: "admin", PasswordHash: "hash", Access: UserAccessAdmin, Enabled: true}
	if _, err := repo.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	adminPtr, _ := repo.FindUserByID(ctx, admin.ID)
	adminPtr.Access = UserAccessUser
	if _, err := repo.UpdateUserSecurity(ctx, *adminPtr, adminPtr.UpdatedAt, true, securityAudit("user.access_changed", "user", admin.ID)); !errors.Is(err, ErrConflict) {
		t.Fatalf("last-admin mutation error = %v, want ErrConflict", err)
	}
}

func openSecurityRepository(t *testing.T) *SQLiteRepository {
	t.Helper()
	repo := openSQLiteRepository(t, filepath.Join(t.TempDir(), "regstair.db"))
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func createSecurityTestUser(t *testing.T, repo *SQLiteRepository) {
	t.Helper()
	if _, err := repo.CreateUser(context.Background(), User{ID: "user-1", Username: "alice", PasswordHash: "argon2id-fixture", Access: UserAccessUser, Enabled: true}); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
}

func securityAudit(action, targetType, targetID string) AuditEvent {
	return AuditEvent{ActorUserID: "user-1", ActorRole: "user", Action: action, TargetType: targetType, TargetID: targetID, Outcome: "success", Details: map[string]string{"source_id": "harbor"}}
}
