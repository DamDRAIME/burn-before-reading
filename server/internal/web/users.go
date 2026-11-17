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
	r.Put("/users/{id}/printers/{printerID}/owner", h.setOwner)
	r.Post("/users/{id}/invite", h.reissueInvite)
	r.Get("/users/{id}/sessions", h.listSessions)
	r.Delete("/users/{id}/sessions", h.revokeAllSessions)
	r.Delete("/users/{id}/sessions/{sessionID}", h.revokeSession)
}

// RegisterUserRoutes registers routes mounted under /user (behind RequireUserToken).
func (h *UsersHandler) RegisterUserRoutes(r chi.Router) {
	r.Get("/me", h.me)
}

// RegisterPrinterOwnerRoutes registers routes mounted under /user/printers/{id} (behind RequirePrinterOwner).
func (h *UsersHandler) RegisterPrinterOwnerRoutes(r chi.Router) {
	r.Get("/members", h.listMembers)
	r.Post("/members", h.addMember)
	r.Delete("/members/{userID}", h.removeMember)
}

// -- Requests ----

type userCreateRequest struct {
	Name       string      `json:"name"`
	PrinterIDs []uuid.UUID `json:"printer_ids"`
}

type printerAccessRequest struct {
	PrinterID uuid.UUID  `json:"printer_id"`
	Role      users.Role `json:"role"`
}

type userSetPrintersRequest struct {
	// Pointer so a missing field (nil) can be told apart from an explicit [] (remove all access).
	PrinterAccesses *[]printerAccessRequest `json:"printer_accesses"`
}

type userClaimRequest struct {
	Invite     string `json:"invite"`
	DeviceName string `json:"device_name"`
}

type userAddMemberRequest struct {
	Name string `json:"name"`
}

// -- Responses ----

type printerAccessResponse struct {
	PrinterID uuid.UUID  `json:"printer_id"`
	Role      users.Role `json:"role"`
}

type userAdminResponse struct {
	ID              uuid.UUID               `json:"id"`
	Name            string                  `json:"name"`
	PrinterAccesses []printerAccessResponse `json:"printer_accesses"`
	CreatedAt       time.Time               `json:"created_at"`
	LastUpdatedAt   time.Time               `json:"last_updated_at"`
}

type userCreateResponse struct {
	userAdminResponse
	Invite string `json:"invite"`
}

type userMeResponse struct {
	ID              uuid.UUID               `json:"id"`
	Name            string                  `json:"name"`
	PrinterAccesses []printerAccessResponse `json:"printer_accesses"`
}

type userMemberResponse struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Role string    `json:"role"`
}

type userAddMemberResponse struct {
	userMemberResponse
	Invite string `json:"invite"`
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
		userAdminResponse: toUserAdminResponse(user),
		Invite:            invite,
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
	writeJSONResponse(w, http.StatusOK, toUserAdminResponse(user))
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
	if req.PrinterAccesses == nil {
		writeErrorResponse(w, http.StatusBadRequest, "printer_accesses is required (use [] to remove all printers)")
		return
	}
	accesses := make([]users.PrinterAccess, len(*req.PrinterAccesses))
	for i, pa := range *req.PrinterAccesses {
		accesses[i] = users.PrinterAccess{PrinterID: pa.PrinterID, Role: pa.Role}
	}
	user, err := h.service.SetPrinters(r.Context(), id, accesses)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, toUserAdminResponse(user))
}

func (h *UsersHandler) setOwner(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	printerID, ok := parseUUIDParam(w, r, "printerID", "invalid printer ID")
	if !ok {
		return
	}
	user, err := h.service.SetOwner(r.Context(), userID, printerID)
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

// -- Owner handlers ----

func (h *UsersHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	members, err := h.service.ListMembers(r.Context(), printerID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	responses := make([]userMemberResponse, len(members))
	for i, member := range members {
		responses[i] = toUserMemberResponse(member)
	}
	writeJSONResponse(w, http.StatusOK, responses)
}

func (h *UsersHandler) addMember(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	var req userAddMemberRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	member, invite, err := h.service.AddMember(r.Context(), printerID, req.Name)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusCreated, userAddMemberResponse{
		userMemberResponse: toUserMemberResponse(member),
		Invite:             invite,
	})
}

func (h *UsersHandler) removeMember(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	userID, ok := parseUUIDParam(w, r, "userID", "invalid user ID")
	if !ok {
		return
	}

	err := h.service.RemoveMember(r.Context(), printerID, userID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- Mappers ----

func toPrinterAccessResponse(pa users.PrinterAccess) printerAccessResponse {
	return printerAccessResponse{PrinterID: pa.PrinterID, Role: pa.Role}
}

func toUserAdminResponse(u *users.User) userAdminResponse {
	return userAdminResponse{
		ID:              u.ID,
		Name:            u.Name,
		PrinterAccesses: mapSlice(u.PrinterAccesses, toPrinterAccessResponse),
		CreatedAt:       u.CreatedAt,
		LastUpdatedAt:   u.LastUpdatedAt,
	}
}

func toUserMeResponse(u *users.User) userMeResponse {
	return userMeResponse{
		ID:              u.ID,
		Name:            u.Name,
		PrinterAccesses: mapSlice(u.PrinterAccesses, toPrinterAccessResponse),
	}
}

func toUserMemberResponse(u *users.User) userMemberResponse {
	return userMemberResponse{
		ID:   u.ID,
		Name: u.Name,
		Role: string(users.RoleMember),
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
		errors.Is(err, users.ErrMemberNotFound),
		errors.Is(err, users.ErrSessionNotFound),
		errors.Is(err, printers.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrUnknownPrinter), // a printer ID in the request body, not the resource in the URL
		errors.Is(err, users.ErrEmptyUserName),
		errors.Is(err, users.ErrUserNameTooLong),
		errors.Is(err, users.ErrEmptyDeviceName),
		errors.Is(err, users.ErrDeviceNameTooLong),
		errors.Is(err, users.ErrInvalidRole),
		errors.Is(err, users.ErrConflictingRoles),
		errors.Is(err, validate.ErrInvalidCharacters):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, users.ErrPrinterOwnershipViolation):
		writeErrorResponse(w, http.StatusConflict, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
