package auth

import (
	"context"
	"fmt"
	"net/http"

	"regstair/internal/config"
	"regstair/internal/identity"
	"regstair/internal/metadata"
	"regstair/internal/registry"
)

type RuntimeConnectorFactory func(config.Source, string, string) (registry.Connector, error)

type RuntimeCredentialSelector struct {
	repo       securityRepository
	keyring    *SecretKeyring
	sources    map[string]config.Source
	connectors map[string]registry.Connector
	factory    RuntimeConnectorFactory
}

func NewRuntimeCredentialSelector(repo securityRepository, keyring *SecretKeyring, sources []config.Source, connectors map[string]registry.Connector, client *http.Client) *RuntimeCredentialSelector {
	byID := make(map[string]config.Source, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	factory := func(source config.Source, username, password string) (registry.Connector, error) {
		options := []registry.HTTPOption{registry.WithBasicAuth(username, password)}
		if len(source.Auth.TokenHosts) > 0 {
			options = append(options, registry.WithAllowedTokenHosts(source.Auth.TokenHosts...))
		}
		if source.Auth.Strategy == config.AuthStrategyRequired {
			options = append(options, registry.WithPreemptiveBasicAuth())
		}
		return registry.NewHTTPConnector(source.ID, source.Endpoint, client, options...)
	}
	return &RuntimeCredentialSelector{repo: repo, keyring: keyring, sources: byID, connectors: connectors, factory: factory}
}

func (s *RuntimeCredentialSelector) ConnectorFor(ctx context.Context, principal identity.Principal, sourceID string, operation metadata.Operation) (registry.Connector, string, error) {
	source, ok := s.sources[sourceID]
	if !ok || !source.Enabled {
		return nil, "", fmt.Errorf("%w: source %s", registry.ErrUnavailable, sourceID)
	}
	if !source.UserCredentials.Approved {
		return s.connectors[sourceID], "anonymous", nil
	}
	if principal.Kind != identity.KindLocalUser || principal.ID == "" {
		if operation == metadata.OperationPull {
			return s.connectors[sourceID], "anonymous", nil
		}
		return nil, "", fmt.Errorf("%w: local user authentication is required", registry.ErrCredentialRequired)
	}
	if (operation == metadata.OperationPull && !source.UserCredentials.Pull) || (operation == metadata.OperationPush && !source.UserCredentials.Push) {
		return nil, "", fmt.Errorf("%w: credential is not approved for %s", registry.ErrAuthorization, operation)
	}
	user, err := s.repo.FindUserByID(ctx, principal.ID)
	if err != nil {
		return nil, "", err
	}
	if user == nil || !user.Enabled {
		return nil, "", fmt.Errorf("%w: local user is disabled", registry.ErrAuthorization)
	}
	credential, err := s.repo.FindRegistryCredential(ctx, principal.ID, sourceID)
	if err != nil {
		return nil, "", err
	}
	if credential == nil {
		if operation == metadata.OperationPull {
			return s.connectors[sourceID], "anonymous", nil
		}
		return nil, "", fmt.Errorf("%w: no credential for source %s", registry.ErrCredentialRequired, sourceID)
	}
	secret, err := s.keyring.Decrypt(credential.ID, principal.ID, sourceID, credential.EncryptedSecret)
	if err != nil {
		return nil, "", fmt.Errorf("%w: credential cannot be decrypted", registry.ErrCredentialUnavailable)
	}
	defer clearBytes(secret)
	connector, err := s.factory(source, credential.Username, string(secret))
	if err != nil {
		return nil, "", fmt.Errorf("%w: create request connector", registry.ErrCredentialUnavailable)
	}
	return connector, "current_user", nil
}

func (s *RuntimeCredentialSelector) AuthorizeCache(ctx context.Context, principal identity.Principal, sourceID string, operation metadata.Operation) (string, error) {
	source, ok := s.sources[sourceID]
	if !ok || !source.UserCredentials.Approved {
		return "anonymous", nil
	}
	if operation == metadata.OperationPull {
		return "anonymous", nil
	}
	if principal.Kind != identity.KindLocalUser || principal.ID == "" {
		return "", registry.ErrCredentialRequired
	}
	user, err := s.repo.FindUserByID(ctx, principal.ID)
	if err != nil || user == nil || !user.Enabled {
		return "", registry.ErrAuthorization
	}
	credential, err := s.repo.FindRegistryCredential(ctx, principal.ID, sourceID)
	if err != nil {
		return "", err
	}
	if credential == nil {
		return "", registry.ErrCredentialRequired
	}
	return "current_user", nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
