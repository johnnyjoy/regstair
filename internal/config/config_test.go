package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileParsesMVPYAMLAndBuildsPolicyConfig(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: internal-curated
    name: Internal Curated
    endpoint: http://internal-registry:5000
    type: internal
    enabled: true
  - id: docker-hub
    name: Docker Hub Stand-in
    endpoint: http://external-registry:5000
    type: external
    enabled: true
  - id: harbor-team-a
    name: Team A Destination
    endpoint: http://destination-registry:5000
    type: internal
    enabled: true
routes:
  - name: curated-library
    match: library/**
    precedence: 10
    pull:
      sources:
        - internal-curated
        - docker-hub
      authoritative: internal-curated
      external_fallback: true
    push:
      destination: internal-curated
    rewrite:
      strip_prefix: library/
      add_prefix: library/
  - name: team-a
    match: team-a/**
    precedence: 20
    pull:
      sources:
        - harbor-team-a
      authoritative: harbor-team-a
      external_fallback: false
    push:
      destination: harbor-team-a
    rewrite:
      strip_prefix: team-a/
      add_prefix: production-team-a/
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	policyConfig, err := cfg.PolicyConfig()
	if err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}

	if got, want := len(policyConfig.Sources), 3; got != want {
		t.Fatalf("source count = %d, want %d", got, want)
	}
	if got, want := policyConfig.Routes[0].PullSources[1], "docker-hub"; got != want {
		t.Fatalf("second pull source = %q, want %q", got, want)
	}
	if got, want := policyConfig.Routes[1].Rewrite.AddPrefix, "production-team-a/"; got != want {
		t.Fatalf("rewrite add prefix = %q, want %q", got, want)
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources: []
routes: []
surprise: true
`)

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want unknown field error")
	}
}

func TestLoadFileRejectsUnsupportedExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regstair.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want unsupported extension error")
	}
}

func TestPolicyConfigRejectsInvalidRouteReferences(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: internal-curated
    endpoint: http://internal-registry:5000
routes:
  - name: library
    match: library/**
    precedence: 10
    pull:
      sources:
        - internal-curated
        - docker-hub
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want policy validation error")
	}
}

func TestPolicyConfigRejectsUnsupportedVersion(t *testing.T) {
	path := writeConfig(t, `
version: 2
sources: []
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want unsupported version error")
	}
}

func TestPolicyConfigRejectsSourceWithoutEndpoint(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: internal-curated
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want missing endpoint error")
	}
}

func TestPolicyConfigAcceptsProxyBasicAuthCredentialReference(t *testing.T) {
	path := writeConfig(t, `
version: 1
credentials:
  - id: docker-hub-basic
    type: basic
    username_env: REGSTAIR_DOCKER_HUB_USERNAME
    password_env: REGSTAIR_DOCKER_HUB_PASSWORD
sources:
  - id: docker-hub
    endpoint: https://registry-1.docker.io
    auth:
      mode: proxy
      credential_ref: docker-hub-basic
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}
	if got, want := cfg.Sources[0].Auth.Mode, "proxy"; got != want {
		t.Fatalf("auth mode = %q, want %q", got, want)
	}
}

func TestPolicyConfigRejectsProxyAuthWithoutKnownCredential(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: docker-hub
    endpoint: https://registry-1.docker.io
    auth:
      mode: proxy
      credential_ref: missing
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want missing credential reference error")
	}
}

func TestPolicyConfigRejectsDeferredAuthModes(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: docker-hub
    endpoint: https://registry-1.docker.io
    auth:
      mode: client_passthrough
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want unsupported auth mode error")
	}
}

func TestPolicyConfigAcceptsCurrentUserChallengeAuth(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: https://harbor.example
    enabled: true
    auth:
      mode: current_user
      strategy: challenge
    user_credentials:
      approved: true
      pull: true
      push: true
      verification_repository: regstair/check
routes: []
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}
}

func TestPolicyConfigValidatesExplicitTokenHosts(t *testing.T) {
	valid := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: https://registry.example
    enabled: true
    auth:
      mode: current_user
      token_hosts:
        - auth.example
    user_credentials:
      approved: true
      pull: true
      verification_repository: regstair/check
routes: []
`)
	cfg, err := LoadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("valid token host: %v", err)
	}

	invalid := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: https://registry.example
    enabled: true
    auth:
      mode: current_user
      token_hosts:
        - https://evil.example/token
    user_credentials:
      approved: true
      pull: true
      verification_repository: regstair/check
routes: []
`)
	cfg, err = LoadFile(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err == nil {
		t.Fatal("PolicyConfig() accepted a token host containing a URL")
	}
}

func TestPolicyConfigAcceptsBasicClients(t *testing.T) {
	path := writeConfig(t, `
version: 1
clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
    allowed:
      pull:
        - curated-library
      push:
        - team-a-publish
sources: []
routes:
  - name: curated-library
    match: library/**
    precedence: 10
    pull:
      sources:
        - internal-curated
  - name: team-a-publish
    match: team-a/**
    precedence: 20
    push:
      destination: harbor-team-a
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}
	if got, want := cfg.Clients[0].ID, "ci-builder"; got != want {
		t.Fatalf("client id = %q, want %q", got, want)
	}
}

func TestPolicyConfigRejectsClientAllowedUnknownRoute(t *testing.T) {
	path := writeConfig(t, `
version: 1
clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
    allowed:
      pull:
        - missing-route
sources: []
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want unknown allowed route error")
	}
}

func TestPolicyConfigRejectsDuplicateClients(t *testing.T) {
	path := writeConfig(t, `
version: 1
clients:
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_CI_USERNAME
    password_env: REGSTAIR_CLIENT_CI_PASSWORD
  - id: ci-builder
    type: basic
    username_env: REGSTAIR_CLIENT_OTHER_USERNAME
    password_env: REGSTAIR_CLIENT_OTHER_PASSWORD
sources: []
routes: []
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	_, err = cfg.PolicyConfig()
	if err == nil {
		t.Fatal("PolicyConfig() error = nil, want duplicate client error")
	}
}

func TestPolicyConfigAcceptsApprovedUserCredentialSource(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    name: Harbor
    endpoint: https://harbor.example.test
    enabled: true
    user_credentials:
      approved: true
      pull: true
      push: true
      verification_repository: regstair/credential-check
      guidance: Use a Harbor robot or local account.
routes: []
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}
	policy := cfg.Sources[0].UserCredentials
	if !policy.Approved || !policy.Pull || !policy.Push || policy.VerificationRepository != "regstair/credential-check" {
		t.Fatalf("user credential policy = %#v", policy)
	}
}

func TestPolicyConfigRejectsUnsafeOrContradictoryUserCredentialSource(t *testing.T) {
	tests := []struct{ name, policy string }{
		{"plaintext without opt in", "approved: true\n      pull: true\n      verification_repository: check/repo"},
		{"no operations", "approved: true\n      verification_repository: check/repo"},
		{"no verification repository", "approved: true\n      pull: true"},
		{"settings while unapproved", "approved: false\n      pull: true\n      verification_repository: check/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, "version: 1\nsources:\n  - id: harbor\n    endpoint: http://harbor:5000\n    enabled: true\n    user_credentials:\n      "+tt.policy+"\nroutes: []\n")
			cfg, err := LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.PolicyConfig(); err == nil {
				t.Fatal("PolicyConfig() error = nil")
			}
		})
	}
}

func TestPolicyConfigAllowsExplicitInsecureApprovedFixture(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: http://harbor:5000
    enabled: true
    user_credentials:
      approved: true
      pull: true
      verification_repository: check/repo
      allow_insecure: true
routes: []
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}
}

func TestPolicyConfigRejectsDuplicateSourceIDs(t *testing.T) {
	path := writeConfig(t, "version: 1\nsources:\n  - id: duplicate\n    endpoint: https://one.example\n  - id: duplicate\n    endpoint: https://two.example\nroutes: []\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err == nil {
		t.Fatal("PolicyConfig() error = nil")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := LoadFile("../../config/regstair.example.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if _, err := cfg.PolicyConfig(); err != nil {
		t.Fatalf("PolicyConfig() error = %v", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "regstair.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}
