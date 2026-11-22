package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
	r.Post("/users/join", h.signupWithJoinToken)
}

// RegisterAdminRoutes registers routes mounted under /admin.
func (h *UsersHandler) RegisterAdminRoutes(r chi.Router) {
	r.Post("/users", h.create)
	r.Get("/users", h.list)
	r.Get("/users/{id}", h.get)
	r.Put("/users/{id}/printers", h.setPrinters)
	r.Post("/users/{id}/invite", h.issueRecoveryInvite)
	r.Get("/users/{id}/sessions", h.listSessions)
	r.Delete("/users/{id}/sessions", h.revokeAllSessions)
	r.Delete("/users/{id}/sessions/{sessionID}", h.revokeSession)

	r.Put("/printers/{id}/owner", h.setOwnership)
	r.Delete("/printers/{id}/owner", h.demoteOwner)
	r.Post("/printers/{id}/join_tokens", h.createJoinToken(false))
	r.Get("/printers/{id}/join_tokens", h.listJoinTokens)
	r.Delete("/printers/{id}/join_tokens/{tokenID}", h.revokeJoinToken)
}

// RegisterUserRoutes registers routes mounted under /user (behind RequireUserToken).
func (h *UsersHandler) RegisterUserRoutes(r chi.Router) {
	r.Get("/me", h.me)
	r.Post("/invite", h.selfIssueInvite)
	r.Get("/sessions", h.listOwnSessions)
	r.Delete("/sessions/{sessionID}", h.revokeOwnSession)
	r.Post("/memberships", h.redeemJoinToken)
	r.Delete("/memberships/{printerID}", h.revokeOwnMembership)
}

// RegisterPrinterOwnerRoutes registers routes mounted under /user/printers/{id} (behind RequirePrinterOwner).
func (h *UsersHandler) RegisterPrinterOwnerRoutes(r chi.Router) {
	r.Get("/members", h.listMembers)
	r.Delete("/members/{userID}", h.revokeMembership)

	r.Post("/join_tokens", h.createJoinToken(true))
	r.Get("/join_tokens", h.listJoinTokens)
	r.Delete("/join_tokens/{tokenID}", h.revokeJoinToken)
}

// -- Requests ----

type userCreateRequest struct {
	UserName   string      `json:"username"`
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

type userIssueRecoveryInviteRequest struct {
	RevokeAllSessions bool `json:"revoke_sessions"`
}

type userClaimRequest struct {
	Invite     string `json:"invite"`
	DeviceName string `json:"device_name"`
}

type userSignupRequest struct {
	UserName   string `json:"username"`
	DeviceName string `json:"device_name"`
	JoinToken  string `json:"join_token"`
}

type userRedeemJoinTokenRequest struct {
	JoinToken string `json:"join_token"`
}

type printerSetOwnershipRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

// -- Responses ----

type printerAccessResponse struct {
	PrinterID uuid.UUID  `json:"printer_id"`
	Role      users.Role `json:"role"`
}

type userAdminResponse struct {
	ID              uuid.UUID               `json:"id"`
	Name            string                  `json:"name"`
	IsAdmin         bool                    `json:"is_admin"`
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
	IsAdmin         bool                    `json:"is_admin"`
	PrinterAccesses []printerAccessResponse `json:"printer_accesses"`
}

type userMemberResponse struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Role string    `json:"role"`
}

type inviteResponse struct {
	Invite    string    `json:"invite"`
	ExpiresAt time.Time `json:"expires_at"`
}

