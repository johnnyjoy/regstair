package resolution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"regstair/internal/content"
	"regstair/internal/identity"
	"regstair/internal/metadata"
	"regstair/internal/policy"
	"regstair/internal/registry"
)

var (
	ErrManifestDigestMismatch = errors.New("manifest digest mismatch")
	ErrStagedBlobMissing      = errors.New("staged blob missing")
)

type PushRequest struct {
	Repository string
	Reference  string
	Manifest   registry.Manifest
	Principal  identity.Principal
}

type PushResult struct {
	Route              string
	Destination        string
	LogicalRepository  string
	PhysicalRepository string
	Reference          string
	ManifestDigest     string
	Explanation        []string
	CredentialSource   string
}

type PushResolver struct {
	policy     *policy.Engine
	store      content.Store
	metadata   metadata.Repository
	connectors map[string]registry.Connector
	provider   ConnectorProvider
	authorizer Authorizer
	now        func() time.Time
}

func NewPushResolver(policyEngine *policy.Engine, store content.Store, metadataRepo metadata.Repository, connectors map[string]registry.Connector, options ...ResolverOption) *PushResolver {
	resolverOptions := resolverOptions{}
	for _, option := range options {
		option(&resolverOptions)
	}
	return &PushResolver{
		policy:     policyEngine,
		store:      store,
		metadata:   metadataRepo,
		connectors: cloneConnectors(connectors),
		authorizer: resolverOptions.authorizer,
		provider:   resolverOptions.provider,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (r *PushResolver) Push(ctx context.Context, request PushRequest) (PushResult, error) {
	started := r.now()
	decision, err := r.policy.ResolvePush(policy.PushRequest{Repository: request.Repository, Reference: request.Reference})
	if err != nil {
		_ = r.recordRejectedPush(ctx, started, request, err)
		return PushResult{}, err
	}
	if !r.authorized(request.Principal, metadata.OperationPush, decision.RouteName) {
		if err := r.recordUnauthorizedPush(ctx, started, request, decision); err != nil {
			return PushResult{}, err
		}
		return PushResult{}, fmt.Errorf("%w: client %q cannot push route %q", ErrUnauthorized, request.Principal.EventIdentity(), decision.RouteName)
	}

	destination := r.connectors[decision.Destination]
	credentialSource := "anonymous"
	if r.provider != nil {
		destination, credentialSource, err = r.provider.ConnectorFor(ctx, request.Principal, decision.Destination, metadata.OperationPush)
		if err != nil {
			errorClass := "credential_unavailable"
			if errors.Is(err, registry.ErrCredentialRequired) {
				errorClass = "credential_required"
			} else if errors.Is(err, registry.ErrAuthorization) {
				errorClass = "upstream_authorization_failed"
			}
			_ = r.recordFailedPush(ctx, started, request, decision, err, errorClass)
			return PushResult{}, err
		}
	}
	if destination == nil {
		err := fmt.Errorf("%w: missing destination connector %q", ErrSourceUnavailable, decision.Destination)
		_ = r.recordFailedPush(ctx, started, request, decision, err, "destination_unavailable")
		return PushResult{}, err
	}

	if err := r.verifyAndStoreManifest(ctx, request.Manifest); err != nil {
		_ = r.recordFailedPush(ctx, started, request, decision, err, "manifest_invalid")
		return PushResult{}, err
	}

	for _, digest := range request.Manifest.BlobDigests {
		if err := r.publishBlob(ctx, destination, decision.PhysicalRepository, digest); err != nil {
			errorClass := "destination_error"
			if errors.Is(err, ErrStagedBlobMissing) {
				errorClass = "staged_blob_missing"
			}
			if errors.Is(err, registry.ErrAuthentication) {
				errorClass = "upstream_authentication_failed"
			}
			_ = r.recordFailedPush(ctx, started, request, decision, err, errorClass)
			return PushResult{}, err
		}
	}

	desc, err := destination.PutManifest(ctx, decision.PhysicalRepository, decision.Reference, request.Manifest)
	if errors.Is(err, registry.ErrAuthentication) {
		wrapped := fmt.Errorf("%w: %w for destination %s", ErrSourceUnavailable, registry.ErrAuthentication, decision.Destination)
		_ = r.recordFailedPush(ctx, started, request, decision, wrapped, "upstream_authentication_failed")
		return PushResult{}, wrapped
	}
	if errors.Is(err, registry.ErrUnavailable) {
		wrapped := fmt.Errorf("%w: %s", ErrSourceUnavailable, decision.Destination)
		_ = r.recordFailedPush(ctx, started, request, decision, wrapped, "destination_unavailable")
		return PushResult{}, wrapped
	}
	if err != nil {
		_ = r.recordFailedPush(ctx, started, request, decision, err, "destination_error")
		return PushResult{}, err
	}

	result := PushResult{
		Route:              decision.RouteName,
		Destination:        decision.Destination,
		LogicalRepository:  decision.LogicalRepository,
		PhysicalRepository: decision.PhysicalRepository,
		Reference:          decision.Reference,
		ManifestDigest:     desc.Digest,
		Explanation: append(append([]string(nil), decision.Explanation...),
			fmt.Sprintf("published manifest %q to destination %q", desc.Digest, decision.Destination)),
		CredentialSource: credentialSource,
	}
	if err := r.recordSuccessfulPush(ctx, started, request, decision, result); err != nil {
		return PushResult{}, err
	}
	return result, nil
}

func (r *PushResolver) authorized(principal identity.Principal, operation metadata.Operation, route string) bool {
	return r.authorizer == nil || r.authorizer.Authorize(principal, operation, route)
}

func (r *PushResolver) verifyAndStoreManifest(ctx context.Context, manifest registry.Manifest) error {
	if _, err := r.store.PutBlob(ctx, manifest.Digest, bytes.NewReader(manifest.Content)); err != nil {
		if errors.Is(err, content.ErrDigestMismatch) {
			return fmt.Errorf("%w: %v", ErrManifestDigestMismatch, err)
		}
		return err
	}
	return nil
}

func (r *PushResolver) publishBlob(ctx context.Context, destination registry.Connector, repository string, digest string) error {
	rc, err := r.store.OpenBlob(ctx, digest)
	if errors.Is(err, content.ErrBlobNotFound) {
		return fmt.Errorf("%w: %s", ErrStagedBlobMissing, digest)
	}
	if err != nil {
		return err
	}
	defer rc.Close()

	if _, err := destination.PutBlob(ctx, repository, digest, rc); errors.Is(err, registry.ErrAuthentication) {
		return fmt.Errorf("%w: %w for destination %s", ErrSourceUnavailable, registry.ErrAuthentication, destination.Name())
	} else if errors.Is(err, registry.ErrUnavailable) {
		return fmt.Errorf("%w: %s", ErrSourceUnavailable, destination.Name())
	} else if err != nil {
		return err
	}
	return nil
}

func (r *PushResolver) recordSuccessfulPush(ctx context.Context, started time.Time, request PushRequest, decision policy.PushDecision, result PushResult) error {
	now := r.now()
	logicalReference := referenceString(decision.LogicalRepository, decision.Reference)
	if err := r.metadata.RecordProvenance(ctx, metadata.ProvenanceRecord{
		LogicalReference:        logicalReference,
		PhysicalSourceReference: referenceString(decision.PhysicalRepository, decision.Reference),
		RequestedReference:      decision.Reference,
		ResolvedDigest:          result.ManifestDigest,
		Source:                  result.Destination,
		Route:                   result.Route,
		RetrievedAt:             now,
		ValidatedAt:             now,
	}); err != nil {
		return err
	}

	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPush,
		ClientIdentity:      request.Principal.EventIdentity(),
		CredentialSource:    result.CredentialSource,
		LogicalReference:    logicalReference,
		MatchedRoute:        result.Route,
		SourceOrDestination: result.Destination,
		Status:              metadata.StatusSuccess,
		CacheResult:         metadata.CacheBypassed,
		Duration:            now.Sub(started),
		Explanation:         result.Explanation,
	})
}

