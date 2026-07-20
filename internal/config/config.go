package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"regstair/internal/policy"
)

type Config struct {
	Version     int          `yaml:"version" json:"version"`
	Clients     []Client     `yaml:"clients" json:"clients"`
	Credentials []Credential `yaml:"credentials" json:"credentials"`
	Sources     []Source     `yaml:"sources" json:"sources"`
	Routes      []Route      `yaml:"routes" json:"routes"`
}

type Source struct {
	ID              string          `yaml:"id" json:"id"`
	Name            string          `yaml:"name" json:"name"`
	Endpoint        string          `yaml:"endpoint" json:"endpoint"`
	Type            string          `yaml:"type" json:"type"`
	Enabled         bool            `yaml:"enabled" json:"enabled"`
	Auth            Auth            `yaml:"auth" json:"auth"`
	UserCredentials UserCredentials `yaml:"user_credentials" json:"user_credentials"`
}

type UserCredentials struct {
	Approved               bool   `yaml:"approved" json:"approved"`
	Pull                   bool   `yaml:"pull" json:"pull"`
	Push                   bool   `yaml:"push" json:"push"`
	VerificationRepository string `yaml:"verification_repository" json:"verification_repository"`
	Guidance               string `yaml:"guidance" json:"guidance,omitempty"`
	AllowInsecure          bool   `yaml:"allow_insecure" json:"allow_insecure"`
}

type Credential struct {
	ID          string `yaml:"id" json:"id"`
	Type        string `yaml:"type" json:"type"`
	UsernameEnv string `yaml:"username_env" json:"username_env"`
	PasswordEnv string `yaml:"password_env" json:"password_env"`
}

type Client struct {
	ID          string        `yaml:"id" json:"id"`
	Type        string        `yaml:"type" json:"type"`
	UsernameEnv string        `yaml:"username_env" json:"username_env"`
	PasswordEnv string        `yaml:"password_env" json:"password_env"`
	Allowed     ClientAllowed `yaml:"allowed" json:"allowed"`
}

type ClientAllowed struct {
	Pull []string `yaml:"pull" json:"pull"`
	Push []string `yaml:"push" json:"push"`
}

type Auth struct {
	Mode          string   `yaml:"mode" json:"mode"`
	CredentialRef string   `yaml:"credential_ref" json:"credential_ref"`
	Strategy      string   `yaml:"strategy" json:"strategy"`
	TokenHosts    []string `yaml:"token_hosts" json:"token_hosts,omitempty"`
}

type Route struct {
	Name       string  `yaml:"name" json:"name"`
	Match      string  `yaml:"match" json:"match"`
	Precedence int     `yaml:"precedence" json:"precedence"`
	Pull       Pull    `yaml:"pull" json:"pull"`
	Push       Push    `yaml:"push" json:"push"`
	Rewrite    Rewrite `yaml:"rewrite" json:"rewrite"`
}

type Pull struct {
	Sources          []string `yaml:"sources" json:"sources"`
	Authoritative    string   `yaml:"authoritative" json:"authoritative"`
	ExternalFallback bool     `yaml:"external_fallback" json:"external_fallback"`
}

type Push struct {
	Destination string `yaml:"destination" json:"destination"`
	Deny        bool   `yaml:"deny" json:"deny"`
}

type Rewrite struct {
	StripPrefix string `yaml:"strip_prefix" json:"strip_prefix"`
	AddPrefix   string `yaml:"add_prefix" json:"add_prefix"`
}

const (
	CredentialTypeBasic = "basic"

	AuthModeNone        = "none"
	AuthModeProxy       = "proxy"
	AuthModeCurrentUser = "current_user"

	AuthStrategyChallenge = "challenge"
	AuthStrategyRequired  = "required"
)

func LoadFile(path string) (*Config, error) {
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
	default:
		return nil, fmt.Errorf("unsupported config file extension %q", filepath.Ext(path))
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config file: %w", err)
	}
	return &cfg, nil
}