type sessionResponse struct {
	ID         uuid.UUID  `json:"id"`
	DeviceName string     `json:"device_name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type ownSessionResponse struct {
	sessionResponse
	Current bool `json:"current"`
}

type userClaimResponse struct {
	Token string `json:"token"`
}

type joinTokenUserResponse struct {
	ID   uuid.UUID `json:"id"`
	Name *string   `json:"name"`
}

type joinTokenResponse struct {
	ID        uuid.UUID              `json:"id"`
	PrinterID uuid.UUID              `json:"printer_id"`
	CreatedBy *uuid.UUID             `json:"created_by"`
	Status    users.JoinTokenStatus  `json:"status"`
	UsedBy    *joinTokenUserResponse `json:"used_by"`
	CreatedAt time.Time              `json:"created_at"`
	ExpiresAt time.Time              `json:"expires_at"`
	UsedAt    *time.Time             `json:"used_at"`
	RevokedAt *time.Time             `json:"revoked_at"`
}

type joinTokenCreateResponse struct {
	joinTokenResponse
	JoinToken string `json:"join_token"`
}

// -- Handlers ----

// -- Mix Group handlers ----

func (h *UsersHandler) createJoinToken(asOwner bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
		if !ok {
			return
		}
		var createdBy *uuid.UUID
		if asOwner {
			userID, ok := requireUserID(w, r, h.logger)
			if !ok {
				return
			}
			createdBy = &userID
		}
		jt, joinToken, err := h.service.CreateJoinToken(r.Context(), printerID, createdBy)
		if err != nil {
			h.handleError(w, err)
			return
		}
		response := joinTokenCreateResponse{joinTokenResponse: toJoinTokenResponse(jt), JoinToken: joinToken}
		w.Header().Set("Cache-Control", "no-store")
		writeJSONResponse(w, http.StatusOK, response)
	}
}

func (h *UsersHandler) listJoinTokens(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	limit, offset, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}
	joinTokens, err := h.service.ListJoinTokens(r.Context(), printerID, limit, offset)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, mapSlice(joinTokens, toJoinTokenResponse))
}

func (h *UsersHandler) revokeJoinToken(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	tokenID, ok := parseUUIDParam(w, r, "tokenID", "invalid join token ID")
	if !ok {
		return
	}
	err := h.service.RevokeJoinToken(r.Context(), printerID, tokenID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- Public handlers ----

func (h *UsersHandler) claimInvite(w http.ResponseWriter, r *http.Request) {
	var req userClaimRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sessionToken, err := h.service.ClaimInvite(r.Context(), req.Invite, req.DeviceName)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusOK, userClaimResponse{Token: sessionToken})
}

func (h *UsersHandler) signupWithJoinToken(w http.ResponseWriter, r *http.Request) {
	var req userSignupRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sessionToken, err := h.service.SignupWithJoinToken(r.Context(), req.JoinToken, req.UserName, req.DeviceName)
	if errors.Is(err, users.ErrInvalidJoinToken) {
		writeErrorResponse(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusOK, userClaimResponse{Token: sessionToken})
}

// -- Admin handlers ----

func (h *UsersHandler) create(w http.ResponseWriter, r *http.Request) {
	var req userCreateRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, invite, err := h.service.Create(r.Context(), users.SubmitParams{UserName: req.UserName, PrinterIDs: req.PrinterIDs})
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
	writeJSONResponse(w, http.StatusOK, mapSlice(users, toUserAdminResponse))
}

func (h *UsersHandler) setPrinters(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id", "invalid user ID")
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
	user, err := h.service.SetPrinters(r.Context(), userID, accesses)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, toUserAdminResponse(user))
}

func (h *UsersHandler) issueRecoveryInvite(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	req := userIssueRecoveryInviteRequest{RevokeAllSessions: true}                // default: recovery revokes all sessions
	if err := decodeJSONBody(w, r, &req); err != nil && !errors.Is(err, io.EOF) { // the body is optional
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	inviteToken, expiresAt, err := h.service.IssueRecoveryInvite(r.Context(), userID, req.RevokeAllSessions)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusOK, inviteResponse{Invite: inviteToken, ExpiresAt: expiresAt})
}

func (h *UsersHandler) listSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	sessions, err := h.service.ListSessions(r.Context(), userID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, mapSlice(sessions, toSessionResponse))
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
	userID, ok := parseUUIDParam(w, r, "id", "invalid user ID")
	if !ok {
		return
	}
	if err := h.service.RevokeAllSessions(r.Context(), userID); err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UsersHandler) setOwnership(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	var req printerSetOwnershipRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == uuid.Nil {
		writeErrorResponse(w, http.StatusBadRequest, "user_id is required")
		return
	}
	user, err := h.service.SetOwnership(r.Context(), req.UserID, printerID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, toUserAdminResponse(user))
}

// Demotes the owner to member, or, with ?keep_as_member=false, remove the link to the printer entirely
func (h *UsersHandler) demoteOwner(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	keepAsMember := true
	if v := r.URL.Query().Get("keep_as_member"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeErrorResponse(w, http.StatusBadRequest, "invalid keep_as_member: must be true or false")
			return
		}
		keepAsMember = b
	}
	err := h.service.DemoteOwner(r.Context(), printerID, keepAsMember)
	if err != nil {
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

func (h *UsersHandler) selfIssueInvite(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r, h.logger)
	if !ok {
		return
	}
	inviteToken, expiresAt, err := h.service.SelfIssueInvite(r.Context(), userID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(w, http.StatusOK, inviteResponse{Invite: inviteToken, ExpiresAt: expiresAt})
}

func (h *UsersHandler) listOwnSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r, h.logger)
	if !ok {
		return
	}
	currentSessionID, _ := sessionIDFromContext(r.Context())
	sessions, err := h.service.ListSessions(r.Context(), userID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSONResponse(w, http.StatusOK, mapSlice(sessions, func(s *users.Session) ownSessionResponse {
		return ownSessionResponse{
			sessionResponse: toSessionResponse(s),
			Current:         s.ID == currentSessionID,
		}
	}))
}

// Revokes one of the caller's sessions; revoking the current one logs out the user
func (h *UsersHandler) revokeOwnSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r, h.logger)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDParam(w, r, "sessionID", "invalid session id")
	if !ok {
		return
	}
	if err := h.service.RevokeSession(r.Context(), userID, sessionID); err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Adds the caller's account as a member of the join token's printer
func (h *UsersHandler) redeemJoinToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r, h.logger)
	if !ok {
		return
	}
	var req userRedeemJoinTokenRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.service.RedeemJoinToken(r.Context(), userID, req.JoinToken)
	if err != nil {
		h.handleError(w, err) // an invalid join token is a 400 here: the session itself is fine
		return
	}
	writeJSONResponse(w, http.StatusOK, toUserMeResponse(user))
}

func (h *UsersHandler) revokeOwnMembership(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r, h.logger)
	if !ok {
		return
	}
	printerID, ok := parseUUIDParam(w, r, "printerID", "invalid printer id")
	if !ok {
		return
	}
	if err := h.service.SelfRevokeMembership(r.Context(), userID, printerID); err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	writeJSONResponse(w, http.StatusOK, mapSlice(members, toUserMemberResponse))
}

func (h *UsersHandler) revokeMembership(w http.ResponseWriter, r *http.Request) {
	printerID, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}
	userID, ok := parseUUIDParam(w, r, "userID", "invalid user ID")
	if !ok {
		return
	}

	err := h.service.RevokeMembership(r.Context(), userID, printerID)
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
		IsAdmin:         u.IsAdmin,
		PrinterAccesses: mapSlice(u.PrinterAccesses, toPrinterAccessResponse),
		CreatedAt:       u.CreatedAt,
		LastUpdatedAt:   u.LastUpdatedAt,
	}
}

func toUserMeResponse(u *users.User) userMeResponse {
	return userMeResponse{
		ID:              u.ID,
		Name:            u.Name,
		IsAdmin:         u.IsAdmin,
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

func toJoinTokenResponse(t *users.JoinToken) joinTokenResponse {
	var usedBy *joinTokenUserResponse
	if t.UsedBy != nil {
		usedBy = &joinTokenUserResponse{ID: *t.UsedBy, Name: t.UsedByName}
	}
	return joinTokenResponse{
		ID:        t.ID,
		PrinterID: t.PrinterID,
		CreatedBy: t.CreatedBy,
		Status:    t.Status(time.Now()),
		UsedBy:    usedBy,
		CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt,
		UsedAt:    t.UsedAt,
		RevokedAt: t.RevokedAt,
	}
}

// -- Errors ----

func (h *UsersHandler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrInvalidInviteToken):
		writeErrorResponse(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, users.ErrUserNotFound),
		errors.Is(err, users.ErrMemberNotFound),
		errors.Is(err, users.ErrOwnerNotFound),
		errors.Is(err, users.ErrSessionNotFound),
		errors.Is(err, users.ErrJoinTokenNotFound),
		errors.Is(err, users.ErrPrinterNotFound),
		errors.Is(err, printers.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrUnknownPrinter), // a printer ID in the request body, not the resource in the URL
		errors.Is(err, users.ErrInvalidJoinToken), // with a valid session
		errors.Is(err, users.ErrEmptyUserName),
		errors.Is(err, users.ErrUserNameTooLong),
		errors.Is(err, users.ErrEmptyDeviceName),
		errors.Is(err, users.ErrDeviceNameTooLong),
		errors.Is(err, users.ErrInvalidRole),
		errors.Is(err, users.ErrConflictingRoles),
		errors.Is(err, validate.ErrInvalidCharacters):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, users.ErrPrinterOwnershipViolation),
		errors.Is(err, users.ErrNameAlreadyTaken),
		errors.Is(err, users.ErrAlreadyMember),
		errors.Is(err, users.ErrOwnerRoleImmutable),
		errors.Is(err, users.ErrNoPrinters):
		writeErrorResponse(w, http.StatusConflict, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
