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

func bearerTokenFromHeader(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	bearer, token, ok := strings.Cut(authHeader, " ")
	if !ok || !strings.EqualFold("Bearer", bearer) {
		return "", false
	}
	return token, true
}

func parsePositiveInt(s string) (int, error) {
	value, err := strconv.Atoi(s)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer: %s", s)
	}
	return value, nil
}

func parseNonNegativeInt(s string) (int, error) {
	value, err := strconv.Atoi(s)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid non-negative integer: %s", s)
	}
	return value, nil
}

func parsePaginationParams(r *http.Request) (limit, offset int) {
	limit = 10
	offset = 0

	query := r.URL.Query()

	if l := query.Get("limit"); l != "" {
		if parsedLimit, err := parsePositiveInt(l); err == nil {
			limit = parsedLimit
		}
	}

	if o := query.Get("offset"); o != "" {
		if parsedOffset, err := parseNonNegativeInt(o); err == nil {
			offset = parsedOffset
		}
	}

	return limit, offset
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
