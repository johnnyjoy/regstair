package resolution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"regstair/internal/content"
	"regstair/internal/identity"
	"regstair/internal/metadata"
	"regstair/internal/policy"
	"regstair/internal/registry"
)

var (
	ErrResolutionNotFound = errors.New("resolution not found")
	ErrSourceUnavailable  = errors.New("source unavailable")
	ErrUnauthorized       = errors.New("unauthorized")
)

const SourceCache = "local-cache"

type PullRequest struct {
	Repository     string
	Reference      string
	ClientIdentity string
	Principal      identity.Principal
}

type PullResult struct {
	Manifest         registry.Manifest
	Source           string
	Route            string
	FallbackUsed     bool
	Explanation      []string
	CredentialSource string
}

type PullResolver struct {
	policy     *policy.Engine
	store      content.Store
	metadata   metadata.Repository
	connectors map[string]registry.Connector
	provider   ConnectorProvider
	authorizer Authorizer
	now        func() time.Time
}

type Authorizer interface {
	Authorize(principal identity.Principal, operation metadata.Operation, route string) bool
}

type ConnectorProvider interface {
	ConnectorFor(context.Context, identity.Principal, string, metadata.Operation) (registry.Connector, string, error)
	AuthorizeCache(context.Context, identity.Principal, string, metadata.Operation) (string, error)
}

type ResolverOption func(*resolverOptions)

type resolverOptions struct {
	authorizer Authorizer
	provider   ConnectorProvider
}

func WithAuthorizer(authorizer Authorizer) ResolverOption {
	return func(options *resolverOptions) {
		options.authorizer = authorizer
	}
}

func WithConnectorProvider(provider ConnectorProvider) ResolverOption {
	return func(options *resolverOptions) { options.provider = provider }
}

