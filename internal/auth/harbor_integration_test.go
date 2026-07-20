package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"

	"regstair/internal/config"
	"regstair/internal/identity"
	"regstair/internal/metadata"
	"regstair/internal/registry"
)

func TestHarborCredentialVerifierIntegration(t *testing.T) {
	if os.Getenv("REGSTAIR_HARBOR_VERIFY_TEST") != "1" {
		t.Skip("set REGSTAIR_HARBOR_VERIFY_TEST=1 to run Harbor verification")
	}
	endpoint, username, secret := os.Getenv("REGSTAIR_HARBOR_ENDPOINT"), os.Getenv("REGSTAIR_HARBOR_USERNAME"), os.Getenv("REGSTAIR_HARBOR_SECRET")
	if endpoint == "" || username == "" || secret == "" {
		t.Fatal("Harbor integration environment is incomplete")
	}
	verifier := NewHarborCredentialVerifier(nil)
	request := VerificationRequest{SourceID: "harbor", Endpoint: endpoint, Repository: "regstair/credential-check", Username: username, Secret: []byte(secret), Pull: true, Push: true}
	if err := verifier.Verify(context.Background(), request); err != nil {
		t.Fatalf("valid Harbor verification error = %v", err)
	}
	repo := openAuthRepository(t)
	accounts := NewAccountService(repo, NewPasswordHasher(DefaultPasswordParams, nil))
	user, err := accounts.BootstrapAdmin(context.Background(), "integration-admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := NewSecretKeyring("integration", map[string][]byte{"integration": bytes.Repeat([]byte{6}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := config.Source{ID: "harbor", Endpoint: endpoint, Enabled: true, Auth: config.Auth{Mode: config.AuthModeCurrentUser, Strategy: config.AuthStrategyChallenge}, UserCredentials: config.UserCredentials{Approved: true, Pull: true, Push: true, VerificationRepository: "regstair/credential-check", AllowInsecure: true}}
	service := NewRegistryCredentialService(repo, keyring, verifier, []config.Source{source})
	view, err := service.VerifyAndSave(context.Background(), user.ID, "harbor", username, []byte(secret))
	if err != nil {
		t.Fatalf("real Harbor VerifyAndSave error = %v", err)
	}
	if view.SourceID != "harbor" || view.Username != username {
		t.Fatalf("saved view = %#v", view)
	}
	selector := NewRuntimeCredentialSelector(repo, keyring, []config.Source{source}, nil, nil)
	connector, credentialSource, err := selector.ConnectorFor(context.Background(), identity.Principal{Kind: identity.KindLocalUser, ID: user.ID, Username: user.Username}, "harbor", metadata.OperationPush)
	if err != nil {
		t.Fatalf("runtime credential selection error = %v", err)
	}
	if credentialSource != "current_user" {
		t.Fatalf("credential source = %q", credentialSource)
	}
	blob := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	blobSum := sha256.Sum256(blob)
	blobDigest := "sha256:" + hex.EncodeToString(blobSum[:])
	if _, err := connector.PutBlob(context.Background(), "regstair/runtime-selection-check", blobDigest, bytes.NewReader(blob)); err != nil {
		t.Fatalf("runtime selected Harbor PutBlob error = %v", err)
	}
	manifestBody := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + blobDigest + `","size":` + fmt.Sprint(len(blob)) + `},"layers":[]}`)
	manifest, err := registry.ParseManifest(manifestBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.PutManifest(context.Background(), "regstair/runtime-selection-check", "m5", manifest); err != nil {
		t.Fatalf("runtime selected Harbor PutManifest error = %v", err)
	}
	request.Secret = []byte("deliberately-invalid-secret")
	if err := verifier.Verify(context.Background(), request); !errors.Is(err, ErrUpstreamCredentials) {
		t.Fatalf("invalid Harbor credential error = %v", err)
	}
}
