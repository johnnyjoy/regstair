package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"regstair/internal/registry"
)

type HarborCredentialVerifier struct{ client *http.Client }

func NewHarborCredentialVerifier(client *http.Client) *HarborCredentialVerifier {
	return &HarborCredentialVerifier{client: client}
}

func (v *HarborCredentialVerifier) Verify(ctx context.Context, request VerificationRequest) error {
	if request.Endpoint == "" || request.Repository == "" || request.Username == "" || len(request.Secret) == 0 || (!request.Pull && !request.Push) {
		return ErrVerificationConfig
	}
	options := []registry.HTTPOption{registry.WithBasicAuth(request.Username, string(request.Secret)), registry.WithPreemptiveBasicAuth()}
	if len(request.TokenHosts) > 0 {
		options = append(options, registry.WithAllowedTokenHosts(request.TokenHosts...))
	}
	connector, err := registry.NewHTTPConnector(request.SourceID, request.Endpoint, v.client, options...)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerificationConfig, err)
	}
	if err := connector.Health(ctx); err != nil {
		return mapRegistryVerificationError(err)
	}
	if request.Pull {
		_, err := connector.ResolveManifest(ctx, request.Repository, "__regstair_credential_check__")
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return mapRegistryVerificationError(err)
		}
	}
	if request.Push {
		if err := connector.VerifyPushScope(ctx, request.Repository); err != nil {
			return mapRegistryVerificationError(err)
		}
	}
	return nil
}

func mapRegistryVerificationError(err error) error {
	switch {
	case errors.Is(err, registry.ErrAuthentication):
		return fmt.Errorf("%w: %v", ErrUpstreamCredentials, err)
	case errors.Is(err, registry.ErrAuthorization):
		return fmt.Errorf("%w: %v", ErrUpstreamPermission, err)
	case errors.Is(err, registry.ErrUnavailable):
		return fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	default:
		return fmt.Errorf("%w: %v", ErrUpstreamFailure, err)
	}
}