func NewPullResolver(policyEngine *policy.Engine, store content.Store, metadataRepo metadata.Repository, connectors map[string]registry.Connector, options ...ResolverOption) *PullResolver {
	resolverOptions := resolverOptions{}
	for _, option := range options {
		option(&resolverOptions)
	}
	return &PullResolver{
		policy:     policyEngine,
		store:      store,
		metadata:   metadataRepo,
		connectors: cloneConnectors(connectors),
		authorizer: resolverOptions.authorizer,
		provider:   resolverOptions.provider,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (r *PullResolver) Pull(ctx context.Context, request PullRequest) (PullResult, error) {
	started := r.now()
	decision, err := r.policy.ResolvePull(policy.PullRequest{Repository: request.Repository, Reference: request.Reference})
	if err != nil {
		return PullResult{}, err
	}
	if !r.authorized(requestPrincipal(request.ClientIdentity, request.Principal), metadata.OperationPull, decision.RouteName) {
		if err := r.recordUnauthorizedPull(ctx, started, request, decision); err != nil {
			return PullResult{}, err
		}
		return PullResult{}, fmt.Errorf("%w: client %q cannot pull route %q", ErrUnauthorized, request.ClientIdentity, decision.RouteName)
	}

	if result, ok, err := r.tryCache(ctx, started, request, decision); err != nil {
		return PullResult{}, err
	} else if ok {
		return result, nil
	}

	logicalReference := referenceString(decision.LogicalRepository, decision.Reference)
	principal := requestPrincipal(request.ClientIdentity, request.Principal)
	var lastErr error
	for index, sourceID := range decision.CandidateSources {
		connector := r.connectors[sourceID]
		credentialSource := "anonymous"
		if r.provider != nil {
			connector, credentialSource, err = r.provider.ConnectorFor(ctx, principal, sourceID, metadata.OperationPull)
			if err != nil {
				lastErr = err
				if errors.Is(err, registry.ErrCredentialRequired) || errors.Is(err, registry.ErrCredentialUnavailable) || errors.Is(err, registry.ErrAuthentication) || errors.Is(err, registry.ErrAuthorization) {
					break
				}
				continue
			}
		}
		if connector == nil {
			lastErr = fmt.Errorf("%w: missing connector %q", ErrSourceUnavailable, sourceID)
			continue
		}

		manifest, err := connector.ResolveManifest(ctx, decision.PhysicalRepository, decision.Reference)
		if errors.Is(err, registry.ErrAuthentication) {
			lastErr = fmt.Errorf("%w: %w for source %s", ErrSourceUnavailable, registry.ErrAuthentication, sourceID)
			break
		}
		if errors.Is(err, registry.ErrNotFound) {
			lastErr = err
			continue
		}
		if errors.Is(err, registry.ErrUnavailable) {
			lastErr = fmt.Errorf("%w: %s", ErrSourceUnavailable, sourceID)
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}

		if err := r.cacheManifest(ctx, connector, decision.PhysicalRepository, manifest); err != nil {
			return PullResult{}, err
		}

		fallbackUsed := index > 0
		explanation := append([]string(nil), decision.Explanation...)
		explanation = append(explanation, fmt.Sprintf("resolved manifest from source %q", sourceID))
		if fallbackUsed {
			explanation = append(explanation, "fallback source was used")
		}

		result := PullResult{
			Manifest:         manifest,
			Source:           sourceID,
			Route:            decision.RouteName,
			FallbackUsed:     fallbackUsed,
			Explanation:      explanation,
			CredentialSource: credentialSource,
		}

		if err := r.recordSuccessfulPull(ctx, started, request, decision, result); err != nil {
			return PullResult{}, err
		}
		return result, nil
	}

	if err := r.recordFailedPull(ctx, started, request, decision, lastErr); err != nil {
		return PullResult{}, err
	}
	if lastErr != nil && errors.Is(lastErr, ErrSourceUnavailable) {
		return PullResult{}, lastErr
	}
	if lastErr != nil && (errors.Is(lastErr, registry.ErrCredentialRequired) || errors.Is(lastErr, registry.ErrCredentialUnavailable) || errors.Is(lastErr, registry.ErrAuthentication) || errors.Is(lastErr, registry.ErrAuthorization)) {
		return PullResult{}, lastErr
	}
	return PullResult{}, fmt.Errorf("%w: %s", ErrResolutionNotFound, logicalReference)
}

func (r *PullResolver) authorized(principal identity.Principal, operation metadata.Operation, route string) bool {
	return r.authorizer == nil || r.authorizer.Authorize(principal, operation, route)
}

func requestPrincipal(clientIdentity string, principal identity.Principal) identity.Principal {
	if principal.Kind != "" {
		return principal
	}
	if clientIdentity == "" {
		return identity.Anonymous()
	}
	return identity.Principal{Kind: identity.KindConfiguredClient, ID: clientIdentity}
}

func (r *PullResolver) cacheManifest(ctx context.Context, connector registry.Connector, repository string, manifest registry.Manifest) error {
	if _, err := r.store.PutBlob(ctx, manifest.Digest, bytes.NewReader(manifest.Content)); err != nil {
		return fmt.Errorf("cache manifest: %w", err)
	}

	for _, digest := range manifest.BlobDigests {
		rc, _, err := connector.OpenBlob(ctx, repository, digest)
		if err != nil {
			return fmt.Errorf("open source blob %s: %w", digest, err)
		}
		_, putErr := r.store.PutBlob(ctx, digest, rc)
		closeErr := rc.Close()
		if putErr != nil {
			return fmt.Errorf("cache blob %s: %w", digest, putErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close source blob %s: %w", digest, closeErr)
		}
	}

	return nil
}

func (r *PullResolver) tryCache(ctx context.Context, started time.Time, request PullRequest, decision policy.PullDecision) (PullResult, bool, error) {
	cacheSource := ""
	if len(decision.CandidateSources) > 0 {
		cacheSource = decision.CandidateSources[0]
	}
	if isDigestReference(decision.Reference) {
		credentialSource := "anonymous"
		if r.provider != nil && cacheSource != "" {
			var err error
			if credentialSource, err = r.provider.AuthorizeCache(ctx, requestPrincipal(request.ClientIdentity, request.Principal), cacheSource, metadata.OperationPull); err != nil {
				return PullResult{}, false, err
			}
		}
		manifest, ok, err := r.cachedManifestByDigest(ctx, decision.Reference, registry.Descriptor{Digest: decision.Reference})
		if err != nil || !ok {
			return PullResult{}, ok, err
		}
		result := PullResult{
			Manifest:         manifest,
			Source:           SourceCache,
			Route:            decision.RouteName,
			Explanation:      append(append([]string(nil), decision.Explanation...), "served digest reference from local cache"),
			CredentialSource: credentialSource,
		}
		if err := r.recordCacheHit(ctx, started, request, decision, result); err != nil {
			return PullResult{}, false, err
		}
		return result, true, nil
	}

	mapping, err := r.metadata.FindTagMapping(ctx, decision.LogicalRepository, decision.Reference)
	if err != nil {
		return PullResult{}, false, err
	}
	if mapping == nil || !mappingIsFresh(*mapping, r.now()) {
		return PullResult{}, false, nil
	}
	credentialSource := "anonymous"
	if r.provider != nil {
		var err error
		if credentialSource, err = r.provider.AuthorizeCache(ctx, requestPrincipal(request.ClientIdentity, request.Principal), mapping.Source, metadata.OperationPull); err != nil {
			return PullResult{}, false, err
		}
	}

	manifest, ok, err := r.cachedManifestByDigest(ctx, mapping.Digest, registry.Descriptor{
		MediaType: mapping.MediaType,
		Digest:    mapping.Digest,
		Size:      mapping.Size,
	})
	if err != nil || !ok {
		return PullResult{}, ok, err
	}
	manifest.BlobDigests = append([]string(nil), mapping.BlobDigests...)

	result := PullResult{
		Manifest:         manifest,
		Source:           SourceCache,
		Route:            decision.RouteName,
		Explanation:      append(append([]string(nil), decision.Explanation...), "served tag reference from fresh local cache mapping"),
		CredentialSource: credentialSource,
	}
	if err := r.recordCacheHit(ctx, started, request, decision, result); err != nil {
		return PullResult{}, false, err
	}
	return result, true, nil
}

func (r *PullResolver) cachedManifestByDigest(ctx context.Context, digest string, descriptor registry.Descriptor) (registry.Manifest, bool, error) {
	rc, err := r.store.OpenBlob(ctx, digest)
	if errors.Is(err, content.ErrBlobNotFound) {
		return registry.Manifest{}, false, nil
	}
	if err != nil {
		return registry.Manifest{}, false, err
	}
	defer rc.Close()

	body, err := ioReadAll(rc)
	if err != nil {
		return registry.Manifest{}, false, err
	}
	if descriptor.Size == 0 {
		descriptor.Size = int64(len(body))
	}
	parsed, err := registry.ParseManifest(body)
	if err == nil {
		if descriptor.MediaType == "" {
			descriptor.MediaType = parsed.MediaType
		}
		return registry.Manifest{
			Descriptor:  descriptor,
			Content:     body,
			BlobDigests: parsed.BlobDigests,
		}, true, nil
	}
	return registry.Manifest{
		Descriptor: descriptor,
		Content:    body,
	}, true, nil
}

func (r *PullResolver) recordSuccessfulPull(ctx context.Context, started time.Time, request PullRequest, decision policy.PullDecision, result PullResult) error {
	now := r.now()
	logicalReference := referenceString(decision.LogicalRepository, decision.Reference)
	if err := r.metadata.RecordProvenance(ctx, metadata.ProvenanceRecord{
		LogicalReference:        logicalReference,
		PhysicalSourceReference: referenceString(decision.PhysicalRepository, decision.Reference),
		RequestedReference:      decision.Reference,
		ResolvedDigest:          result.Manifest.Digest,
		Source:                  result.Source,
		Route:                   result.Route,
		FallbackUsed:            result.FallbackUsed,
		RetrievedAt:             now,
		ValidatedAt:             now,
	}); err != nil {
		return err
	}

	if !isDigestReference(decision.Reference) {
		if err := r.metadata.RecordTagMapping(ctx, metadata.TagMapping{
			LogicalRepository: decision.LogicalRepository,
			Tag:               decision.Reference,
			Digest:            result.Manifest.Digest,
			MediaType:         result.Manifest.MediaType,
			Size:              result.Manifest.Size,
			BlobDigests:       result.Manifest.BlobDigests,
			Source:            result.Source,
			Route:             result.Route,
			ResolvedAt:        now,
			LastValidatedAt:   now,
			FreshUntil:        now.Add(24 * time.Hour),
		}); err != nil {
			return err
		}
	}

	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPull,
		ClientIdentity:      request.ClientIdentity,
		CredentialSource:    result.CredentialSource,
		LogicalReference:    logicalReference,
		MatchedRoute:        result.Route,
		SourceOrDestination: result.Source,
		Status:              metadata.StatusSuccess,
		CacheResult:         metadata.CacheMiss,
		Duration:            now.Sub(started),
		BytesTransferred:    manifestAndBlobBytes(result.Manifest),
		Explanation:         result.Explanation,
	})
}

func (r *PullResolver) recordCacheHit(ctx context.Context, started time.Time, request PullRequest, decision policy.PullDecision, result PullResult) error {
	now := r.now()
	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPull,
		ClientIdentity:      request.ClientIdentity,
		CredentialSource:    result.CredentialSource,
		LogicalReference:    referenceString(decision.LogicalRepository, decision.Reference),
		MatchedRoute:        result.Route,
		SourceOrDestination: result.Source,
		Status:              metadata.StatusSuccess,
		CacheResult:         metadata.CacheHit,
		Duration:            now.Sub(started),
		BytesTransferred:    result.Manifest.Size,
		Explanation:         result.Explanation,
	})
}

