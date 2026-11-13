package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	
	"github.com/google/uuid"

	"bbr/internal/printers"
	"bbr/internal/users"
)

// -- Admin --

// RequireAdminToken rejects requests that don't carry "Authorization: Bearer <token>".
// Placeholder until real admin authentication (session/JWT with a role) exists.
func RequireAdminToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := bearerTokenFromHeader(r)
			if !ok || token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// -- Printer --

type printerAuthenticator interface {
	Authenticate(ctx context.Context, token string) (uuid.UUID, error)
}

type printerIDKey struct{}

// RequirePrinterToken rejects requests that don't carry "Authorization: Bearer <token>".
func RequirePrinterToken(auth printerAuthenticator, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerTokenFromHeader(r)
			if !ok {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			printerID, err := auth.Authenticate(r.Context(), token)
			if errors.Is(err, printers.ErrInvalidToken) {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if err != nil {
				logger.Error("failed to authenticate printer token", "error", err)
				writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
				return
			}
			ctx := context.WithValue(r.Context(), printerIDKey{}, printerID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func printerIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	printerID, ok := ctx.Value(printerIDKey{}).(uuid.UUID)
	return printerID, ok
}


// -- User --

type userAuthenticator interface {
	Authenticate(ctx context.Context, token string) (uuid.UUID, uuid.UUID, error)
}

type userIDKey struct{}
type sessionIDKey struct{}

// RequireUserToken rejects requests that don't carry "Authorization: Bearer <token>".
func RequireUserToken(auth userAuthenticator, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerTokenFromHeader(r)
			if !ok {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			userID, sessionID, err := auth.Authenticate(r.Context(), token)
			if errors.Is(err, users.ErrInvalidToken) {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if err != nil {
				logger.Error("failed to authenticate user token", "error", err)
				writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey{}, userID)
			ctx = context.WithValue(ctx, sessionIDKey{}, sessionID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func userIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	userID, ok := ctx.Value(userIDKey{}).(uuid.UUID)
	return userID, ok
}

func sessionIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	sessionID, ok := ctx.Value(sessionIDKey{}).(uuid.UUID)
	return sessionID, ok
}