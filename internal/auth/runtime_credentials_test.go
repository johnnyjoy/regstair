package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"regstair/internal/config"
	"regstair/internal/identity"
	"regstair/internal/metadata"
	"regstair/internal/registry"
)

func TestRuntimeCredentialSelectorUsesExactLocalUserCredential(t *testing.T) {
	repo := openAuthRepository(t)
	ctx := context.Background()
	for _, user := range []metadata.User{
		{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true},
		{ID: "user-2", Username: "bob", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true},
	} {
		if _, err := repo.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	keyring, err := NewSecretKeyring("test", map[string][]byte{"test": bytes.Repeat([]byte{7}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := keyring.Encrypt("credential-1", "user-1", "harbor", []byte("alice-secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.SaveRegistryCredential(ctx, metadata.RegistryCredential{ID: "credential-1", UserID: "user-1", SourceID: "harbor", Username: "alice-upstream", EncryptedSecret: encrypted}, metadata.AuditEvent{ActorUserID: "user-1", ActorRole: "user", Action: "credential.created", TargetType: "registry_credential", TargetID: "credential-1", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	source := config.Source{ID: "harbor", Endpoint: "https://harbor.example", Enabled: true, Auth: config.Auth{Strategy: config.AuthStrategyCurrentUserRequired}, UserCredentials: config.UserCredentials{Pull: true, Push: true}}
	selector := NewRuntimeCredentialSelector(repo, keyring, []config.Source{source}, nil, nil)
	var gotUsername, gotPassword string
	selector.factory = func(_ config.Source, username, password string) (registry.Connector, error) {
		gotUsername, gotPassword = username, password
		return registry.NewFakeConnector("harbor"), nil
	}

	_, credentialSource, err := selector.ConnectorFor(ctx, identity.Principal{Kind: identity.KindLocalUser, ID: "user-1"}, "harbor", metadata.OperationPush)
	if err != nil {
		t.Fatalf("ConnectorFor() error = %v", err)
	}
	if gotUsername != "alice-upstream" || gotPassword != "alice-secret" || credentialSource != "current_user" {
		t.Fatalf("selection = %q/%q source=%q", gotUsername, gotPassword, credentialSource)
	}

	_, _, err = selector.ConnectorFor(ctx, identity.Principal{Kind: identity.KindLocalUser, ID: "user-2"}, "harbor", metadata.OperationPush)
	if !errors.Is(err, registry.ErrCredentialRequired) {
		t.Fatalf("second user error = %v, want credential required", err)
	}
}

func TestRuntimeCredentialSelectorDoesNotInspectCredentialsForAnonymousSource(t *testing.T) {
	base := registry.NewFakeConnector("public")
	source := config.Source{ID: "public", Endpoint: "https://public.example", Enabled: true}
	selector := NewRuntimeCredentialSelector(nil, nil, []config.Source{source}, map[string]registry.Connector{"public": base}, nil)
	got, credentialSource, err := selector.ConnectorFor(context.Background(), identity.Anonymous(), "public", metadata.OperationPull)
	if err != nil || got != base || credentialSource != "anonymous" {
		t.Fatalf("ConnectorFor(public) = %#v, %q, %v", got, credentialSource, err)
	}
}

func TestRuntimeCredentialSelectorKeepsPublicPullAnonymousWithoutCredential(t *testing.T) {
	repo := openAuthRepository(t)
	if _, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	base := registry.NewFakeConnector("docker-hub")
	source := config.Source{ID: "docker-hub", Endpoint: "https://registry-1.docker.io", Enabled: true, UserCredentials: config.UserCredentials{Pull: true}}
	selector := NewRuntimeCredentialSelector(repo, nil, []config.Source{source}, map[string]registry.Connector{"docker-hub": base}, nil)

	for _, principal := range []identity.Principal{identity.Anonymous(), {Kind: identity.KindLocalUser, ID: "user-1"}} {
		connector, credentialSource, err := selector.ConnectorFor(context.Background(), principal, "docker-hub", metadata.OperationPull)
		if err != nil || connector != base || credentialSource != "anonymous" {
			t.Fatalf("ConnectorFor(%#v) = %#v, %q, %v", principal, connector, credentialSource, err)
		}
	}
}

func TestRuntimeCredentialSelectorChallengeAllowsAuthenticatedPushAttemptWithoutUpstreamCredential(t *testing.T) {
	repo := openAuthRepository(t)
	if _, err := repo.CreateUser(context.Background(), metadata.User{ID: "user-1", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	base := registry.NewFakeConnector("fixture")
	source := config.Source{ID: "fixture", Endpoint: "http://fixture:5000", Enabled: true, Auth: config.Auth{Strategy: config.AuthStrategyChallenge}}
	selector := NewRuntimeCredentialSelector(repo, nil, []config.Source{source}, map[string]registry.Connector{"fixture": base}, nil)

	connector, credentialSource, err := selector.ConnectorFor(context.Background(), identity.Principal{Kind: identity.KindLocalUser, ID: "user-1"}, "fixture", metadata.OperationPush)
	if err != nil || connector != base || credentialSource != "anonymous" {
		t.Fatalf("ConnectorFor(challenge push) = %#v, %q, %v", connector, credentialSource, err)
	}
	if _, _, err := selector.ConnectorFor(context.Background(), identity.Anonymous(), "fixture", metadata.OperationPush); err == nil {
		t.Fatal("anonymous local client was allowed to push")
	}
}

func TestRuntimeCredentialSelectorEnforcesClosedStrategies(t *testing.T) {
	repo := openAuthRepository(t)
	user, err := repo.CreateUser(context.Background(), metadata.User{ID: "alice", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	base := registry.NewFakeConnector("source")
	for _, tt := range []struct {
		strategy  string
		principal identity.Principal
		source    string
		wantErr   bool
	}{
		{config.AuthStrategyChallenge, identity.Anonymous(), "anonymous", false},
		{config.AuthStrategyProxy, identity.Anonymous(), "", true},
		{config.AuthStrategyProxy, identity.Principal{Kind: identity.KindLocalUser, ID: user.ID}, "proxy", false},
		{config.AuthStrategyCurrentUserRequired, identity.Anonymous(), "", true},
		{config.AuthStrategyCurrentUserRequired, identity.Principal{Kind: identity.KindLocalUser, ID: user.ID}, "", true},
	} {
		source := config.Source{ID: "source", Endpoint: "https://registry.example", Enabled: true, Auth: config.Auth{Strategy: tt.strategy}}
		selector := NewRuntimeCredentialSelector(repo, nil, []config.Source{source}, map[string]registry.Connector{"source": base}, nil)
		_, gotSource, err := selector.ConnectorFor(context.Background(), tt.principal, "source", metadata.OperationPull)
		if (err != nil) != tt.wantErr || gotSource != tt.source {
			t.Fatalf("strategy %s = source %q error %v", tt.strategy, gotSource, err)
		}
	}
}

func TestRuntimeCredentialSelectorCacheGrantTracksUserAndCredentialExistence(t *testing.T) {
	repo := openAuthRepository(t)
	ctx := context.Background()
	users := []metadata.User{{ID: "alice", Username: "alice", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true}, {ID: "bob", Username: "bob", PasswordHash: "hash", Access: metadata.UserAccessUser, Enabled: true}}
	for i := range users {
		created, err := repo.CreateUser(ctx, users[i])
		if err != nil {
			t.Fatal(err)
		}
		users[i] = *created
	}
	keyring, err := NewSecretKeyring("test", map[string][]byte{"test": bytes.Repeat([]byte{8}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	save := func(secret string) {
		encrypted, err := keyring.Encrypt("credential", "alice", "harbor", []byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.SaveRegistryCredential(ctx, metadata.RegistryCredential{ID: "credential", UserID: "alice", SourceID: "harbor", Username: "alice-upstream", EncryptedSecret: encrypted}, metadata.AuditEvent{ActorUserID: "alice", ActorRole: "user", Action: "credential.saved", TargetType: "registry_credential", TargetID: "credential", Outcome: "success"})
		if err != nil {
			t.Fatal(err)
		}
	}
	save("first")
	selector := NewRuntimeCredentialSelector(repo, keyring, []config.Source{{ID: "harbor", Endpoint: "https://harbor.example", Enabled: true}}, nil, nil)
	binding := metadata.CacheBinding{Source: "harbor", Access: metadata.CacheAccessCurrentUserRequired, UserID: "alice"}

	if source, err := selector.AuthorizeCache(ctx, identity.Principal{Kind: identity.KindLocalUser, ID: "alice"}, binding, metadata.OperationPull); err != nil || source != "current_user" {
		t.Fatalf("alice authorization = %q, %v", source, err)
	}
	for _, principal := range []identity.Principal{identity.Anonymous(), {Kind: identity.KindLocalUser, ID: "bob"}} {
		if _, err := selector.AuthorizeCache(ctx, principal, binding, metadata.OperationPull); err == nil {
			t.Fatalf("principal %#v used alice cache grant", principal)
		}
	}

	save("replacement")
	if _, err := selector.AuthorizeCache(ctx, identity.Principal{Kind: identity.KindLocalUser, ID: "alice"}, binding, metadata.OperationPull); err != nil {
		t.Fatalf("replacement invalidated same-user grant: %v", err)
	}
	if err := repo.DeleteRegistryCredential(ctx, "alice", "harbor", metadata.AuditEvent{ActorUserID: "alice", ActorRole: "user", Action: "credential.deleted", TargetType: "registry_credential", TargetID: "credential", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if _, err := selector.AuthorizeCache(ctx, identity.Principal{Kind: identity.KindLocalUser, ID: "alice"}, binding, metadata.OperationPull); !errors.Is(err, registry.ErrCredentialRequired) {
		t.Fatalf("removed credential error = %v", err)
	}

	save("third")
	users[0].Enabled = false
	if _, err := repo.UpdateUser(ctx, users[0], users[0].UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := selector.AuthorizeCache(ctx, identity.Principal{Kind: identity.KindLocalUser, ID: "alice"}, binding, metadata.OperationPull); !errors.Is(err, registry.ErrAuthorization) {
		t.Fatalf("disabled user error = %v", err)
	}
}
