package security

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverHTTPDoesNotLeakPanicOrRequestSecrets(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := RecoverHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("PANIC-CANARY")
	}), logger)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/users?token=QUERY-CANARY", strings.NewReader("BODY-CANARY"))
	request.Header.Set("Authorization", "Bearer HEADER-CANARY")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	combined := logs.String() + response.Body.String()
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "internal_error") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"PANIC-CANARY", "QUERY-CANARY", "BODY-CANARY", "HEADER-CANARY", "Authorization"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("panic boundary leaked %q: %s", secret, combined)
		}
	}
	for _, diagnostic := range []string{`"method":"POST"`, `"surface":"admin"`} {
		if !strings.Contains(logs.String(), diagnostic) {
			t.Fatalf("panic log missing %s: %s", diagnostic, logs.String())
		}
	}
}
