package security

import (
	"log/slog"
	"net/http"
	"strings"
)

func RecoverHTTP(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() == nil {
				return
			}
			logger.Error("HTTP request panic recovered", "method", r.Method, "surface", requestSurface(r.URL.Path))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"The request could not be completed."}}`))
		}()
		next.ServeHTTP(w, r)
	})
}

func requestSurface(path string) string {
	switch {
	case strings.HasPrefix(path, "/admin"):
		return "admin"
	case strings.HasPrefix(path, "/v2/") || path == "/v2":
		return "oci"
	case path == "/healthz" || path == "/readyz":
		return "probe"
	default:
		return "other"
	}
}
