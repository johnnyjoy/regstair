package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"regstair/internal/metadata"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSession     = errors.New("invalid session")
	ErrInvalidCSRF        = errors.New("invalid CSRF token")
	ErrForbidden          = errors.New("forbidden")
	ErrLastAdministrator  = errors.New("last enabled administrator cannot be disabled or demoted")
)

type accountRepository interface {
	BootstrapAdmin(context.Context, metadata.User, metadata.AuditEvent) (*metadata.User, error)
	FindUserByID(context.Context, string) (*metadata.User, error)
	FindUserByUsername(context.Context, string) (*metadata.User, error)
	ListUsers(context.Context) ([]metadata.User, error)
	UpdateUser(context.Context, metadata.User, time.Time) (*metadata.User, error)
	ChangeUserPassword(context.Context, string, time.Time, string, metadata.AuditEvent) (*metadata.User, error)
}

func (s *AccountService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || !user.Enabled {
		return ErrInvalidCredentials
	}
	valid, _, err := s.hasher.Verify(user.PasswordHash, currentPassword)
	if err != nil || !valid {
		return ErrInvalidCredentials
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	audit := metadata.AuditEvent{ActorUserID: user.ID, ActorRole: "user", Action: "user.password_changed", TargetType: "user", TargetID: user.ID, Outcome: "success"}
	updated, err := s.repo.ChangeUserPassword(ctx, user.ID, user.UpdatedAt, hash, audit)
	if err != nil {
		return err
	}
	if updated == nil {
		return ErrInvalidCredentials
	}
	return nil
}

type AccountService struct {
	repo   accountRepository
	hasher *PasswordHasher
}

func NewAccountService(repo accountRepository, hasher *PasswordHasher) *AccountService {
	return &AccountService{repo: repo, hasher: hasher}
}

func (s *AccountService) BootstrapAdmin(ctx context.Context, username, password string) (*metadata.User, error) {
	return s.BootstrapAdminWithProfile(ctx, username, password, "", "")
}

func (s *AccountService) BootstrapAdminWithProfile(ctx context.Context, username, password, displayName, email string) (*metadata.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	id, err := randomID(rand.Reader)
	if err != nil {
		return nil, err
	}
	user := metadata.User{ID: id, Username: username, DisplayName: strings.TrimSpace(displayName), Email: strings.TrimSpace(email), PasswordHash: hash, Access: metadata.UserAccessAdmin, Enabled: true}
	audit := metadata.AuditEvent{ActorRole: "system", Action: "user.bootstrap", TargetType: "user", TargetID: id, Outcome: "success"}
	return s.repo.BootstrapAdmin(ctx, user, audit)
}

func (s *AccountService) AuthenticateWeb(ctx context.Context, username, password string) (*metadata.User, error) {
	user, err := s.repo.FindUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}
	valid, _, err := s.hasher.Verify(user.PasswordHash, password)
	if err != nil || !valid || !user.Enabled {
		return nil, ErrInvalidCredentials
	}
	return user, nil
}

type securityRepository interface {
	accountRepository
	metadata.SecurityRepository
}

type IssuedDockerToken struct {
	Token  metadata.DockerToken `json:"token"`
	Secret string               `json:"secret"`
}

type DockerTokenService struct {
	repo   securityRepository
	now    func() time.Time
	random io.Reader
}

func NewDockerTokenService(repo securityRepository, now func() time.Time, random io.Reader) *DockerTokenService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if random == nil {
		random = rand.Reader
	}
	return &DockerTokenService{repo: repo, now: now, random: random}
}

func (s *DockerTokenService) Issue(ctx context.Context, userID, label string, lifetime time.Duration) (*IssuedDockerToken, error) {
	if lifetime <= 0 || lifetime > 90*24*time.Hour {
		return nil, fmt.Errorf("token lifetime must be between one second and 90 days")
	}
	id, err := randomID(s.random)
	if err != nil {
		return nil, err
	}
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.random, secretBytes); err != nil {
		return nil, fmt.Errorf("generate docker token: %w", err)
	}
	secret := "rst_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(secret))
	audit := metadata.AuditEvent{ActorUserID: userID, ActorRole: "user", Action: "docker_token.created", TargetType: "docker_token", TargetID: id, Outcome: "success"}
	token, err := s.repo.CreateDockerTokenWithAudit(ctx, metadata.DockerToken{ID: id, UserID: userID, Label: strings.TrimSpace(label), TokenHash: hash[:], ExpiresAt: s.now().Add(lifetime)}, audit)
	if err != nil {
		return nil, err
	}
	token.TokenHash = nil
	return &IssuedDockerToken{Token: *token, Secret: secret}, nil
}

func (s *DockerTokenService) Authenticate(ctx context.Context, username, secret string) (*metadata.User, error) {
	user, _, err := s.AuthenticateWithToken(ctx, username, secret)
	return user, err
}

