package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
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

func (h *MessagesHandler) RegisterRoutes(r chi.Router) {
	r.Post("/messages", h.createMessage)
	r.Get("/messages/{id}", h.getMessageByID)
	r.Get("/messages", h.listMessages)
	r.Patch("/messages/{id}/approve", h.approveMessage)
}

type messageResponse struct {
	ID         uuid.UUID  `json:"id"`
	SenderName string     `json:"sender_name"`
	SenderIP   *net.IP    `json:"sender_ip,omitempty"`
	Source     string     `json:"source"`
	Content    string     `json:"content"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	PrintedAt  *time.Time `json:"printed_at,omitempty"`
	Attempts   int        `json:"attempts"`
	LastError  *string    `json:"last_error,omitempty"`
	PrinterID  *uuid.UUID `json:"printer_id,omitempty"`
}

func (h *MessagesHandler) createMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sender string `json:"sender"`
		Body   string `json:"body"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10) // Limit request body to 16KB
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields() // Disallow unknown fields in the JSON request

	if err := decoder.Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid request body")
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

	submitParams := messages.SubmitMessageParams{
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

	writeJSONResponse(w, http.StatusCreated, toMessageResponse(msg))
}

func (h *MessagesHandler) getMessageByID(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid message ID")
		return
	}

	msg, err := h.service.GetMessageByID(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageResponse(msg))
}

func (h *MessagesHandler) listMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	msgs, err := h.service.ListMessages(r.Context(), limit, offset)
	if err != nil {
		h.handleError(w, err)
		return
	}

	msgs_response := make([]messageResponse, len(msgs))
	for i, msg := range msgs {
		msgs_response[i] = toMessageResponse(msg)
	}

	writeJSONResponse(w, http.StatusOK, msgs_response)
}

func (h *MessagesHandler) approveMessage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid message ID")
		return
	}

	msg, err := h.service.ApproveMessage(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toMessageResponse(msg))
}

func toMessageResponse(msg *messages.Message) messageResponse {
	return messageResponse{
		ID:         msg.ID,
		SenderName: msg.SenderName,
		SenderIP:   msg.SenderIP,
		Source:     string(msg.Source),
		Content:    msg.Content,
		Status:     string(msg.Status),
		CreatedAt:  msg.CreatedAt,
		ApprovedAt: msg.ApprovedAt,
		ClaimedAt:  msg.ClaimedAt,
		PrintedAt:  msg.PrintedAt,
		Attempts:   msg.Attempts,
		LastError:  msg.LastError,
		PrinterID:  msg.PrinterID,
	}
}

func writeJSONResponse(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(v)
}

func writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	writeJSONResponse(w, statusCode, map[string]string{"error": message})
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
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
