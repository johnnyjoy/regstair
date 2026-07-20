package auth

import (
	"testing"

	"regstair/internal/identity"
	"regstair/internal/metadata"
)

func TestRouteAuthorizerPreservesAnonymousPullAndRequiresLocalUserForPush(t *testing.T) {
	authorizer := RouteAuthorizer{}
	if !authorizer.Authorize(identity.Anonymous(), metadata.OperationPull, "public") {
		t.Fatal("anonymous pull was denied")
	}
	if authorizer.Authorize(identity.Anonymous(), metadata.OperationPush, "publish") {
		t.Fatal("anonymous push was allowed")
	}
	if !authorizer.Authorize(identity.Principal{Kind: identity.KindLocalUser, ID: "user-1"}, metadata.OperationPush, "publish") {
		t.Fatal("authenticated local-user push was denied")
	}
}
