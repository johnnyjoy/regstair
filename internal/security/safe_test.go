package security

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAuditDetailsAllowsOnlyKnownSafeFields(t *testing.T) {
	if err := ValidateAuditDetails(map[string]string{"source_id": "harbor", "reason": "user requested replacement"}); err != nil {
		t.Fatalf("ValidateAuditDetails() error = %v", err)
	}
	for _, details := range []map[string]string{
		{"password": "secret"},
		{"token": "secret"},
		{"source_id": "harbor\nauthorization: secret"},
		{"reason": strings.Repeat("x", 513)},
	} {
		if err := ValidateAuditDetails(details); !errors.Is(err, ErrUnsafeDetail) {
			t.Fatalf("ValidateAuditDetails(%v) error = %v", details, err)
		}
	}
}

func TestPublicErrorExposesSafeMessageAndPreservesInternalCause(t *testing.T) {
	cause := errors.New("upstream returned authorization header secret")
	err := NewPublicError("upstream_authentication_failed", "The registry rejected the credential.", cause)
	if err.Error() != "The registry rejected the credential." || strings.Contains(err.Error(), "secret") {
		t.Fatalf("public error = %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("public error does not preserve internal cause")
	}
}