func (r *PullResolver) recordFailedPull(ctx context.Context, started time.Time, request PullRequest, decision policy.PullDecision, cause error) error {
	now := r.now()
	status := metadata.StatusError
	errorClass := "not_found"
	if decision.AuthoritativeSource != "" && !decision.ExternalFallbackAllowed {
		status = metadata.StatusDenied
		errorClass = "fallback_blocked"
	}
	if cause != nil && errors.Is(cause, ErrSourceUnavailable) {
		errorClass = "source_unavailable"
	}
	if cause != nil && errors.Is(cause, registry.ErrAuthentication) {
		errorClass = "upstream_authentication_failed"
	}
	if cause != nil && errors.Is(cause, registry.ErrCredentialRequired) {
		errorClass = "credential_required"
	}
	if cause != nil && errors.Is(cause, registry.ErrCredentialUnavailable) {
		errorClass = "credential_unavailable"
	}
	if cause != nil && errors.Is(cause, registry.ErrAuthorization) {
		errorClass = "upstream_authorization_failed"
	}

	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPull,
		ClientIdentity:      request.ClientIdentity,
		LogicalReference:    referenceString(decision.LogicalRepository, decision.Reference),
		MatchedRoute:        decision.RouteName,
		Status:              status,
		CacheResult:         metadata.CacheMiss,
		Duration:            now.Sub(started),
		ErrorClassification: errorClass,
		Explanation:         decision.Explanation,
	})
}

