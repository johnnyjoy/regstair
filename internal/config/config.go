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
	Version int      `yaml:"version" json:"version"`
	Sources []Source `yaml:"sources" json:"sources"`
	Routes  []Route  `yaml:"routes" json:"routes"`
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
	Pull                   bool   `yaml:"pull" json:"pull"`
	Push                   bool   `yaml:"push" json:"push"`
	VerificationRepository string `yaml:"verification_repository" json:"verification_repository"`
	Guidance               string `yaml:"guidance" json:"guidance,omitempty"`
	AllowInsecure          bool   `yaml:"allow_insecure" json:"allow_insecure"`
}

type Auth struct {
	Strategy   string   `yaml:"strategy" json:"strategy"`
	TokenHosts []string `yaml:"token_hosts" json:"token_hosts,omitempty"`
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
	AuthStrategyChallenge           = "challenge"
	AuthStrategyProxy               = "proxy"
	AuthStrategyCurrentUserRequired = "current_user_required"
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

	sourceIDs := map[string]struct{}{}
	for i, source := range c.Sources {
		if source.ID == "" {
			return fmt.Errorf("source %d has no id", i)
		}
		if _, exists := sourceIDs[source.ID]; exists {
			return fmt.Errorf("duplicate source id %q", source.ID)
		}
		sourceIDs[source.ID] = struct{}{}
		if source.Endpoint == "" {
			return fmt.Errorf("source %q has no endpoint", source.ID)
		}
		endpoint, err := validateSourceEndpoint(source)
		if err != nil {
			return err
		}
		if err := validateSourceAuth(source); err != nil {
			return err
		}
		if err := validateUserCredentials(source, endpoint); err != nil {
			return err
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
	credentialConfigured := policy.Pull || policy.Push || policy.VerificationRepository != "" || policy.Guidance != "" || policy.AllowInsecure
	if policy.VerificationRepository != "" && (policy.VerificationRepository != strings.TrimSpace(policy.VerificationRepository) || strings.ContainsAny(policy.VerificationRepository, " :@\t\r\n") || strings.HasPrefix(policy.VerificationRepository, "/") || strings.HasSuffix(policy.VerificationRepository, "/") || strings.Contains(policy.VerificationRepository, "..")) {
		return fmt.Errorf("source %q has invalid verification_repository", source.ID)
	}
	if credentialConfigured && endpoint.Scheme != "https" && !policy.AllowInsecure {
		return fmt.Errorf("source %q requires HTTPS or explicit allow_insecure", source.ID)
	}
	return nil
}

func validateSourceAuth(source Source) error {
	strategy := source.Auth.Strategy
	if strategy == "" {
		strategy = AuthStrategyChallenge
	}
	if strategy != AuthStrategyChallenge && strategy != AuthStrategyProxy && strategy != AuthStrategyCurrentUserRequired {
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

	return nil
}
