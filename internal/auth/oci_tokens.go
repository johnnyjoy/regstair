package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"regstair/internal/identity"
)

var ErrInvalidAccessToken = errors.New("invalid OCI access token")

type ociClaims struct {
	UserID     string   `json:"sub,omitempty"`
	Username   string   `json:"username,omitempty"`
	TokenHash  string   `json:"token_hash,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	ExpiresAt  int64    `json:"exp"`
}

type OCITokenService struct {
	docker *DockerTokenService
	key    []byte
	now    func() time.Time
}

func NewOCITokenService(docker *DockerTokenService, key []byte, now func() time.Time) (*OCITokenService, error) {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &OCITokenService{docker: docker, key: append([]byte(nil), key...), now: now}, nil
}

func (s *OCITokenService) Issue(ctx context.Context, username, secret, repository string, actions []string) (string, time.Time, error) {
	claims := ociClaims{Repository: repository, Actions: normalizedActions(actions), ExpiresAt: s.now().Add(5 * time.Minute).Unix()}
	if username != "" || secret != "" {
		user, token, err := s.docker.AuthenticateWithToken(ctx, username, secret)
		if err != nil {
			return "", time.Time{}, err
		}
		claims.UserID, claims.Username = user.ID, user.Username
		claims.TokenHash = base64.RawURLEncoding.EncodeToString(token.TokenHash)
	} else if containsAction(claims.Actions, "push") {
		return "", time.Time{}, ErrInvalidCredentials
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), time.Unix(claims.ExpiresAt, 0).UTC(), nil
}

func (s *OCITokenService) Authenticate(ctx context.Context, raw, repository, action string) (identity.Principal, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	var claims ociClaims
	if json.Unmarshal(payload, &claims) != nil || !s.now().Before(time.Unix(claims.ExpiresAt, 0)) || claims.Repository != repository || (action != "" && !containsAction(claims.Actions, action)) {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	if claims.UserID == "" {
		if action != "" && action != "pull" {
			return identity.Principal{}, ErrInvalidAccessToken
		}
		return identity.Anonymous(), nil
	}
	hash, err := base64.RawURLEncoding.DecodeString(claims.TokenHash)
	if err != nil {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	user, err := s.docker.ValidateHash(ctx, claims.UserID, hash)
	if err != nil {
		return identity.Principal{}, ErrInvalidAccessToken
	}
	return identity.Principal{Kind: identity.KindLocalUser, ID: user.ID, Username: user.Username}, nil
}

func normalizedActions(actions []string) []string {
	result := make([]string, 0, 2)
	for _, action := range actions {
		if (action == "pull" || action == "push") && !containsAction(result, action) {
			result = append(result, action)
		}
	}
	return result
}

func containsAction(actions []string, wanted string) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}