func (s *DockerTokenService) AuthenticateWithToken(ctx context.Context, username, secret string) (*metadata.User, *metadata.DockerToken, error) {
	user, err := s.repo.FindUserByUsername(ctx, username)
	if err != nil {
		return nil, nil, err
	}
	hash := sha256.Sum256([]byte(secret))
	token, err := s.repo.FindDockerTokenByHash(ctx, hash[:])
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	if user == nil || !user.Enabled || token == nil || token.UserID != user.ID || !token.RevokedAt.IsZero() || !now.Before(token.ExpiresAt) {
		return nil, nil, ErrInvalidCredentials
	}
	return user, token, nil
}

func (s *DockerTokenService) ValidateHash(ctx context.Context, userID string, hash []byte) (*metadata.User, error) {
	token, err := s.repo.FindDockerTokenByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if user == nil || !user.Enabled || token == nil || token.UserID != user.ID || !token.RevokedAt.IsZero() || !now.Before(token.ExpiresAt) {
		return nil, ErrInvalidCredentials
	}
	return user, nil
}

func (s *DockerTokenService) Revoke(ctx context.Context, id string) error {
	return s.repo.RevokeDockerToken(ctx, id, s.now())
}

func (s *DockerTokenService) List(ctx context.Context, userID string) ([]metadata.DockerToken, error) {
	return s.repo.ListDockerTokensForUser(ctx, userID)
}

func (s *DockerTokenService) RevokeForUser(ctx context.Context, userID, id string) error {
	return s.repo.RevokeDockerTokenWithAudit(ctx, userID, id, s.now(), metadata.AuditEvent{ActorUserID: userID, ActorRole: "user", Action: "docker_token.revoked", TargetType: "docker_token", TargetID: id, Outcome: "success"})
}

type IssuedWebSession struct {
	Secret, CSRFToken string
	Session           metadata.WebSession
}
type WebSessionService struct {
	repo           securityRepository
	now            func() time.Time
	random         io.Reader
	idle, absolute time.Duration
}

func NewWebSessionService(repo securityRepository, now func() time.Time, random io.Reader, idle, absolute time.Duration) *WebSessionService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if random == nil {
		random = rand.Reader
	}
	return &WebSessionService{repo: repo, now: now, random: random, idle: idle, absolute: absolute}
}

func (s *WebSessionService) Create(ctx context.Context, userID string) (*IssuedWebSession, error) {
	id, err := randomID(s.random)
	if err != nil {
		return nil, err
	}
	secret, err := randomSecret(s.random)
	if err != nil {
		return nil, err
	}
	csrf, err := randomSecret(s.random)
	if err != nil {
		return nil, err
	}
	tokenHash, csrfHash := sha256.Sum256([]byte(secret)), sha256.Sum256([]byte(csrf))
	now := s.now()
	session, err := s.repo.CreateWebSession(ctx, metadata.WebSession{ID: id, UserID: userID, TokenHash: tokenHash[:], CSRFTokenHash: csrfHash[:], IdleExpiresAt: now.Add(s.idle), AbsoluteExpiresAt: now.Add(s.absolute)})
	if err != nil {
		return nil, err
	}
	session.TokenHash, session.CSRFTokenHash = nil, nil
	return &IssuedWebSession{Secret: secret, CSRFToken: csrf, Session: *session}, nil
}

func (s *WebSessionService) Validate(ctx context.Context, secret, csrf string) (*metadata.User, error) {
	user, session, err := s.authenticate(ctx, secret)
	if err != nil {
		return nil, err
	}
	csrfHash := sha256.Sum256([]byte(csrf))
	if !equalBytes(csrfHash[:], session.CSRFTokenHash) {
		return nil, ErrInvalidCSRF
	}
	return user, nil
}

func (s *WebSessionService) Authenticate(ctx context.Context, secret string) (*metadata.User, error) {
	user, _, err := s.authenticate(ctx, secret)
	return user, err
}

func (s *WebSessionService) authenticate(ctx context.Context, secret string) (*metadata.User, *metadata.WebSession, error) {
	hash := sha256.Sum256([]byte(secret))
	session, err := s.repo.FindWebSessionByHash(ctx, hash[:])
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	if session == nil || !session.RevokedAt.IsZero() || !now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		return nil, nil, ErrInvalidSession
	}
	user, err := s.repo.FindUserByID(ctx, session.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user == nil || !user.Enabled {
		return nil, nil, ErrInvalidSession
	}
	return user, session, nil
}

func (s *WebSessionService) Revoke(ctx context.Context, secret string) error {
	hash := sha256.Sum256([]byte(secret))
	session, err := s.repo.FindWebSessionByHash(ctx, hash[:])
	if err != nil || session == nil {
		return err
	}
	return s.repo.RevokeWebSession(ctx, session.ID, s.now())
}

