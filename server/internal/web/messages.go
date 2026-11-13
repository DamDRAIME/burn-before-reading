package web

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"bbr/internal/messages"
	"bbr/internal/printers"
)

type MessagesHandler struct {
	service *messages.Service
	logger  *slog.Logger
}

func NewMessagesHandler(service *messages.Service, logger *slog.Logger) *MessagesHandler {
	return &MessagesHandler{service: service, logger: logger}
}

func (h *MessagesHandler) RegisterPublicRoutes(r chi.Router) {
	r.Post("/messages", h.submit)
	r.Get("/messages/{id}", h.get(func(msg *messages.Message) any {
		return toMessagePublicResponse(msg)
	}))
}

func (h *MessagesHandler) RegisterAdminRoutes(r chi.Router) {
	r.Post("/messages", h.submitAdmin)
	r.Get("/messages", h.list)
	r.Get("/messages/{id}", h.get(func(msg *messages.Message) any {
		return toMessageAdminResponse(msg)
	}))
	r.Patch("/messages/{id}/approve", h.approve)
	r.Patch("/messages/{id}/reject", h.reject)
	r.Patch("/messages/{id}/requeue", h.requeue)
}

func (h *MessagesHandler) RegisterPrinterRoutes(r chi.Router) {
	r.Post("/messages/oldest/claim", h.claimOldest)
	r.Patch("/messages/{id}/processed", h.markAsProcessed)
	r.Patch("/messages/{id}/printed", h.markAsPrinted)
	r.Patch("/messages/{id}/failed", h.markAsFailed)
}

type messagePublicResponse struct {
	ID             uuid.UUID  `json:"id"`
	SequenceNumber int64      `json:"sequence_number"`
	SenderName     *string    `json:"sender_name"`
	Content        string     `json:"content"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	ModeratedAt    *time.Time `json:"moderated_at"`
	ClaimedAt      *time.Time `json:"claimed_at"`
	ProcessedAt    *time.Time `json:"processed_at"`
	PrintedAt      *time.Time `json:"printed_at"`
	LastUpdatedAt  time.Time  `json:"last_updated_at"`
}

type messageAdminResponse struct {
	ID             uuid.UUID  `json:"id"`
	SequenceNumber int64      `json:"sequence_number"`
	SenderName     *string    `json:"sender_name"`
	SenderIP       *net.IP    `json:"sender_ip"`
	Source         string     `json:"source"`
	Content        string     `json:"content"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	ModeratedAt    *time.Time `json:"moderated_at"`
	ClaimedAt      *time.Time `json:"claimed_at"`
	ProcessedAt    *time.Time `json:"processed_at"`
	PrintedAt      *time.Time `json:"printed_at"`
	LastUpdatedAt  time.Time  `json:"last_updated_at"`
	Attempts       int        `json:"attempts"`
	LastError      *string    `json:"last_error"`
	PrinterID      uuid.UUID  `json:"printer_id"`
}

func (h *MessagesHandler) submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SenderID  string `json:"sender_id"`
		PrinterID string `json:"printer_id"`
		Content   string `json:"content"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var senderIP net.IP
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// In case RemoteAddr doesn't have a port (unlikely for HTTP)
		senderIP = net.ParseIP(r.RemoteAddr)
	} else {
		senderIP = net.ParseIP(host)
	}

	senderID, err := uuid.Parse(req.SenderID)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid sender ID")
		return
	}

	printerID, err := uuid.Parse(req.PrinterID)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid printer ID")
		return
	}

	submitParams := messages.SubmitParams{
		PrinterID: printerID,
		SenderID:  &senderID,
		SenderIP:  &senderIP,
		Source:    messages.SourceWeb,
		Content:   req.Content,
	}

	msg, err := h.service.Submit(r.Context(), submitParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) submitAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sender    string `json:"sender"`
		Body      string `json:"body"`
		PrinterID string `json:"printer_id"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var senderIP net.IP
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// In case RemoteAddr doesn't have a port (unlikely for HTTP)
		senderIP = net.ParseIP(r.RemoteAddr)
	} else {
		senderIP = net.ParseIP(host)
	}

	printerID, err := uuid.Parse(req.PrinterID)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid printer ID")
		return
	}

	submitParams := messages.SubmitParams{
		PrinterID: printerID,
		SenderID:  nil,
		SenderIP:  &senderIP,
		Source:    messages.SourceAdmin,
		Content:   req.Body,
	}

	msg, err := h.service.Submit(r.Context(), submitParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, toMessageAdminResponse(msg))
}

