package auth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"regstair/internal/metadata"
)

func TestAccountServiceBootstrapAdminIsOneShotUnderConcurrency(t *testing.T) {
	repo := openAuthRepository(t)
	service := NewAccountService(repo, NewPasswordHasher(DefaultPasswordParams, nil))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, metadata.ErrConflict):
			conflicts++
		default:
			t.Fatalf("BootstrapAdmin() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("bootstrap results = %d success, %d conflict", successes, conflicts)
	}
}

func TestAccountServiceAuthenticatesEnabledWebUser(t *testing.T) {
	repo := openAuthRepository(t)
	service := NewAccountService(repo, NewPasswordHasher(DefaultPasswordParams, nil))
	user, err := service.BootstrapAdmin(context.Background(), "alice", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.AuthenticateWeb(context.Background(), "alice", "correct horse battery staple")
	if err != nil || got.ID != user.ID {
		t.Fatalf("AuthenticateWeb() = %#v, %v", got, err)
	}
	if _, err := service.AuthenticateWeb(context.Background(), "alice", "wrong password value"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong-password error = %v", err)
	}
	user.Enabled = false
	if _, err := repo.UpdateUser(context.Background(), *user, user.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateWeb(context.Background(), "alice", "correct horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled-user error = %v", err)
	}
}

func TestDockerTokenServiceLifecycle(t *testing.T) {
	repo := openAuthRepository(t)
	accounts := NewAccountService(repo, NewPasswordHasher(DefaultPasswordParams, nil))
	user, err := accounts.BootstrapAdmin(context.Background(), "alice", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewDockerTokenService(repo, func() time.Time { return now }, nil)
	issued, err := service.Issue(context.Background(), user.ID, "workstation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Secret == "" || issued.Token.TokenHash != nil {
		t.Fatalf("issued token leaks hash or omits one-time secret: %#v", issued)
	}
	identity, err := service.Authenticate(context.Background(), "alice", issued.Secret)
	if err != nil || identity.ID != user.ID {
		t.Fatalf("Authenticate() = %#v, %v", identity, err)
	}
	if err := service.Revoke(context.Background(), issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), "alice", issued.Secret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked-token error = %v", err)
	}
}

func TestWebSessionServiceLifecycleAndCSRF(t *testing.T) {
	repo := openAuthRepository(t)
	accounts := NewAccountService(repo, NewPasswordHasher(DefaultPasswordParams, nil))
	user, err := accounts.BootstrapAdmin(context.Background(), "alice", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewWebSessionService(repo, func() time.Time { return now }, nil, 30*time.Minute, 12*time.Hour)
	issued, err := service.Create(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Validate(context.Background(), issued.Secret, issued.CSRFToken)
	if err != nil || got.ID != user.ID {
		t.Fatalf("Validate() = %#v, %v", got, err)
	}
	if _, err := service.Validate(context.Background(), issued.Secret, "wrong"); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("wrong-CSRF error = %v", err)
	}
	now = now.Add(31 * time.Minute)
	if _, err := service.Validate(context.Background(), issued.Secret, issued.CSRFToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired-session error = %v", err)
	}
}

func TestAdminAccountMutationInvalidatesExistingIdentityImmediately(t *testing.T) {
	repo := openAuthRepository(t)
	hasher := NewPasswordHasher(DefaultPasswordParams, nil)
	accounts := NewAccountService(repo, hasher)
	admin, err := accounts.BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	adminService := NewAdminAccountService(repo, hasher)
	user, err := adminService.Create(context.Background(), admin.ID, NewUser{Username: "alice", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	tokens := NewDockerTokenService(repo, func() time.Time { return now }, nil)
	sessions := NewWebSessionService(repo, func() time.Time { return now }, nil, 30*time.Minute, 12*time.Hour)
	token, err := tokens.Issue(context.Background(), user.ID, "laptop", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	user.Enabled = false
	if _, err := adminService.Update(context.Background(), admin.ID, *user, user.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Authenticate(context.Background(), user.Username, token.Secret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("token after disable error = %v", err)
	}
	if _, err := sessions.Validate(context.Background(), session.Secret, session.CSRFToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("session after disable error = %v", err)
	}
}

func TestAdminAccountServiceRejectsNonAdminActor(t *testing.T) {
	repo := openAuthRepository(t)
	hasher := NewPasswordHasher(DefaultPasswordParams, nil)
	accounts := NewAccountService(repo, hasher)
	admin, err := accounts.BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	service := NewAdminAccountService(repo, hasher)
	user, err := service.Create(context.Background(), admin.ID, NewUser{Username: "alice", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), user.ID, NewUser{Username: "bob", Password: "third correct battery staple", Access: metadata.UserAccessUser, Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin create error = %v", err)
	}
}

func TestAccessChangeAndPasswordResetInvalidateExistingIdentity(t *testing.T) {
	repo := openAuthRepository(t)
	hasher := NewPasswordHasher(DefaultPasswordParams, nil)
	accounts := NewAccountService(repo, hasher)
	admin, err := accounts.BootstrapAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	service := NewAdminAccountService(repo, hasher)
	user, err := service.Create(context.Background(), admin.ID, NewUser{Username: "alice", Password: "another correct battery staple", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	tokens := NewDockerTokenService(repo, func() time.Time { return now }, nil)
	sessions := NewWebSessionService(repo, func() time.Time { return now }, nil, time.Hour, 12*time.Hour)

	assertInvalidated := func(mutate func() error) {
		t.Helper()
		token, err := tokens.Issue(context.Background(), user.ID, "test", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		session, err := sessions.Create(context.Background(), user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := mutate(); err != nil {
			t.Fatal(err)
		}
		if _, err := tokens.Authenticate(context.Background(), user.Username, token.Secret); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("token remains valid: %v", err)
		}
		if _, err := sessions.Validate(context.Background(), session.Secret, session.CSRFToken); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("session remains valid: %v", err)
		}
	}

	assertInvalidated(func() error {
		user.Access = metadata.UserAccessAdmin
		updated, err := service.Update(context.Background(), admin.ID, *user, user.UpdatedAt)
		if err == nil {
			user = updated
		}
		return err
	})
	assertInvalidated(func() error {
		updated, err := service.ResetPassword(context.Background(), admin.ID, user.ID, "replacement correct battery staple")
		if err == nil {
			user = updated
		}
		return err
	})
}

func openAuthRepository(t *testing.T) *metadata.SQLiteRepository {
	t.Helper()
	repo, err := metadata.NewSQLiteRepository(filepath.Join(t.TempDir(), "regstair.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}