func (c Config) PolicyConfig() (policy.Config, error) {
	if err := c.validate(); err != nil {
		return policy.Config{}, err
	}

	policyConfig := policy.Config{
		Sources: make([]policy.Source, 0, len(c.Sources)),
		Routes:  make([]policy.Route, 0, len(c.Routes)),
	}

	for _, source := range c.Sources {
		policyConfig.Sources = append(policyConfig.Sources, policy.Source{ID: source.ID})
	}

	for _, route := range c.Routes {
		policyConfig.Routes = append(policyConfig.Routes, policy.Route{
			Name:             route.Name,
			Match:            route.Match,
			Precedence:       route.Precedence,
			PullSources:      append([]string(nil), route.Pull.Sources...),
			Authoritative:    route.Pull.Authoritative,
			ExternalFallback: route.Pull.ExternalFallback,
			PushDestination:  route.Push.Destination,
			PushDenied:       route.Push.Deny,
			Rewrite: policy.Rewrite{
				StripPrefix: route.Rewrite.StripPrefix,
				AddPrefix:   route.Rewrite.AddPrefix,
			},
		})
	}

	if _, err := policy.NewEngine(policyConfig); err != nil {
		return policy.Config{}, err
	}

	return policyConfig, nil
}

func (c Config) validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}

	routeNames := map[string]struct{}{}
	for _, route := range c.Routes {
		if route.Name != "" {
			routeNames[route.Name] = struct{}{}
		}
	}

	clientIDs := map[string]struct{}{}
	for i, client := range c.Clients {
		if client.ID == "" {
			return fmt.Errorf("client %d has no id", i)
		}
		if _, exists := clientIDs[client.ID]; exists {
			return fmt.Errorf("duplicate client id %q", client.ID)
		}
		clientIDs[client.ID] = struct{}{}
		if client.Type != CredentialTypeBasic {
			return fmt.Errorf("client %q has unsupported type %q", client.ID, client.Type)
		}
		if client.UsernameEnv == "" {
			return fmt.Errorf("client %q has no username_env", client.ID)
		}
		if client.PasswordEnv == "" {
			return fmt.Errorf("client %q has no password_env", client.ID)
		}
		for _, route := range client.Allowed.Pull {
			if _, exists := routeNames[route]; !exists {
				return fmt.Errorf("client %q pull authorization references unknown route %q", client.ID, route)
			}
		}
		for _, route := range client.Allowed.Push {
			if _, exists := routeNames[route]; !exists {
				return fmt.Errorf("client %q push authorization references unknown route %q", client.ID, route)
			}
		}
	}

	credentialIDs := map[string]struct{}{}
	for i, credential := range c.Credentials {
		if credential.ID == "" {
			return fmt.Errorf("credential %d has no id", i)
		}
		if _, exists := credentialIDs[credential.ID]; exists {
			return fmt.Errorf("duplicate credential id %q", credential.ID)
		}
		credentialIDs[credential.ID] = struct{}{}
		if credential.Type != CredentialTypeBasic {
			return fmt.Errorf("credential %q has unsupported type %q", credential.ID, credential.Type)
		}
		if credential.UsernameEnv == "" {
			return fmt.Errorf("credential %q has no username_env", credential.ID)
		}
		if credential.PasswordEnv == "" {
			return fmt.Errorf("credential %q has no password_env", credential.ID)
		}
	}

	sourceIDs := map[string]struct{}{}
	sourcesByID := map[string]Source{}
	for i, source := range c.Sources {
		if source.ID == "" {
			return fmt.Errorf("source %d has no id", i)
		}
		if _, exists := sourceIDs[source.ID]; exists {
			return fmt.Errorf("duplicate source id %q", source.ID)
		}
		sourceIDs[source.ID] = struct{}{}
		sourcesByID[source.ID] = source
		if source.Endpoint == "" {
			return fmt.Errorf("source %q has no endpoint", source.ID)
		}
		endpoint, err := validateSourceEndpoint(source)
		if err != nil {
			return err
		}
		if err := validateSourceAuth(source, credentialIDs); err != nil {
			return err
		}
		if err := validateUserCredentials(source, endpoint); err != nil {
			return err
		}
	}
	for _, route := range c.Routes {
		for _, sourceID := range route.Pull.Sources {
			source, ok := sourcesByID[sourceID]
			if ok && source.Auth.Mode == AuthModeCurrentUser && !source.UserCredentials.Pull {
				return fmt.Errorf("route %q pulls from current-user source %q that does not allow user pull credentials", route.Name, sourceID)
			}
		}
		if source, ok := sourcesByID[route.Push.Destination]; ok && source.Auth.Mode == AuthModeCurrentUser && !route.Push.Deny && !source.UserCredentials.Push {
			return fmt.Errorf("route %q pushes to current-user source %q that does not allow user push credentials", route.Name, source.ID)
		}
	}

	return nil
}

