package metadata

import (
	"context"
	"fmt"
	"time"

	"regstair/internal/security"
)

type DockerToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Label     string    `json:"label"`
	TokenHash []byte    `json:"-"`
	CreatedAt time.Time `json:"ctime"`
	ExpiresAt time.Time `json:"expires_at"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
	LastUsed  time.Time `json:"last_used_at,omitempty"`
}

type WebSession struct {
	ID                string    `json:"id"`
	UserID            string    `json:"user_id"`
	TokenHash         []byte    `json:"-"`
	CSRFTokenHash     []byte    `json:"-"`
	CreatedAt         time.Time `json:"ctime"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	IdleExpiresAt     time.Time `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
	RevokedAt         time.Time `json:"revoked_at,omitempty"`
}

type RegistryCredential struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	SourceID        string    `json:"source_id"`
	Username        string    `json:"username"`
	EncryptedSecret string    `json:"-"`
	CreatedAt       time.Time `json:"ctime"`
	UpdatedAt       time.Time `json:"mtime"`
}

type AuditEvent struct {
	ID            int64             `json:"id"`
	Timestamp     time.Time         `json:"timestamp"`
	ActorUserID   string            `json:"actor_user_id,omitempty"`
	ActorRole     string            `json:"actor_role"`
	Action        string            `json:"action"`
	TargetType    string            `json:"target_type"`
	TargetID      string            `json:"target_id"`
	Outcome       string            `json:"outcome"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	RemoteAddress string            `json:"remote_address,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
}

type SecurityRepository interface {
	CreateUserWithAudit(ctx context.Context, user User, audit AuditEvent) (*User, error)
	UpdateUserSecurity(ctx context.Context, user User, expectedUpdatedAt time.Time, invalidateIdentity bool, audit AuditEvent) (*User, error)
	ChangeUserPassword(ctx context.Context, userID string, expectedUpdatedAt time.Time, passwordHash string, audit AuditEvent) (*User, error)
	CreateDockerToken(ctx context.Context, token DockerToken) (*DockerToken, error)
	CreateDockerTokenWithAudit(ctx context.Context, token DockerToken, audit AuditEvent) (*DockerToken, error)
	FindDockerTokenByHash(ctx context.Context, hash []byte) (*DockerToken, error)
	ListDockerTokensForUser(ctx context.Context, userID string) ([]DockerToken, error)
	RevokeDockerToken(ctx context.Context, id string, revokedAt time.Time) error
	RevokeDockerTokenWithAudit(ctx context.Context, userID, id string, revokedAt time.Time, audit AuditEvent) error
	CreateWebSession(ctx context.Context, session WebSession) (*WebSession, error)
	FindWebSessionByHash(ctx context.Context, hash []byte) (*WebSession, error)
	RevokeWebSession(ctx context.Context, id string, revokedAt time.Time) error
	RevokeWebSessionsForUser(ctx context.Context, userID string, revokedAt time.Time) error
	FindRegistryCredential(ctx context.Context, userID, sourceID string) (*RegistryCredential, error)
	ListRegistryCredentialsForUser(ctx context.Context, userID string) ([]RegistryCredential, error)
	SaveRegistryCredential(ctx context.Context, credential RegistryCredential, audit AuditEvent) (*RegistryCredential, error)
	DeleteRegistryCredential(ctx context.Context, userID, sourceID string, audit AuditEvent) error
	RotateRegistryCredentialSecrets(ctx context.Context, replacements map[string]string, audit AuditEvent) (int, error)
	RecordAuditEvent(ctx context.Context, event AuditEvent) (*AuditEvent, error)
	ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error)
}

func validateDockerToken(token DockerToken) error {
	if token.ID == "" || token.UserID == "" || len(token.TokenHash) != 32 || token.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: invalid docker token", ErrInvalidRecord)
	}
	return nil
}

func validateWebSession(session WebSession) error {
	if session.ID == "" || session.UserID == "" || len(session.TokenHash) != 32 || len(session.CSRFTokenHash) != 32 || session.IdleExpiresAt.IsZero() || session.AbsoluteExpiresAt.IsZero() {
		return fmt.Errorf("%w: invalid web session", ErrInvalidRecord)
	}
	return nil
}

func validateRegistryCredential(credential RegistryCredential) error {
	if credential.ID == "" || credential.UserID == "" || credential.SourceID == "" || credential.Username == "" || credential.EncryptedSecret == "" {
		return fmt.Errorf("%w: invalid registry credential", ErrInvalidRecord)
	}
	return nil
}

func validateAuditEvent(event AuditEvent) error {
	if event.ActorRole == "" || event.Action == "" || event.TargetType == "" || event.TargetID == "" || event.Outcome == "" {
		return fmt.Errorf("%w: invalid audit event", ErrInvalidRecord)
	}
	if err := security.ValidateAuditDetails(event.Details); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	return nil
}
