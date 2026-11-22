package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"bbr/internal/printers"
	"bbr/internal/users"
)

// -- Admin --

// RequireAdmin rejects requests that don't come from a Admin user.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdminFromContext(r.Context()) {
			writeErrorResponse(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAdminFromContext(ctx context.Context) bool {
	isAdmin, _ := ctx.Value(isAdminKey{}).(bool)
	return isAdmin
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
	Authenticate(ctx context.Context, token string) (uuid.UUID, uuid.UUID, bool, error)
}

type userIDKey struct{}
type sessionIDKey struct{}
type isAdminKey struct{}

// RequireUserToken rejects requests that don't carry "Authorization: Bearer <token>".
func RequireUserToken(auth userAuthenticator, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerTokenFromHeader(r)
			if !ok {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			userID, sessionID, isAdmin, err := auth.Authenticate(r.Context(), token)
			if errors.Is(err, users.ErrInvalidSessionToken) {
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
			ctx = context.WithValue(ctx, isAdminKey{}, isAdmin)
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

// -- Owner --

type printerOwnershipChecker interface {
	IsOwner(ctx context.Context, userID, printerID uuid.UUID) (bool, error)
}

func RequirePrinterOwner(checker printerOwnershipChecker, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := userIDFromContext(r.Context())
			if !ok {
				writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
			if !ok {
				return
			}
			isOwner, err := checker.IsOwner(r.Context(), userID, printerID)
			if err != nil {
				logger.Error("failed to check printer ownership", "error", err)
				writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
				return
			}
			if !isOwner {
				writeErrorResponse(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