func validateSourceEndpoint(source Source) (*url.URL, error) {
	endpoint, err := url.Parse(source.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("source %q endpoint must be an absolute HTTP or HTTPS URL", source.ID)
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("source %q endpoint cannot contain user info, query, or fragment", source.ID)
	}
	return endpoint, nil
}

func validateUserCredentials(source Source, endpoint *url.URL) error {
	policy := source.UserCredentials
	configured := policy.Pull || policy.Push || policy.VerificationRepository != "" || policy.Guidance != "" || policy.AllowInsecure
	if !policy.Approved {
		if configured {
			return fmt.Errorf("source %q has user credential settings but is not approved", source.ID)
		}
		return nil
	}
	if !policy.Pull && !policy.Push {
		return fmt.Errorf("source %q approved user credentials allow no operations", source.ID)
	}
	if policy.VerificationRepository == "" {
		return fmt.Errorf("source %q approved user credentials require verification_repository", source.ID)
	}
	if policy.VerificationRepository != strings.TrimSpace(policy.VerificationRepository) || strings.ContainsAny(policy.VerificationRepository, " :@\t\r\n") || strings.HasPrefix(policy.VerificationRepository, "/") || strings.HasSuffix(policy.VerificationRepository, "/") || strings.Contains(policy.VerificationRepository, "..") {
		return fmt.Errorf("source %q has invalid verification_repository", source.ID)
	}
	if endpoint.Scheme != "https" && !policy.AllowInsecure {
		return fmt.Errorf("source %q approved user credentials require HTTPS or allow_insecure", source.ID)
	}
	return nil
}

func validateSourceAuth(source Source, credentialIDs map[string]struct{}) error {
	mode := source.Auth.Mode
	if mode == "" {
		mode = AuthModeNone
	}

	strategy := source.Auth.Strategy
	if strategy == "" {
		strategy = AuthStrategyChallenge
	}
	if strategy != AuthStrategyChallenge && strategy != AuthStrategyRequired {
		return fmt.Errorf("source %q has unsupported auth strategy %q", source.ID, strategy)
	}
	seenTokenHosts := map[string]struct{}{}
	for _, host := range source.Auth.TokenHosts {
		if host == "" || host != strings.TrimSpace(host) || strings.ContainsAny(host, "/?#@ \t\r\n") {
			return fmt.Errorf("source %q has invalid auth token host %q", source.ID, host)
		}
		key := strings.ToLower(host)
		if _, exists := seenTokenHosts[key]; exists {
			return fmt.Errorf("source %q has duplicate auth token host %q", source.ID, host)
		}
		seenTokenHosts[key] = struct{}{}
	}

	switch mode {
	case AuthModeNone:
		if source.Auth.CredentialRef != "" || source.Auth.Strategy != "" || len(source.Auth.TokenHosts) > 0 {
			return fmt.Errorf("source %q anonymous auth cannot configure credentials or a strategy", source.ID)
		}
		return nil
	case AuthModeProxy:
		if source.Auth.CredentialRef == "" {
			return fmt.Errorf("source %q proxy auth has no credential_ref", source.ID)
		}
		if _, exists := credentialIDs[source.Auth.CredentialRef]; !exists {
			return fmt.Errorf("source %q references unknown credential %q", source.ID, source.Auth.CredentialRef)
		}
		return nil
	case AuthModeCurrentUser:
		if source.Auth.CredentialRef != "" {
			return fmt.Errorf("source %q current-user auth cannot configure credential_ref", source.ID)
		}
		if !source.UserCredentials.Approved {
			return fmt.Errorf("source %q current-user auth requires approved user credentials", source.ID)
		}
		return nil
	case "client_passthrough", "identity_mapped":
		return fmt.Errorf("source %q auth mode %q is not implemented yet", source.ID, mode)
	default:
		return fmt.Errorf("source %q has unsupported auth mode %q", source.ID, mode)
	}
}
