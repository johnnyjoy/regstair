package auth

import (
	"context"
	"time"

	"regstair/internal/identity"
	"regstair/internal/metadata"
)

type GatewayAuthenticator struct {
	oci *OCITokenService
}

func NewGatewayAuthenticator(oci *OCITokenService) *GatewayAuthenticator {
	return &GatewayAuthenticator{oci: oci}
}

func (a *GatewayAuthenticator) Issue(ctx context.Context, username, password, repository string, actions []string) (string, time.Time, error) {
	return a.oci.Issue(ctx, username, password, repository, actions)
}

func (a *GatewayAuthenticator) Authenticate(ctx context.Context, token, repository, action string) (identity.Principal, error) {
	return a.oci.Authenticate(ctx, token, repository, action)
}

type RouteAuthorizer struct{}

func (RouteAuthorizer) Authorize(principal identity.Principal, operation metadata.Operation, _ string) bool {
	if operation == metadata.OperationPull {
		return true
	}
	return operation == metadata.OperationPush && principal.Kind == identity.KindLocalUser && principal.ID != ""
}
