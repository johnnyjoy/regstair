package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretKeyringEncryptsWithUniqueNoncesAndAssociatedData(t *testing.T) {
	random := append(bytes.Repeat([]byte{1}, 12), bytes.Repeat([]byte{2}, 12)...)
	keyring := newTestKeyring(t, "key-1", map[string][]byte{"key-1": bytes.Repeat([]byte{3}, 32)}, bytes.NewReader(random))
	first, err := keyring.Encrypt("credential-1", "user-1", "harbor", []byte("upstream-secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	second, err := keyring.Encrypt("credential-1", "user-1", "harbor", []byte("upstream-secret"))
	if err != nil {
		t.Fatalf("second Encrypt() error = %v", err)
	}
	if first == second || strings.Contains(first, "upstream-secret") {
		t.Fatalf("encrypted envelopes are equal or expose plaintext")
	}
	plaintext, err := keyring.Decrypt("credential-1", "user-1", "harbor", first)
	if err != nil || string(plaintext) != "upstream-secret" {
		t.Fatalf("Decrypt() = %q, %v", plaintext, err)
	}
	for _, identity := range [][3]string{{"credential-2", "user-1", "harbor"}, {"credential-1", "user-2", "harbor"}, {"credential-1", "user-1", "ghcr"}} {
		if _, err := keyring.Decrypt(identity[0], identity[1], identity[2], first); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("Decrypt(moved envelope) error = %v", err)
		}
	}
}

func TestSecretKeyringRejectsTamperingAndSupportsRotation(t *testing.T) {
	oldKey := bytes.Repeat([]byte{3}, 32)
	newKey := bytes.Repeat([]byte{4}, 32)
	oldRing := newTestKeyring(t, "old", map[string][]byte{"old": oldKey}, bytes.NewReader(bytes.Repeat([]byte{1}, 12)))
	encoded, err := oldRing.Encrypt("credential-1", "user-1", "harbor", []byte("upstream-secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["ciphertext"] = envelope["ciphertext"].(string) + "A"
	tampered, _ := json.Marshal(envelope)
	if _, err := oldRing.Decrypt("credential-1", "user-1", "harbor", string(tampered)); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("Decrypt(tampered) error = %v", err)
	}

	rotating := newTestKeyring(t, "new", map[string][]byte{"old": oldKey, "new": newKey}, bytes.NewReader(bytes.Repeat([]byte{2}, 12)))
	rotated, err := rotating.Reencrypt("credential-1", "user-1", "harbor", encoded)
	if err != nil {
		t.Fatalf("Reencrypt() error = %v", err)
	}
	if !strings.Contains(rotated, `"kid":"new"`) {
		t.Fatalf("rotated envelope = %s", rotated)
	}
	newOnly := newTestKeyring(t, "new", map[string][]byte{"new": newKey}, nil)
	plaintext, err := newOnly.Decrypt("credential-1", "user-1", "harbor", rotated)
	if err != nil || string(plaintext) != "upstream-secret" {
		t.Fatalf("Decrypt(rotated) = %q, %v", plaintext, err)
	}
}

func TestSecretKeyringRequiresValidActiveKey(t *testing.T) {
	if _, err := NewSecretKeyring("missing", map[string][]byte{"other": bytes.Repeat([]byte{1}, 32)}, nil); err == nil {
		t.Fatal("NewSecretKeyring() error = nil for missing active key")
	}
	if _, err := NewSecretKeyring("key", map[string][]byte{"key": bytes.Repeat([]byte{1}, 31)}, nil); err == nil {
		t.Fatal("NewSecretKeyring() error = nil for short key")
	}
}

func TestLoadSecretKeyringReadsRawAndBase64KeyFiles(t *testing.T) {
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "raw.key")
	encodedPath := filepath.Join(dir, "encoded.key")
	if err := os.WriteFile(rawPath, bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)) + "\n"
	if err := os.WriteFile(encodedPath, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	keyring, err := LoadSecretKeyring("new", map[string]string{"old": rawPath, "new": encodedPath}, nil)
	if err != nil {
		t.Fatalf("LoadSecretKeyring() error = %v", err)
	}
	if keyring.activeKeyID != "new" || len(keyring.keys) != 2 {
		t.Fatalf("loaded keyring = %#v", keyring)
	}
}

func TestLoadSecretKeyringRejectsInvalidOrMissingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.key")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecretKeyring("key", map[string]string{"key": path}, nil); err == nil {
		t.Fatal("LoadSecretKeyring(short) error = nil")
	}
	if _, err := LoadSecretKeyring("key", map[string]string{"key": path + ".missing"}, nil); err == nil {
		t.Fatal("LoadSecretKeyring(missing) error = nil")
	}
}

func newTestKeyring(t *testing.T, active string, keys map[string][]byte, random io.Reader) *SecretKeyring {
	t.Helper()
	keyring, err := NewSecretKeyring(active, keys, random)
	if err != nil {
		t.Fatalf("NewSecretKeyring() error = %v", err)
	}
	return keyring
}
