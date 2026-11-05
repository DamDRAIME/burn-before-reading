package web

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"bbr/internal/messages"
)

type MessagesHandler struct {
	service *messages.Service
	logger  *slog.Logger
}

func NewMessagesHandler(service *messages.Service, logger *slog.Logger) *MessagesHandler {
	return &MessagesHandler{service: service, logger: logger}
}

func (h *MessagesHandler) RegisterPublicRoutes(r chi.Router) {
	r.Post("/messages", h.createMessage)
	r.Get("/messages/{id}", h.getMessageByID(func(msg *messages.Message) any {
		return toMessagePublicResponse(msg)
	}))
}

func (h *MessagesHandler) RegisterAdminRoutes(r chi.Router) {
	r.Post("/messages", h.createAdminMessage)
	r.Get("/messages", h.listMessages)
	r.Get("/messages/{id}", h.getMessageByID(func(msg *messages.Message) any {
		return toMessageAdminResponse(msg)
	}))
	r.Patch("/messages/{id}/approve", h.approveMessage)
	r.Patch("/messages/{id}/reject", h.rejectMessage)
	r.Patch("/messages/{id}/requeue", h.requeueMessage)
}

func (h *MessagesHandler) RegisterPrinterRoutes(r chi.Router) {
	r.Post("/{id}/messages/oldest/claim", h.claimOldestMessage)
	r.Patch("/messages/{id}/processed", h.markAsProcessed)
	r.Patch("/messages/{id}/printed", h.markAsPrinted)
	r.Patch("/messages/{id}/failed", h.markAsFailed)
}

type messagePublicResponse struct {
	ID             uuid.UUID  `json:"id"`
	SequenceNumber int64      `json:"sequence_number"`
	SenderName     string     `json:"sender_name"`
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
	SenderName     string     `json:"sender_name"`
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

func (h *MessagesHandler) createMessage(w http.ResponseWriter, r *http.Request) {
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

	submitParams := messages.SubmitMessageParams{
		PrinterID:  printerID,
		SenderName: req.Sender,
		SenderIP:   &senderIP,
		Source:     messages.SourceWeb,
		Content:    req.Body,
	}

	msg, err := h.service.SubmitMessage(r.Context(), submitParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) createAdminMessage(w http.ResponseWriter, r *http.Request) {
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

	submitParams := messages.SubmitMessageParams{
		PrinterID:  printerID,
		SenderName: req.Sender,
		SenderIP:   &senderIP,
		Source:     messages.SourceAdmin,
		Content:    req.Body,
	}

	msg, err := h.service.SubmitMessage(r.Context(), submitParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) requeueMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.RequeueMessage(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageAdminResponse(msg))
}

func (h *MessagesHandler) getMessageByID(toResponse func(*messages.Message) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
		if !ok {
			return
		}

		msg, err := h.service.GetMessageByID(r.Context(), id)
		if err != nil {
			h.handleError(w, err)
			return
		}

		writeJSONResponse(w, http.StatusOK, toResponse(msg))
	}
}

func (h *MessagesHandler) listMessages(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaginationParams(r)

	msgs, err := h.service.ListMessages(r.Context(), limit, offset)
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

func (h *MessagesHandler) approveMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.ApproveMessage(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageAdminResponse(msg))
}

func (h *MessagesHandler) rejectMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.RejectMessage(r.Context(), id)
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

	msg, err := h.service.MarkAsProcessed(r.Context(), id)
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

	msg, err := h.service.MarkAsPrinted(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) markAsFailed(w http.ResponseWriter, r *http.Request) {
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

	msg, err := h.service.FailedMessage(r.Context(), msg_id, req.ErrorMsg)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) claimMessage(w http.ResponseWriter, r *http.Request) {
	msg_id, ok := parseUUIDParam(w, r, "id", "invalid message ID")
	if !ok {
		return
	}

	msg, err := h.service.ClaimMessage(r.Context(), msg_id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessagePublicResponse(msg))
}

func (h *MessagesHandler) claimOldestMessage(w http.ResponseWriter, r *http.Request) {
	printer_id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}

	msg, err := h.service.ClaimOldestMessage(r.Context(), printer_id)
	if err != nil {
		if errors.Is(err, messages.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent) // Queue is empty
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
	case errors.Is(err, messages.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	case errors.Is(err, messages.ErrInvalidStatus):
		writeErrorResponse(w, http.StatusConflict, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
