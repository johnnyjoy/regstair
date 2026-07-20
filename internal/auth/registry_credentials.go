package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"regstair/internal/config"
	"regstair/internal/metadata"
	"regstair/internal/security"
)

var (
	ErrUpstreamCredentials = errors.New("upstream credentials rejected")
	ErrUpstreamPermission  = errors.New("upstream permission insufficient")
	ErrUpstreamUnavailable = errors.New("upstream registry unavailable")
	ErrVerificationConfig  = errors.New("credential verification configuration invalid")
	ErrUpstreamFailure     = errors.New("upstream registry verification failed")
)

const (
	VerificationInvalidCredentials     = "invalid_credentials"
	VerificationInsufficientPermission = "insufficient_permission"
	VerificationRegistryUnavailable    = "registry_unavailable"
	VerificationConfigurationInvalid   = "verification_configuration_invalid"
	VerificationRegistryFailure        = "registry_failure"
)

type VerificationRequest struct {
	SourceID, Endpoint, Repository, Username string
	Secret                                   []byte
	TokenHosts                               []string
	Pull, Push                               bool
}

type CredentialVerifier interface {
	Verify(context.Context, VerificationRequest) error
}

type RegistryCredentialView struct {
	ID        string    `json:"id"`
	SourceID  string    `json:"source_id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"ctime"`
	UpdatedAt time.Time `json:"mtime"`
}

type RegistryCredentialService struct {
	repo     securityRepository
	keyring  *SecretKeyring
	verifier CredentialVerifier
	sources  map[string]config.Source
}

func NewRegistryCredentialService(repo securityRepository, keyring *SecretKeyring, verifier CredentialVerifier, sources []config.Source) *RegistryCredentialService {
	byID := make(map[string]config.Source, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	return &RegistryCredentialService{repo: repo, keyring: keyring, verifier: verifier, sources: byID}
}

func (s *RegistryCredentialService) VerifyAndSave(ctx context.Context, userID, sourceID, username string, secret []byte) (*RegistryCredentialView, error) {
	source, ok := s.sources[sourceID]
	if !ok || !source.Enabled || !source.UserCredentials.Approved {
		return nil, publicVerificationError(ErrVerificationConfig)
	}
	if username == "" || len(secret) == 0 {
		return nil, publicVerificationError(ErrUpstreamCredentials)
	}
	request := VerificationRequest{SourceID: source.ID, Endpoint: source.Endpoint, Repository: source.UserCredentials.VerificationRepository, Username: username, Secret: secret, TokenHosts: append([]string(nil), source.Auth.TokenHosts...), Pull: source.UserCredentials.Pull, Push: source.UserCredentials.Push}
	if err := s.verifier.Verify(ctx, request); err != nil {
		classified := classifyVerificationError(err)
		_, _ = s.repo.RecordAuditEvent(ctx, metadata.AuditEvent{ActorUserID: userID, ActorRole: "user", Action: "credential.verification_failed", TargetType: "registry_credential", TargetID: sourceID, Outcome: "failure", Details: map[string]string{"source_id": sourceID, "error_classification": classified.Code}})
		return nil, classified
	}
	existing, err := s.repo.FindRegistryCredential(ctx, userID, sourceID)
	if err != nil {
		return nil, err
	}
	id := ""
	action := "credential.created"
	if existing != nil {
		id, action = existing.ID, "credential.replaced"
	} else {
		id, err = randomID(rand.Reader)
		if err != nil {
			return nil, err
		}
	}
	encrypted, err := s.keyring.Encrypt(id, userID, sourceID, secret)
	if err != nil {
		return nil, err
	}
	credential := metadata.RegistryCredential{ID: id, UserID: userID, SourceID: sourceID, Username: username, EncryptedSecret: encrypted}
	saved, err := s.repo.SaveRegistryCredential(ctx, credential, metadata.AuditEvent{ActorUserID: userID, ActorRole: "user", Action: action, TargetType: "registry_credential", TargetID: id, Outcome: "success", Details: map[string]string{"source_id": sourceID}})
	if err != nil {
		return nil, err
	}
	view := credentialView(*saved)
	return &view, nil
}

func (s *RegistryCredentialService) List(ctx context.Context, userID string) ([]RegistryCredentialView, error) {
	credentials, err := s.repo.ListRegistryCredentialsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	views := make([]RegistryCredentialView, 0, len(credentials))
	for _, credential := range credentials {
		views = append(views, credentialView(credential))
	}
	return views, nil
}

func (s *RegistryCredentialService) Remove(ctx context.Context, userID, sourceID string, confirmed bool) error {
	if !confirmed {
		return security.NewPublicError("confirmation_required", "Credential removal requires confirmation.", errors.New("credential removal was not confirmed"))
	}
	source, ok := s.sources[sourceID]
	if !ok || !source.UserCredentials.Approved {
		return publicVerificationError(ErrVerificationConfig)
	}
	credential, err := s.repo.FindRegistryCredential(ctx, userID, sourceID)
	if err != nil {
		return err
	}
	if credential == nil {
		return nil
	}
	audit := metadata.AuditEvent{ActorUserID: userID, ActorRole: "user", Action: "credential.deleted", TargetType: "registry_credential", TargetID: credential.ID, Outcome: "success", Details: map[string]string{"source_id": sourceID}}
	return s.repo.DeleteRegistryCredential(ctx, userID, sourceID, audit)
}

func credentialView(value metadata.RegistryCredential) RegistryCredentialView {
	return RegistryCredentialView{ID: value.ID, SourceID: value.SourceID, Username: value.Username, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func classifyVerificationError(err error) *security.PublicError {
	switch {
	case errors.Is(err, ErrUpstreamCredentials):
		return security.NewPublicError(VerificationInvalidCredentials, "The registry rejected these credentials.", err)
	case errors.Is(err, ErrUpstreamPermission):
		return security.NewPublicError(VerificationInsufficientPermission, "The credentials do not have the required registry permissions.", err)
	case errors.Is(err, ErrUpstreamUnavailable):
		return security.NewPublicError(VerificationRegistryUnavailable, "The registry is currently unavailable.", err)
	case errors.Is(err, ErrVerificationConfig):
		return security.NewPublicError(VerificationConfigurationInvalid, "Registry credential verification is not configured correctly.", err)
	default:
		return security.NewPublicError(VerificationRegistryFailure, "The registry could not verify these credentials.", fmt.Errorf("%w: %v", ErrUpstreamFailure, err))
	}
}

func publicVerificationError(err error) *security.PublicError { return classifyVerificationError(err) }
