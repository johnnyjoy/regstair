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
	source := config.Source{ID: "harbor", Endpoint: "https://harbor.example", Enabled: true, Auth: config.Auth{Mode: config.AuthModeCurrentUser}, UserCredentials: config.UserCredentials{Approved: true, Pull: true, Push: true}}
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
	source := config.Source{ID: "public", Endpoint: "https://public.example", Enabled: true, Auth: config.Auth{Mode: config.AuthModeNone}}
	selector := NewRuntimeCredentialSelector(nil, nil, []config.Source{source}, map[string]registry.Connector{"public": base}, nil)
	got, credentialSource, err := selector.ConnectorFor(context.Background(), identity.Anonymous(), "public", metadata.OperationPull)
	if err != nil || got != base || credentialSource != "anonymous" {
		t.Fatalf("ConnectorFor(public) = %#v, %q, %v", got, credentialSource, err)
	}
}