func (r *PullResolver) recordUnauthorizedPull(ctx context.Context, started time.Time, request PullRequest, decision policy.PullDecision) error {
	now := r.now()
	return r.metadata.RecordRequestEvent(ctx, metadata.RequestEvent{
		Timestamp:           now,
		Operation:           metadata.OperationPull,
		ClientIdentity:      request.ClientIdentity,
		LogicalReference:    referenceString(decision.LogicalRepository, decision.Reference),
		MatchedRoute:        decision.RouteName,
		Status:              metadata.StatusDenied,
		CacheResult:         metadata.CacheMiss,
		Duration:            now.Sub(started),
		ErrorClassification: "authorization_denied",
		Explanation:         append(append([]string(nil), decision.Explanation...), fmt.Sprintf("client %q is not authorized for pull route %q", request.ClientIdentity, decision.RouteName)),
	})
}

func manifestAndBlobBytes(manifest registry.Manifest) int64 {
	return manifest.Size
}

func referenceString(repository string, reference string) string {
	if isDigestReference(reference) {
		return repository + "@" + reference
	}
	return repository + ":" + reference
}

func isDigestReference(reference string) bool {
	return strings.HasPrefix(reference, "sha256:")
}

func mappingIsFresh(mapping metadata.TagMapping, now time.Time) bool {
	return mapping.FreshUntil.IsZero() || mapping.FreshUntil.After(now) || mapping.FreshUntil.Equal(now)
}

func cloneConnectors(connectors map[string]registry.Connector) map[string]registry.Connector {
	cloned := make(map[string]registry.Connector, len(connectors))
	for key, connector := range connectors {
		cloned[key] = connector
	}
	return cloned
}

func ioReadAll(reader io.Reader) ([]byte, error) {
	return io.ReadAll(reader)
}
