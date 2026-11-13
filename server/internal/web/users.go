package web

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"bbr/internal/printers"
	"bbr/internal/users"
	"bbr/internal/validate"
)

type UsersHandler struct {
	service *users.Service
	logger  *slog.Logger
}

func NewUsersHandler(service *users.Service, logger *slog.Logger) *UsersHandler {
	return &UsersHandler{service: service, logger: logger}
}

// -- Routes ----

// RegisterPublicRoutes registers routes that need no authentication.
func (h *UsersHandler) RegisterPublicRoutes(r chi.Router) {
	r.Post("/users/claim", h.claimInvite)
}

// RegisterAdminRoutes registers routes mounted under /admin.
func (h *UsersHandler) RegisterAdminRoutes(r chi.Router) {
	r.Post("/users", h.create)
	r.Get("/users", h.list)
	r.Get("/users/{id}", h.get)
	r.Put("/users/{id}/printers", h.setPrinters)
	r.Post("/users/{id}/invite", h.reissueInvite)
	r.Get("/users/{id}/sessions", h.listSessions)
	r.Delete("/users/{id}/sessions", h.revokeAllSessions)
	r.Delete("/users/{id}/sessions/{sessionID}", h.revokeSession)
}

// RegisterUserRoutes registers routes mounted under /user (behind RequireUserToken).
func (h *UsersHandler) RegisterUserRoutes(r chi.Router) {
	r.Get("/me", h.me)
}

// -- Requests ----

type userCreateRequest struct {
	Name       string      `json:"name"`
	PrinterIDs []uuid.UUID `json:"printer_ids"`
}

type userSetPrintersRequest struct {
	// Pointer so a missing field (nil) can be told apart from an explicit [] (remove all access).
	PrinterIDs *[]uuid.UUID `json:"printer_ids"`
}

type userClaimRequest struct {
	Invite     string `json:"invite"`
	DeviceName string `json:"device_name"`
}

// -- Responses ----

type userAdminResponse struct {
	ID            uuid.UUID   `json:"id"`
	Name          string      `json:"name"`
	PrinterIDs    []uuid.UUID `json:"printer_ids"`
	CreatedAt     time.Time   `json:"created_at"`
	LastUpdatedAt time.Time   `json:"last_updated_at"`
}

type userCreateResponse struct {
	userAdminResponse
	Invite string `json:"invite"`
}

type userMeResponse struct {
	ID         uuid.UUID   `json:"id"`
	Name       string      `json:"name"`
	PrinterIDs []uuid.UUID `json:"printer_ids"`
}

type sessionResponse struct {
	ID         uuid.UUID  `json:"id"`
	DeviceName string     `json:"device_name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type userClaimResponse struct {
	Token string `json:"token"`
}

// -- Handlers ----

// -- Public handlers ----

func (h *UsersHandler) claimInvite(w http.ResponseWriter, r *http.Request) {
	var req userClaimRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, err := h.service.ClaimInvite(r.Context(), req.Invite, req.DeviceName)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusOK, userClaimResponse{Token: token})
}

// -- Admin handlers ----

func (h *UsersHandler) create(w http.ResponseWriter, r *http.Request) {
	var req userCreateRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, invite, err := h.service.Create(r.Context(), users.SubmitParams{Name: req.Name, PrinterIDs: req.PrinterIDs})
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusCreated, userCreateResponse{
		userAdminResponse: userAdminResponse{
			ID:            user.ID,
			Name:          user.Name,
			PrinterIDs:    user.PrinterIDs,
			CreatedAt:     user.CreatedAt,
			LastUpdatedAt: user.LastUpdatedAt,
		},
		Invite: invite,
	})
}

func (h *UsersHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	user, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, userAdminResponse{
		ID:            user.ID,
		Name:          user.Name,
		PrinterIDs:    user.PrinterIDs,
		CreatedAt:     user.CreatedAt,
		LastUpdatedAt: user.LastUpdatedAt,
	})
}

func (h *UsersHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}
	users, err := h.service.List(r.Context(), limit, offset)
	if err != nil {
		h.handleError(w, err)
		return
	}
	responses := make([]userAdminResponse, len(users))
	for i, user := range users {
		responses[i] = toUserAdminResponse(user)
	}
	writeJSONResponse(w, http.StatusOK, responses)
}

func (h *UsersHandler) setPrinters(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	var req userSetPrintersRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PrinterIDs == nil {
		writeErrorResponse(w, http.StatusBadRequest, "printer_ids is required (use [] to remove all printers)")
		return
	}
	user, err := h.service.SetPrinters(r.Context(), id, *req.PrinterIDs)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, toUserAdminResponse(user))
}

func (h *UsersHandler) reissueInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	invite, err := h.service.ReissueInvite(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusOK, map[string]string{"invite": invite})
}

func (h *UsersHandler) listSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	sessions, err := h.service.ListSessions(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}
	responses := make([]sessionResponse, len(sessions))
	for i, session := range sessions {
		responses[i] = toSessionResponse(session)
	}
	writeJSONResponse(w, http.StatusOK, responses)
}

func (h *UsersHandler) revokeSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDParam(w, r, "sessionID", "invalid session ID")
	if !ok {
		return
	}
	if err := h.service.RevokeSession(r.Context(), userID, sessionID); err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UsersHandler) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	if err := h.service.RevokeAllSessions(r.Context(), id); err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- User handlers ----

func (h *UsersHandler) me(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r, h.logger)
	if !ok {
		return
	}
	user, err := h.service.Get(r.Context(), userID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, toUserMeResponse(user))
}

// -- Mappers ----

func toUserAdminResponse(u *users.User) userAdminResponse {
	return userAdminResponse{
		ID:            u.ID,
		Name:          u.Name,
		PrinterIDs:    u.PrinterIDs,
		CreatedAt:     u.CreatedAt,
		LastUpdatedAt: u.LastUpdatedAt,
	}
}

func toUserMeResponse(u *users.User) userMeResponse {
	return userMeResponse{
		ID:         u.ID,
		Name:       u.Name,
		PrinterIDs: u.PrinterIDs,
	}
}

func toSessionResponse(s *users.Session) sessionResponse {
	return sessionResponse{
		ID:         s.ID,
		DeviceName: s.DeviceName,
		CreatedAt:  s.CreatedAt,
		LastUsedAt: s.LastUsedAt,
		RevokedAt:  s.RevokedAt,
	}
}

// -- Errors ----

func (h *UsersHandler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrInvalidInvite):
		writeErrorResponse(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, users.ErrUserNotFound),
		errors.Is(err, users.ErrSessionNotFound),
		errors.Is(err, printers.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrUnknownPrinter), // a printer ID in the request body, not the resource in the URL
		errors.Is(err, users.ErrEmptyUserName),
		errors.Is(err, users.ErrUserNameTooLong),
		errors.Is(err, users.ErrEmptyDeviceName),
		errors.Is(err, users.ErrDeviceNameTooLong),
		errors.Is(err, validate.ErrInvalidCharacters):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
