package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"regstair/internal/config"
	"regstair/internal/metadata"
	"regstair/internal/security"
)

type verifierFunc func(context.Context, VerificationRequest) error

func (f verifierFunc) Verify(ctx context.Context, request VerificationRequest) error {
	return f(ctx, request)
}

func TestRegistryCredentialVerificationFailuresAreClassifiedAndStoreNothing(t *testing.T) {
	cases := []struct {
		name  string
		cause error
		code  string
	}{
		{"credentials", fmtError(ErrUpstreamCredentials), VerificationInvalidCredentials},
		{"permission", fmtError(ErrUpstreamPermission), VerificationInsufficientPermission},
		{"unavailable", fmtError(ErrUpstreamUnavailable), VerificationRegistryUnavailable},
		{"configuration", fmtError(ErrVerificationConfig), VerificationConfigurationInvalid},
		{"other", errors.New("upstream response included SECRET-FIXTURE"), VerificationRegistryFailure},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo, service := credentialTestService(t, verifierFunc(func(context.Context, VerificationRequest) error { return tt.cause }))
			_, err := service.VerifyAndSave(context.Background(), "user-1", "harbor", "alice", []byte("SECRET-FIXTURE"))
			var public *security.PublicError
			if !errors.As(err, &public) || public.Code != tt.code {
				t.Fatalf("error = %#v, want code %q", err, tt.code)
			}
			if strings.Contains(err.Error(), "SECRET-FIXTURE") || strings.Contains(err.Error(), "upstream response") {
				t.Fatalf("public error leaked upstream detail: %v", err)
			}
			stored, findErr := repo.FindRegistryCredential(context.Background(), "user-1", "harbor")
			if findErr != nil || stored != nil {
				t.Fatalf("stored after failure = %#v, %v", stored, findErr)
			}
		})
	}
}

func TestRegistryCredentialSuccessfulReplacementIsEncryptedAndAtomic(t *testing.T) {
	seen := 0
	repo, service := credentialTestService(t, verifierFunc(func(_ context.Context, request VerificationRequest) error {
		seen++
		if request.Repository != "regstair/check" || !request.Pull || !request.Push || string(request.Secret) == "" || len(request.TokenHosts) != 1 || request.TokenHosts[0] != "auth.harbor.example" {
			t.Fatalf("verification request = %#v", request)
		}
		return nil
	}))
	first, err := service.VerifyAndSave(context.Background(), "user-1", "harbor", "alice", []byte("FIRST-SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.VerifyAndSave(context.Background(), "user-1", "harbor", "alice-robot", []byte("SECOND-SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 || first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) || !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("replacement first=%#v second=%#v seen=%d", first, second, seen)
	}
	stored, err := repo.FindRegistryCredential(context.Background(), "user-1", "harbor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.EncryptedSecret, "FIRST-SECRET") || strings.Contains(stored.EncryptedSecret, "SECOND-SECRET") || stored.Username != "alice-robot" {
		t.Fatalf("stored credential = %#v", stored)
	}
	events, _ := repo.ListAuditEvents(context.Background(), 10)
	if len(events) != 2 || events[0].Action != "credential.replaced" || events[1].Action != "credential.created" {
		t.Fatalf("audit events = %#v", events)
	}
	views, err := service.List(context.Background(), "user-1")
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %#v, %v", views, err)
	}
}

func TestRegistryCredentialListAndRemovalAreUserScoped(t *testing.T) {
	repo, service := credentialTestService(t, verifierFunc(func(context.Context, VerificationRequest) error { return nil }))
	if _, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-2", Username: "bob", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyAndSave(context.Background(), "user-1", "harbor", "alice-upstream", []byte("SECRET")); err != nil {
		t.Fatal(err)
	}
	otherViews, err := service.List(context.Background(), "user-2")
	if err != nil || len(otherViews) != 0 {
		t.Fatalf("other user views = %#v, %v", otherViews, err)
	}
	if err := service.Remove(context.Background(), "user-2", "harbor", true); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.FindRegistryCredential(context.Background(), "user-1", "harbor")
	if stored == nil {
		t.Fatal("other user removed credential")
	}
	if err := service.Remove(context.Background(), "user-1", "harbor", false); err == nil {
		t.Fatal("unconfirmed removal succeeded")
	}
	if err := service.Remove(context.Background(), "user-1", "harbor", true); err != nil {
		t.Fatal(err)
	}
	stored, _ = repo.FindRegistryCredential(context.Background(), "user-1", "harbor")
	if stored != nil {
		t.Fatal("confirmed removal retained credential")
	}
	events, _ := repo.ListAuditEvents(context.Background(), 10)
	if len(events) != 2 || events[0].Action != "credential.deleted" {
		t.Fatalf("audit events = %#v", events)
	}
}

func credentialTestService(t *testing.T, verifier CredentialVerifier) (*metadata.SQLiteRepository, *RegistryCredentialService) {
	t.Helper()
	repo := openAuthRepository(t)
	if _, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	keyring, err := NewSecretKeyring("test", map[string][]byte{"test": bytes.Repeat([]byte{4}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sources := []config.Source{{ID: "harbor", Endpoint: "https://harbor.example", Enabled: true, Auth: config.Auth{TokenHosts: []string{"auth.harbor.example"}}, UserCredentials: config.UserCredentials{Pull: true, Push: true, VerificationRepository: "regstair/check"}}}
	return repo, NewRegistryCredentialService(repo, keyring, verifier, sources)
}

func fmtError(base error) error { return errors.Join(base, errors.New("private upstream detail")) }
