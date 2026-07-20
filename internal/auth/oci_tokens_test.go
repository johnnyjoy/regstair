package auth

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"regstair/internal/identity"
	"regstair/internal/metadata"
)

func TestOCITokensEnforceScopeAndImmediateRevocation(t *testing.T) {
	repo, err := metadata.NewSQLiteRepository(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	accounts := NewAccountService(repo, NewPasswordHasher(DefaultPasswordParams, nil))
	user, err := accounts.BootstrapAdmin(context.Background(), "alice", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	docker := NewDockerTokenService(repo, nil, bytes.NewReader(bytes.Repeat([]byte{4}, 256)))
	issued, err := docker.Issue(context.Background(), user.ID, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewOCITokenService(docker, bytes.Repeat([]byte{9}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	access, _, err := service.Issue(context.Background(), "alice", issued.Secret, "team/service", []string{"pull"})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(context.Background(), access, "team/service", "pull")
	if err != nil || principal.Kind != identity.KindLocalUser || principal.ID != user.ID {
		t.Fatalf("authenticated principal = %#v, %v", principal, err)
	}
	if _, err := service.Authenticate(context.Background(), access, "team/service", "push"); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("pull token push error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), access, "other/service", "pull"); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("cross-repository token error = %v", err)
	}
	if err := docker.Revoke(context.Background(), issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), access, "team/service", "pull"); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("revoked token error = %v", err)
	}
}

func TestOCIAnonymousTokenAllowsPullOnly(t *testing.T) {
	repo, err := metadata.NewSQLiteRepository(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	service, err := NewOCITokenService(NewDockerTokenService(repo, nil, nil), bytes.Repeat([]byte{3}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := service.Issue(context.Background(), "", "", "library/alpine", []string{"pull"})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(context.Background(), token, "library/alpine", "pull")
	if err != nil || principal.Kind != identity.KindAnonymous {
		t.Fatalf("anonymous principal = %#v, %v", principal, err)
	}
	if _, _, err := service.Issue(context.Background(), "", "", "library/alpine", []string{"push"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("anonymous push issue error = %v", err)
	}
}
