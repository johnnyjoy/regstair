package auth

import (
	"testing"

	"regstair/internal/config"
	"regstair/internal/identity"
	"regstair/internal/metadata"
)

func TestLoadStoreLoadsBasicCredentialsFromEnvironment(t *testing.T) {
	store, err := LoadStore(config.Config{
		Credentials: []config.Credential{
			{
				ID:          "docker-hub-basic",
				Type:        config.CredentialTypeBasic,
				UsernameEnv: "REGSTAIR_DOCKER_HUB_USERNAME",
				PasswordEnv: "REGSTAIR_DOCKER_HUB_PASSWORD",
			},
		},
	}, mapLookup(map[string]string{
		"REGSTAIR_DOCKER_HUB_USERNAME": "regstair",
		"REGSTAIR_DOCKER_HUB_PASSWORD": "secret",
	}))
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}

	credential, ok := store.Basic("docker-hub-basic")
	if !ok {
		t.Fatal("Basic() ok = false, want true")
	}
	if got, want := credential.Username, "regstair"; got != want {
		t.Fatalf("username = %q, want %q", got, want)
	}
	if got, want := credential.Password, "secret"; got != want {
		t.Fatalf("password = %q, want %q", got, want)
	}
}

func TestLoadStoreRejectsMissingCredentialEnvironment(t *testing.T) {
	_, err := LoadStore(config.Config{
		Credentials: []config.Credential{
			{
				ID:          "docker-hub-basic",
				Type:        config.CredentialTypeBasic,
				UsernameEnv: "REGSTAIR_DOCKER_HUB_USERNAME",
				PasswordEnv: "REGSTAIR_DOCKER_HUB_PASSWORD",
			},
		},
	}, mapLookup(map[string]string{
		"REGSTAIR_DOCKER_HUB_USERNAME": "regstair",
	}))
	if err == nil {
		t.Fatal("LoadStore() error = nil, want missing password environment error")
	}
}

func TestLoadAuthenticatorValidatesConfiguredBasicClient(t *testing.T) {
	authenticator, err := LoadAuthenticator(config.Config{
		Clients: []config.Client{
			{
				ID:          "ci-builder",
				Type:        config.CredentialTypeBasic,
				UsernameEnv: "REGSTAIR_CLIENT_CI_USERNAME",
				PasswordEnv: "REGSTAIR_CLIENT_CI_PASSWORD",
			},
		},
	}, mapLookup(map[string]string{
		"REGSTAIR_CLIENT_CI_USERNAME": "ci",
		"REGSTAIR_CLIENT_CI_PASSWORD": "secret",
	}))
	if err != nil {
		t.Fatalf("LoadAuthenticator() error = %v", err)
	}

	identity, ok := authenticator.AuthenticateBasic("ci", "secret")
	if !ok {
		t.Fatal("AuthenticateBasic() ok = false, want true")
	}
	if got, want := identity, "ci-builder"; got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}

	if _, ok := authenticator.AuthenticateBasic("ci", "wrong"); ok {
		t.Fatal("AuthenticateBasic() ok = true for wrong password")
	}
}

func TestLoadAuthenticatorRejectsMissingClientEnvironment(t *testing.T) {
	_, err := LoadAuthenticator(config.Config{
		Clients: []config.Client{
			{
				ID:          "ci-builder",
				Type:        config.CredentialTypeBasic,
				UsernameEnv: "REGSTAIR_CLIENT_CI_USERNAME",
				PasswordEnv: "REGSTAIR_CLIENT_CI_PASSWORD",
			},
		},
	}, mapLookup(map[string]string{
		"REGSTAIR_CLIENT_CI_USERNAME": "ci",
	}))
	if err == nil {
		t.Fatal("LoadAuthenticator() error = nil, want missing client password environment error")
	}
}

func TestRouteAuthorizerAllowsConfiguredRoutesByOperation(t *testing.T) {
	authorizer := NewRouteAuthorizer(config.Config{
		Clients: []config.Client{
			{
				ID: "ci-builder",
				Allowed: config.ClientAllowed{
					Pull: []string{"curated-library"},
					Push: []string{"team-a-publish"},
				},
			},
		},
	})

	principal := identity.Principal{Kind: identity.KindConfiguredClient, ID: "ci-builder"}
	if !authorizer.Authorize(principal, metadata.OperationPull, "curated-library") {
		t.Fatal("Authorize(pull curated-library) = false, want true")
	}
	if !authorizer.Authorize(principal, metadata.OperationPush, "team-a-publish") {
		t.Fatal("Authorize(push team-a-publish) = false, want true")
	}
	if authorizer.Authorize(principal, metadata.OperationPush, "curated-library") {
		t.Fatal("Authorize(push curated-library) = true, want false")
	}
	if authorizer.Authorize(identity.Principal{Kind: identity.KindConfiguredClient, ID: "unknown"}, metadata.OperationPull, "curated-library") {
		t.Fatal("Authorize(unknown client) = true, want false")
	}
}

func TestRouteAuthorizerDeniesClientWithNoAllowedRoutes(t *testing.T) {
	authorizer := NewRouteAuthorizer(config.Config{
		Clients: []config.Client{{ID: "ci-builder"}},
	})

	if authorizer.Authorize(identity.Principal{Kind: identity.KindConfiguredClient, ID: "ci-builder"}, metadata.OperationPull, "curated-library") {
		t.Fatal("Authorize() = true, want deny by default")
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
