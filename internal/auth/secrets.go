package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var ErrSecretUnavailable = errors.New("credential secret unavailable")

type SecretKeyring struct {
	activeKeyID string
	keys        map[string][]byte
	random      io.Reader
}

func LoadSecretKeyring(activeKeyID string, keyFiles map[string]string, random io.Reader) (*SecretKeyring, error) {
	keys := make(map[string][]byte, len(keyFiles))
	for id, path := range keyFiles {
		if id == "" || path == "" {
			return nil, fmt.Errorf("credential encryption key id and file path are required")
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read credential encryption key %q: %w", id, err)
		}
		key, err := decodeSecretKey(contents)
		if err != nil {
			return nil, fmt.Errorf("decode credential encryption key %q: %w", id, err)
		}
		keys[id] = key
	}
	return NewSecretKeyring(activeKeyID, keys, random)
}

func decodeSecretKey(contents []byte) ([]byte, error) {
	if len(contents) == 32 {
		return append([]byte(nil), contents...), nil
	}
	encoded := strings.TrimSpace(string(contents))
	for _, encoding := range []*base64.Encoding{base64.StdEncoding.Strict(), base64.RawStdEncoding.Strict()} {
		decoded, err := encoding.DecodeString(encoded)
		if err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("key file must contain 32 raw bytes or one base64-encoded 32-byte key")
}

type secretEnvelope struct {
	Version    int    `json:"v"`
	Algorithm  string `json:"alg"`
	KeyID      string `json:"kid"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func NewSecretKeyring(activeKeyID string, keys map[string][]byte, random io.Reader) (*SecretKeyring, error) {
	if activeKeyID == "" {
		return nil, fmt.Errorf("active credential encryption key id is required")
	}
	cloned := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if id == "" || len(key) != 32 {
			return nil, fmt.Errorf("credential encryption key %q must contain exactly 32 bytes", id)
		}
		cloned[id] = append([]byte(nil), key...)
	}
	if _, ok := cloned[activeKeyID]; !ok {
		return nil, fmt.Errorf("active credential encryption key %q is not loaded", activeKeyID)
	}
	if random == nil {
		random = rand.Reader
	}
	return &SecretKeyring{activeKeyID: activeKeyID, keys: cloned, random: random}, nil
}

func (k *SecretKeyring) Encrypt(recordID, userID, sourceID string, plaintext []byte) (string, error) {
	gcm, err := newGCM(k.keys[k.activeKeyID])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(k.random, nonce); err != nil {
		return "", fmt.Errorf("generate credential encryption nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, secretAssociatedData(recordID, userID, sourceID))
	envelope, err := json.Marshal(secretEnvelope{Version: 1, Algorithm: "AES-256-GCM", KeyID: k.activeKeyID, Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext)})
	if err != nil {
		return "", fmt.Errorf("encode credential envelope: %w", err)
	}
	return string(envelope), nil
}

func (k *SecretKeyring) Decrypt(recordID, userID, sourceID, encoded string) ([]byte, error) {
	var envelope secretEnvelope
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		return nil, ErrSecretUnavailable
	}
	if envelope.Version != 1 || envelope.Algorithm != "AES-256-GCM" {
		return nil, ErrSecretUnavailable
	}
	key, ok := k.keys[envelope.KeyID]
	if !ok {
		return nil, ErrSecretUnavailable
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, ErrSecretUnavailable
	}
	nonce, err := base64.RawStdEncoding.Strict().DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, ErrSecretUnavailable
	}
	ciphertext, err := base64.RawStdEncoding.Strict().DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, ErrSecretUnavailable
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, secretAssociatedData(recordID, userID, sourceID))
	if err != nil {
		return nil, ErrSecretUnavailable
	}
	return plaintext, nil
}

func (k *SecretKeyring) Reencrypt(recordID, userID, sourceID, encoded string) (string, error) {
	plaintext, err := k.Decrypt(recordID, userID, sourceID, encoded)
	if err != nil {
		return "", err
	}
	defer clearSecretBytes(plaintext)
	return k.Encrypt(recordID, userID, sourceID, plaintext)
}

func clearSecretBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func secretAssociatedData(recordID, userID, sourceID string) []byte {
	data, _ := json.Marshal([]string{"regstair-credential-v1", recordID, userID, sourceID})
	return data
}
