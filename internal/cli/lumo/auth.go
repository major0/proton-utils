package lumoCmd

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/major0/proton-utils/api/lumo"
)

// authMiddleware validates the Authorization: Bearer <token> header using
// constant-time comparison. Returns 401 with an OpenAI-format ErrorResponse
// on mismatch, missing, or malformed header.
func authMiddleware(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			writeAuthError(w)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
			writeAuthError(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeAuthError writes a 401 response with an OpenAI-format error body.
func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(lumo.OAIErrorResponse{
		Error: lumo.OAIErrorBody{
			Message: "Invalid API key",
			Type:    "invalid_api_key",
		},
	})
}
