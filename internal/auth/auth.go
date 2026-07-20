package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"os"

	"regstair/internal/config"
	"regstair/internal/identity"
	"regstair/internal/metadata"
)

type BasicCredential struct {
	Username string
	Password string
}

type Store struct {
	basic map[string]BasicCredential
}

type Authenticator struct {
	clients []basicClient
}

type GatewayAuthenticator struct {
	configured *Authenticator
	local      *DockerTokenService
}

func NewGatewayAuthenticator(configured *Authenticator, local *DockerTokenService) *GatewayAuthenticator {
	return &GatewayAuthenticator{configured: configured, local: local}
}

func (a *GatewayAuthenticator) AuthenticateBasic(username, password string) (identity.Principal, bool) {
	if a == nil {
		return identity.Principal{}, false
	}
	if a.configured != nil {
		if id, ok := a.configured.AuthenticateBasic(username, password); ok {
			return identity.Principal{Kind: identity.KindConfiguredClient, ID: id, Username: username}, true
		}
	}
	if a.local != nil {
		if user, err := a.local.Authenticate(context.Background(), username, password); err == nil {
			return identity.Principal{Kind: identity.KindLocalUser, ID: user.ID, Username: user.Username}, true
		}
	}
	return identity.Principal{}, false
}

func (a *GatewayAuthenticator) Enabled() bool {
	return a != nil && (a.local != nil || (a.configured != nil && a.configured.Enabled()))
}
func (a *GatewayAuthenticator) AuthenticationRequired() bool {
	return a != nil && a.configured != nil && a.configured.Enabled()
}

type RouteAuthorizer struct {
	permissions map[string]clientPermissions
}

type clientPermissions struct {
	pull map[string]struct{}
	push map[string]struct{}
}

type basicClient struct {
	id       string
	username string
	password string
}

func LoadStore(cfg config.Config, lookup func(string) (string, bool)) (*Store, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}

	store := &Store{basic: map[string]BasicCredential{}}
	for _, credential := range cfg.Credentials {
		switch credential.Type {
		case config.CredentialTypeBasic:
			username, ok := lookup(credential.UsernameEnv)
			if !ok {
				return nil, fmt.Errorf("credential %q username env %q is not set", credential.ID, credential.UsernameEnv)
			}
			password, ok := lookup(credential.PasswordEnv)
			if !ok {
				return nil, fmt.Errorf("credential %q password env %q is not set", credential.ID, credential.PasswordEnv)
			}
			store.basic[credential.ID] = BasicCredential{Username: username, Password: password}
		default:
			return nil, fmt.Errorf("credential %q has unsupported type %q", credential.ID, credential.Type)
		}
	}
	return store, nil
}

func (s *Store) Basic(id string) (BasicCredential, bool) {
	if s == nil {
		return BasicCredential{}, false
	}
	credential, ok := s.basic[id]
	return credential, ok
}

func LoadAuthenticator(cfg config.Config, lookup func(string) (string, bool)) (*Authenticator, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}

	authenticator := &Authenticator{}
	for _, client := range cfg.Clients {
		switch client.Type {
		case config.CredentialTypeBasic:
			username, ok := lookup(client.UsernameEnv)
			if !ok {
				return nil, fmt.Errorf("client %q username env %q is not set", client.ID, client.UsernameEnv)
			}
			password, ok := lookup(client.PasswordEnv)
			if !ok {
				return nil, fmt.Errorf("client %q password env %q is not set", client.ID, client.PasswordEnv)
			}
			authenticator.clients = append(authenticator.clients, basicClient{
				id:       client.ID,
				username: username,
				password: password,
			})
		default:
			return nil, fmt.Errorf("client %q has unsupported type %q", client.ID, client.Type)
		}
	}
	return authenticator, nil
}

func (a *Authenticator) AuthenticateBasic(username string, password string) (string, bool) {
	if a == nil {
		return "", false
	}
	for _, client := range a.clients {
		if constantTimeEqual(username, client.username) && constantTimeEqual(password, client.password) {
			return client.id, true
		}
	}
	return "", false
}

func (a *Authenticator) Enabled() bool {
	return a != nil && len(a.clients) > 0
}

func NewRouteAuthorizer(cfg config.Config) *RouteAuthorizer {
	authorizer := &RouteAuthorizer{permissions: map[string]clientPermissions{}}
	for _, client := range cfg.Clients {
		permissions := clientPermissions{
			pull: setFromSlice(client.Allowed.Pull),
			push: setFromSlice(client.Allowed.Push),
		}
		authorizer.permissions[client.ID] = permissions
	}
	return authorizer
}

func (a *RouteAuthorizer) Authorize(principal identity.Principal, operation metadata.Operation, route string) bool {
	if a == nil {
		return true
	}
	if principal.Kind == identity.KindLocalUser {
		return true
	}
	permissions, ok := a.permissions[principal.ID]
	if !ok {
		return false
	}
	switch operation {
	case metadata.OperationPull:
		_, ok = permissions.pull[route]
		return ok
	case metadata.OperationPush:
		_, ok = permissions.push[route]
		return ok
	default:
		return false
	}
}

func constantTimeEqual(a string, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func setFromSlice(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