func randomID(reader io.Reader) (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(reader, b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
func randomSecret(reader io.Reader) (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(reader, b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

type NewUser struct {
	Username    string              `json:"username"`
	Password    string              `json:"password"`
	DisplayName string              `json:"display_name"`
	Email       string              `json:"email"`
	Access      metadata.UserAccess `json:"access"`
	Enabled     bool                `json:"enabled"`
}

type UserEdit struct {
	Username    *string              `json:"username,omitempty"`
	DisplayName *string              `json:"display_name,omitempty"`
	Email       *string              `json:"email,omitempty"`
	Access      *metadata.UserAccess `json:"access,omitempty"`
	Enabled     *bool                `json:"enabled,omitempty"`
	UpdatedAt   time.Time            `json:"mtime"`
}

type AdminAccountService struct {
	repo   securityRepository
	hasher *PasswordHasher
}

func NewAdminAccountService(repo securityRepository, hasher *PasswordHasher) *AdminAccountService {
	return &AdminAccountService{repo: repo, hasher: hasher}
}

func (s *AdminAccountService) authorize(ctx context.Context, actorID string) error {
	actor, err := s.repo.FindUserByID(ctx, actorID)
	if err != nil {
		return err
	}
	if actor == nil || !actor.Enabled || actor.Access != metadata.UserAccessAdmin {
		return ErrForbidden
	}
	return nil
}

func (s *AdminAccountService) List(ctx context.Context, actorID string) ([]metadata.User, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	return s.repo.ListUsers(ctx)
}

func (s *AdminAccountService) Create(ctx context.Context, actorID string, input NewUser) (*metadata.User, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return nil, err
	}
	id, err := randomID(rand.Reader)
	if err != nil {
		return nil, err
	}
	user := metadata.User{ID: id, Username: strings.TrimSpace(input.Username), PasswordHash: hash, DisplayName: strings.TrimSpace(input.DisplayName), Email: strings.TrimSpace(input.Email), Access: input.Access, Enabled: input.Enabled}
	audit := metadata.AuditEvent{ActorUserID: actorID, ActorRole: "admin", Action: "user.created", TargetType: "user", TargetID: id, Outcome: "success", Details: map[string]string{"username": user.Username, "new_access": string(user.Access), "enabled": strconv.FormatBool(user.Enabled)}}
	return s.repo.CreateUserWithAudit(ctx, user, audit)
}

func (s *AdminAccountService) Update(ctx context.Context, actorID string, user metadata.User, expected time.Time) (*metadata.User, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	current, err := s.repo.FindUserByID(ctx, user.ID)
	if err != nil || current == nil {
		return current, err
	}
	if current.Enabled && current.Access == metadata.UserAccessAdmin && (!user.Enabled || user.Access != metadata.UserAccessAdmin) {
		users, err := s.repo.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		enabledAdmins := 0
		for _, candidate := range users {
			if candidate.Enabled && candidate.Access == metadata.UserAccessAdmin {
				enabledAdmins++
			}
		}
		if enabledAdmins <= 1 {
			return nil, ErrLastAdministrator
		}
	}
	invalidate := current.PasswordHash != user.PasswordHash || current.Username != user.Username || current.Access != user.Access || (current.Enabled && !user.Enabled)
	action := "user.updated"
	if current.Enabled && !user.Enabled {
		action = "user.disabled"
	} else if current.Access != user.Access {
		action = "user.access_changed"
	} else if current.PasswordHash != user.PasswordHash {
		action = "user.password_reset"
	}
	audit := metadata.AuditEvent{ActorUserID: actorID, ActorRole: "admin", Action: action, TargetType: "user", TargetID: user.ID, Outcome: "success", Details: map[string]string{"username": user.Username, "previous_access": string(current.Access), "new_access": string(user.Access), "enabled": strconv.FormatBool(user.Enabled)}}
	return s.repo.UpdateUserSecurity(ctx, user, expected, invalidate, audit)
}

func (s *AdminAccountService) Edit(ctx context.Context, actorID, userID string, input UserEdit) (*metadata.User, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	if input.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("mtime is required")
	}
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil || user == nil {
		return user, err
	}
	if input.Username != nil {
		user.Username = strings.TrimSpace(*input.Username)
	}
	if input.DisplayName != nil {
		user.DisplayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Email != nil {
		user.Email = strings.TrimSpace(*input.Email)
	}
	if input.Access != nil {
		user.Access = *input.Access
	}
	if input.Enabled != nil {
		user.Enabled = *input.Enabled
	}
	return s.Update(ctx, actorID, *user, input.UpdatedAt)
}

func (s *AdminAccountService) ResetPassword(ctx context.Context, actorID, userID, password string) (*metadata.User, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil || user == nil {
		return user, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	user.PasswordHash = hash
	return s.Update(ctx, actorID, *user, user.UpdatedAt)
}
