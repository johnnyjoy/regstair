package security

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrUnsafeDetail = errors.New("unsafe audit detail")

var allowedAuditDetailKeys = map[string]struct{}{
	"source_id":            {},
	"route":                {},
	"operation":            {},
	"error_classification": {},
	"reason":               {},
	"previous_access":      {},
	"new_access":           {},
	"enabled":              {},
	"username":             {},
	"credential_ref":       {},
	"previous_key_id":      {},
	"new_key_id":           {},
	"credential_count":     {},
}

func ValidateAuditDetails(details map[string]string) error {
	for key, value := range details {
		if _, ok := allowedAuditDetailKeys[key]; !ok {
			return fmt.Errorf("%w: detail key %q is not allowed", ErrUnsafeDetail, key)
		}
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 512 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: detail value for %q is invalid", ErrUnsafeDetail, key)
		}
	}
	return nil
}

type PublicError struct {
	Code    string
	Message string
	cause   error
}

func NewPublicError(code, message string, cause error) *PublicError {
	return &PublicError{Code: code, Message: message, cause: cause}
}

func (e *PublicError) Error() string { return e.Message }
func (e *PublicError) Unwrap() error { return e.cause }