func (r *PushResolver) recordRejectedPush(ctx context.Context, started time.Time, request PushRequest, cause error) error {
	now := r.now()
	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPush,
		ClientIdentity:      request.Principal.EventIdentity(),
		LogicalReference:    referenceString(request.Repository, request.Reference),
		Status:              metadata.StatusDenied,
		CacheResult:         metadata.CacheBypassed,
		Duration:            now.Sub(started),
		ErrorClassification: "push_denied",
		Explanation:         []string{cause.Error()},
	})
}

func (r *PushResolver) recordFailedPush(ctx context.Context, started time.Time, request PushRequest, decision policy.PushDecision, cause error, errorClass string) error {
	now := r.now()
	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPush,
		ClientIdentity:      request.Principal.EventIdentity(),
		LogicalReference:    referenceString(decision.LogicalRepository, decision.Reference),
		MatchedRoute:        decision.RouteName,
		SourceOrDestination: decision.Destination,
		Status:              metadata.StatusError,
		CacheResult:         metadata.CacheBypassed,
		Duration:            now.Sub(started),
		ErrorClassification: errorClass,
		Explanation:         append(append([]string(nil), decision.Explanation...), cause.Error()),
	})
}

func (r *PushResolver) recordUnauthorizedPush(ctx context.Context, started time.Time, request PushRequest, decision policy.PushDecision) error {
	now := r.now()
	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPush,
		ClientIdentity:      request.Principal.EventIdentity(),
		LogicalReference:    referenceString(decision.LogicalRepository, decision.Reference),
		MatchedRoute:        decision.RouteName,
		SourceOrDestination: decision.Destination,
		Status:              metadata.StatusDenied,
		CacheResult:         metadata.CacheBypassed,
		Duration:            now.Sub(started),
		ErrorClassification: "authorization_denied",
		Explanation:         append(append([]string(nil), decision.Explanation...), fmt.Sprintf("client %q is not authorized for push route %q", request.Principal.EventIdentity(), decision.RouteName)),
	})
}
