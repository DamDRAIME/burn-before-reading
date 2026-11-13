package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func parseUUIDParam(w http.ResponseWriter, r *http.Request, param string, errMsg string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, errMsg)
		return uuid.Nil, false
	}
	return id, true
}

func requirePrinterID(w http.ResponseWriter, r *http.Request, logger *slog.Logger) (uuid.UUID, bool) {
	id, ok := printerIDFromContext(r.Context())
	if !ok {
		logger.Error("printer ID missing from request context. Middleware may not be applied.")
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
		return uuid.Nil, false
	}
	return id, true
}

func requireUserID(w http.ResponseWriter, r *http.Request, logger *slog.Logger) (uuid.UUID, bool) {
	id, ok := userIDFromContext(r.Context())
	if !ok {
		logger.Error("user ID missing from request context. Middleware may not be applied.")
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
		return uuid.Nil, false
	}
	return id, true
}

func bearerTokenFromHeader(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	bearer, token, ok := strings.Cut(authHeader, " ")
	if !ok || !strings.EqualFold("Bearer", bearer) {
		return "", false
	}
	return token, true
}

const (
	// DefaultPageLimit is the page size used when ?limit is omitted.
	DefaultPageLimit = 10
	// MaxPageLimit is the largest accepted ?limit.
	MaxPageLimit = 10
)

// parsePaginationParams reads ?limit (1..MaxPageLimit, default DefaultPageLimit) and ?offset (>= 0, default 0).
// Like the other query parameters (status, order), an invalid value is rejected with 400 instead of being ignored.
// It writes the error response itself and returns ok=false in that case.
func parsePaginationParams(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit, offset = DefaultPageLimit, 0
	query := r.URL.Query()

	if l, present := query["limit"]; present {
		v, err := strconv.Atoi(l[0])
		if err != nil || v < 1 || v > MaxPageLimit {
			writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid limit: must be an integer between 1 and %d", MaxPageLimit))
			return 0, 0, false
		}
		limit = v
	}

	if o, present := query["offset"]; present {
		v, err := strconv.Atoi(o[0])
		if err != nil || v < 0 {
			writeErrorResponse(w, http.StatusBadRequest, "invalid offset: must be a non-negative integer")
			return 0, 0, false
		}
		offset = v
	}

	return limit, offset, true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err // io.EOF when the body is empty
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}
