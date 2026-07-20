package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var ErrPasswordPolicy = errors.New("password does not meet policy")

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultPasswordParams = PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}

type PasswordHasher struct {
	params PasswordParams
	random io.Reader
}

func NewPasswordHasher(params PasswordParams, random io.Reader) *PasswordHasher {
	if random == nil {
		random = rand.Reader
	}
	return &PasswordHasher{params: params, random: random}
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	if err := validatePasswordParams(h.params); err != nil {
		return "", err
	}
	salt := make([]byte, h.params.SaltLength)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.params.Memory, h.params.Iterations, h.params.Parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (h *PasswordHasher) Verify(encoded, password string) (valid bool, needsRehash bool, err error) {
	if !utf8.ValidString(password) {
		return false, false, ErrPasswordPolicy
	}
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(actual, expected) == 1
	return valid, valid && params != h.params, nil
}

func validatePassword(password string) error {
	if !utf8.ValidString(password) {
		return fmt.Errorf("%w: password is not valid UTF-8", ErrPasswordPolicy)
	}
	length := utf8.RuneCountInString(password)
	if length < 15 || length > 128 {
		return fmt.Errorf("%w: password must contain 15 to 128 characters", ErrPasswordPolicy)
	}
	return nil
}

func validatePasswordParams(params PasswordParams) error {
	if params.Memory == 0 || params.Iterations == 0 || params.Parallelism == 0 || params.SaltLength < 16 || params.KeyLength < 16 {
		return fmt.Errorf("invalid Argon2id password parameters")
	}
	return nil
}

func parsePasswordHash(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return PasswordParams{}, nil, nil, fmt.Errorf("invalid Argon2id password hash")
	}
	var params PasswordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Iterations, &params.Parallelism); err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("parse Argon2id parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("decode Argon2id salt: %w", err)
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("decode Argon2id key: %w", err)
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(key))
	if err := validatePasswordParams(params); err != nil {
		return PasswordParams{}, nil, nil, err
	}
	return params, salt, key, nil
}
