package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireAdminTokens rejects requests that don't carry "Authorization: Bearer <token>".
// Placeholder until real admin authentication (session/JWT with a role) exists.

func RequireAdminToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}