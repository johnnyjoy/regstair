package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPasswordHasherHashesVerifiesAndUsesUniqueSalts(t *testing.T) {
	random := append(bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)...)
	hasher := NewPasswordHasher(DefaultPasswordParams, bytes.NewReader(random))
	first, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	second, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("second Hash() error = %v", err)
	}
	if first == second {
		t.Fatal("two hashes are equal, want unique salts")
	}
	valid, needsRehash, err := hasher.Verify(first, "correct horse battery staple")
	if err != nil || !valid || needsRehash {
		t.Fatalf("Verify() = %v, %v, %v", valid, needsRehash, err)
	}
	valid, _, err = hasher.Verify(first, "incorrect horse battery staple")
	if err != nil || valid {
		t.Fatalf("Verify(wrong) = %v, %v", valid, err)
	}
}

func TestPasswordHasherDetectsParameterUpgrade(t *testing.T) {
	oldParams := DefaultPasswordParams
	oldParams.Memory = 12 * 1024
	encoded, err := NewPasswordHasher(oldParams, bytes.NewReader(bytes.Repeat([]byte{2}, 32))).Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	valid, needsRehash, err := NewPasswordHasher(DefaultPasswordParams, nil).Verify(encoded, "correct horse battery staple")
	if err != nil || !valid || !needsRehash {
		t.Fatalf("Verify() = %v, %v, %v", valid, needsRehash, err)
	}
}

func TestPasswordHasherEnforcesLengthAndRejectsMalformedHashes(t *testing.T) {
	hasher := NewPasswordHasher(DefaultPasswordParams, nil)
	for _, password := range []string{strings.Repeat("a", 14), strings.Repeat("a", 129), string([]byte{0xff, 0xfe})} {
		if _, err := hasher.Hash(password); !errors.Is(err, ErrPasswordPolicy) {
			t.Fatalf("Hash(%d chars) error = %v, want ErrPasswordPolicy", len(password), err)
		}
	}
	if _, _, err := hasher.Verify("not-a-hash", "correct horse battery staple"); err == nil {
		t.Fatal("Verify(malformed) error = nil")
	}
}