// Doesn't reset `Attempts` so history is preserved and failed messages get one more try
func (h *MessagesHandler) requeue(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.Requeue(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageAdminResponse(msg))
}

func (h *MessagesHandler) get(toResponse func(*messages.Message) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
		if !ok {
			return
		}

		msg, err := h.service.Get(r.Context(), id)
		if err != nil {
			h.handleError(w, err)
			return
		}

		writeJSONResponse(w, http.StatusOK, toResponse(msg))
	}
}

func (h *MessagesHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaginationParams(r)

	var statuses []string
	for _, v := range r.URL.Query()["status"] {
		statuses = append(statuses, strings.Split(v, ",")...)
	}

	order := r.URL.Query().Get("order")

	msgs, err := h.service.List(r.Context(), statuses, order, limit, offset)
	if err != nil {
		h.handleError(w, err)
		return
	}

	resp := make([]messageAdminResponse, len(msgs))
	for i, msg := range msgs {
		resp[i] = toMessageAdminResponse(msg)
	}

	writeJSONResponse(w, http.StatusOK, resp)
}

func (h *MessagesHandler) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.Approve(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageAdminResponse(msg))
}

func (h *MessagesHandler) reject(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.Reject(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageAdminResponse(msg))
}

func (h *MessagesHandler) markAsProcessed(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	printerID, ok := requirePrinterID(w, r, h.logger)
	if !ok {
		return
	}

	msg, err := h.service.MarkAsProcessed(r.Context(), id, printerID)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) markAsPrinted(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	printerID, ok := requirePrinterID(w, r, h.logger)
	if !ok {
		return
	}

	msg, err := h.service.MarkAsPrinted(r.Context(), id, printerID)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) markAsFailed(w http.ResponseWriter, r *http.Request) {
	printerID, ok := requirePrinterID(w, r, h.logger)
	if !ok {
		return
	}

	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	var req struct {
		ErrorMsg string `json:"error"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	msg, err := h.service.MarkAsFailed(r.Context(), id, printerID, req.ErrorMsg)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) claimOldest(w http.ResponseWriter, r *http.Request) {
	printerID, ok := requirePrinterID(w, r, h.logger)
	if !ok {
		return
	}

	msg, err := h.service.ClaimOldest(r.Context(), printerID)
	if err != nil {
		if errors.Is(err, messages.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent) // Queue is empty
			return
		}
		if errors.Is(err, messages.ErrInQuietHours) {
			w.WriteHeader(http.StatusNoContent) // Operation not allowed during quiet hours
			return
		}
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func toMessagePublicResponse(msg *messages.Message) messagePublicResponse {
	return messagePublicResponse{
		ID:             msg.ID,
		SequenceNumber: msg.SequenceNumber,
		SenderName:     msg.SenderName,
		Content:        msg.Content,
		Status:         string(msg.Status),
		CreatedAt:      msg.CreatedAt,
		ModeratedAt:    msg.ModeratedAt,
		ClaimedAt:      msg.ClaimedAt,
		ProcessedAt:    msg.ProcessedAt,
		PrintedAt:      msg.PrintedAt,
		LastUpdatedAt:  msg.LastUpdatedAt,
	}
}

func toMessageAdminResponse(msg *messages.Message) messageAdminResponse {
	return messageAdminResponse{
		ID:             msg.ID,
		SequenceNumber: msg.SequenceNumber,
		SenderName:     msg.SenderName,
		SenderIP:       msg.SenderIP,
		Source:         string(msg.Source),
		Content:        msg.Content,
		Status:         string(msg.Status),
		CreatedAt:      msg.CreatedAt,
		ModeratedAt:    msg.ModeratedAt,
		ClaimedAt:      msg.ClaimedAt,
		ProcessedAt:    msg.ProcessedAt,
		PrintedAt:      msg.PrintedAt,
		Attempts:       msg.Attempts,
		LastError:      msg.LastError,
		PrinterID:      msg.PrinterID,
		LastUpdatedAt:  msg.LastUpdatedAt,
	}
}

func (h *MessagesHandler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, messages.ErrEmptySenderName):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, messages.ErrSenderNameTooLong):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, messages.ErrEmptyContent):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, messages.ErrContentTooLong):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, messages.ErrNotFound), errors.Is(err, printers.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	case errors.Is(err, messages.ErrInvalidStatus):
		writeErrorResponse(w, http.StatusConflict, err.Error())
	case errors.Is(err, messages.ErrInvalidStatusFilter):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, messages.ErrInvalidOrder):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
