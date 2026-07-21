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

func TestExampleConfigProvidesMultiRegistryRoutingFabric(t *testing.T) {
	cfg, err := LoadFile(filepath.Join("..", "..", "config", "regstair.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(cfg.Sources), 9; got != want {
		t.Fatalf("source count = %d, want %d", got, want)
	}
	policyConfig, err := cfg.PolicyConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(policyConfig.Routes), 11; got != want {
		t.Fatalf("route count = %d, want %d", got, want)
	}
	want := map[string]string{
		"docker-hub-namespaced":        "dockerhub/",
		"github-container-registry":    "ghcr/",
		"quay":                         "quay/",
		"kubernetes":                   "k8s/",
		"google-container-registry":    "gcr/",
		"microsoft-container-registry": "mcr/",
		"ecr-public":                   "ecr-public/",
		"gitlab-container-registry":    "gitlab/",
		"nvidia-container-registry":    "nvcr/",
	}
	for _, route := range policyConfig.Routes {
		if prefix, ok := want[route.Name]; ok {
			if route.Rewrite.StripPrefix != prefix {
				t.Fatalf("route %q strip prefix = %q, want %q", route.Name, route.Rewrite.StripPrefix, prefix)
			}
			delete(want, route.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing provider routes: %v", want)
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

func TestLoadRejectsAuthenticationModes(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: docker-hub
    endpoint: https://registry-1.docker.io
    auth:
      mode: client_passthrough
routes: []
`)

	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted an authentication mode")
	}
}

func TestLoadRejectsRemovedConfiguredClients(t *testing.T) {
	path := writeConfig(t, "version: 1\nclients: []\nsources: []\nroutes: []\n")
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted removed clients configuration")
	}
}

func TestLoadRejectsRemovedSharedCredentials(t *testing.T) {
	path := writeConfig(t, "version: 1\ncredentials: []\nsources: []\nroutes: []\n")
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted removed credentials configuration")
	}
}

func TestPolicyConfigRejectsRemovedAuthModeField(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: https://harbor.example
    auth:
      mode: proxy
routes: []
`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted removed auth mode field")
	}
}

func TestPolicyConfigAcceptsClosedCredentialStrategies(t *testing.T) {
	for _, strategy := range []string{"challenge", "proxy", "current_user_required"} {
		t.Run(strategy, func(t *testing.T) {
			path := writeConfig(t, "version: 1\nsources:\n  - id: source\n    endpoint: https://registry.example\n    enabled: true\n    auth:\n      strategy: "+strategy+"\nroutes: []\n")
			cfg, err := LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.PolicyConfig(); err != nil {
				t.Fatal(err)
			}
		})
	}
	path := writeConfig(t, "version: 1\nsources:\n  - id: source\n    endpoint: https://registry.example\n    enabled: true\n    auth:\n      strategy: invented\nroutes: []\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.PolicyConfig(); err == nil {
		t.Fatal("unsupported credential strategy accepted")
	}
}

func TestPolicyConfigAcceptsCredentialChallengeCompatibility(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: https://harbor.example
    enabled: true
    auth:
      strategy: challenge
    user_credentials:
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
      token_hosts:
        - auth.example
    user_credentials:
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
      token_hosts:
        - https://evil.example/token
    user_credentials:
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

func TestPolicyConfigAcceptsEnabledUserCredentialSource(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    name: Harbor
    endpoint: https://harbor.example.test
    enabled: true
    user_credentials:
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
	if !policy.Pull || !policy.Push || policy.VerificationRepository != "regstair/credential-check" {
		t.Fatalf("user credential policy = %#v", policy)
	}
}

func TestPolicyConfigRejectsUnsafeOrContradictoryUserCredentialSource(t *testing.T) {
	tests := []struct{ name, policy string }{{"plaintext without opt in", "pull: true\n      verification_repository: check/repo"}}
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

func TestPolicyConfigAllowsExplicitInsecureFixture(t *testing.T) {
	path := writeConfig(t, `
version: 1
sources:
  - id: harbor
    endpoint: http://harbor:5000
    enabled: true
    user_credentials:
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
