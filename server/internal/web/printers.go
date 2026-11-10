package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"bbr/internal/printers"
)

type PrintersHandler struct {
	service *printers.Service
	logger  *slog.Logger
}

func NewPrintersHandler(service *printers.Service, logger *slog.Logger) *PrintersHandler {
	return &PrintersHandler{service: service, logger: logger}
}

func (h *PrintersHandler) RegisterPublicRoutes(r chi.Router) {
	r.Get("/printers", h.listPrinters(func(p *printers.Printer) any {
		return toPrinterPublicResponse(p)
	}))
	r.Get("/printers/{id}", h.getPrinterByID(func(p *printers.Printer) any {
		return toPrinterPublicResponse(p)
	}))
}

func (h *PrintersHandler) RegisterAdminRoutes(r chi.Router) {
	r.Post("/printers", h.createPrinter)
	r.Get("/printers", h.listPrinters(func(p *printers.Printer) any {
		return toPrinterAdminResponse(p)
	}))
	r.Get("/printers/{id}", h.getPrinterByID(func(p *printers.Printer) any {
		return toPrinterAdminResponse(p)
	}))
	r.Patch("/printers/{id}", h.updatePrinter)
	r.Get("/printers/{id}/stats", h.getPrinterStats)
	r.Patch("/printers/{id}/set_quiet_hours", h.setQuietHours)
	r.Patch("/printers/{id}/clear_quiet_hours", h.clearQuietHours)
}

func (h *PrintersHandler) RegisterPrinterRoutes(r chi.Router) {
	r.Post("/{id}/heartbeat", h.heartbeat)
}

type printerPublicResponse struct {
	ID            uuid.UUID           `json:"id"`
	Model         string              `json:"model"`
	RecipientName string              `json:"recipient_name"`
	QuietStart    *printers.TimeOfDay `json:"quiet_start"`
	QuietEnd      *printers.TimeOfDay `json:"quiet_end"`
	Timezone      *string             `json:"timezone"`
	Online        bool                `json:"online"`
	CreatedAt     time.Time           `json:"created_at"`
	LastUpdatedAt time.Time           `json:"last_updated_at"`
}

type printerAdminResponse struct {
	ID              uuid.UUID           `json:"id"`
	Model           string              `json:"model"`
	RecipientName   string              `json:"recipient_name"`
	QuietStart      *printers.TimeOfDay `json:"quiet_start"`
	QuietEnd        *printers.TimeOfDay `json:"quiet_end"`
	Timezone        *string             `json:"timezone"`
	Error           *string             `json:"error"`
	Online          bool                `json:"online"`
	CreatedAt       time.Time           `json:"created_at"`
	LastUpdatedAt   time.Time           `json:"last_updated_at"`
	LastHeartbeatAt *time.Time          `json:"last_heartbeat_at"`
}

type printerStatsResponse struct {
	ID              uuid.UUID  `json:"id"`
	MessagesClaimed int64      `json:"messages_claimed"`
	MessagesPrinted int64      `json:"messages_printed"`
	MessagesFailed  int64      `json:"messages_failed"`
	PrintAttempts   int64      `json:"print_attempts"`
	LastClaimedAt   *time.Time `json:"last_claimed_at"`
	LastPrintedAt   *time.Time `json:"last_printed_at"`
}

func (h *PrintersHandler) createPrinter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model         string              `json:"model"`
		RecipientName string              `json:"recipient_name"`
		QuietStart    *printers.TimeOfDay `json:"quiet_start"`
		QuietEnd      *printers.TimeOfDay `json:"quiet_end"`
		Timezone      *string             `json:"timezone"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	submitParams := printers.SubmitPrinterParams{
		Model:         req.Model,
		RecipientName: req.RecipientName,
		QuietStart:    req.QuietStart,
		QuietEnd:      req.QuietEnd,
		Timezone:      req.Timezone,
	}

	p, err := h.service.SubmitPrinter(r.Context(), submitParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, toPrinterAdminResponse(p))
}

func (h *PrintersHandler) getPrinterByID(toResponse func(*printers.Printer) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
		if !ok {
			return
		}

		p, err := h.service.GetPrinterByID(r.Context(), id)
		if err != nil {
			h.handleError(w, err)
			return
		}

		writeJSONResponse(w, http.StatusOK, toResponse(p))
	}
}

func (h *PrintersHandler) listPrinters(toResponse func(*printers.Printer) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := parsePaginationParams(r)

		ps, err := h.service.ListPrinters(r.Context(), limit, offset)
		if err != nil {
			h.handleError(w, err)
			return
		}

		resp := make([]any, len(ps))
		for i, p := range ps {
			resp[i] = toResponse(p)
		}

		writeJSONResponse(w, http.StatusOK, resp)
	}
}

func (h *PrintersHandler) getPrinterStats(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}

	ps, err := h.service.GetPrinterStats(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toPrinterStatsResponse(ps))
}

func (h *PrintersHandler) updatePrinter(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}

	var req struct {
		Model         *string `json:"model"`
		RecipientName *string `json:"recipient_name"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updateParams := printers.UpdatePrinterParams{
		Model:         req.Model,
		RecipientName: req.RecipientName,
	}

	p, err := h.service.UpdatePrinter(r.Context(), id, updateParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toPrinterAdminResponse(p))
}

func (h *PrintersHandler) setQuietHours(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}

	var req struct {
		QuietStart *printers.TimeOfDay `json:"quiet_start"`
		QuietEnd   *printers.TimeOfDay `json:"quiet_end"`
		Timezone   *string             `json:"timezone"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := printers.QuietHoursParams{
		QuietStart: req.QuietStart,
		QuietEnd:   req.QuietEnd,
		Timezone:   req.Timezone,
	}

	p, err := h.service.SetQuietHours(r.Context(), id, params)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toPrinterAdminResponse(p))
}

func (h *PrintersHandler) clearQuietHours(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}

	p, err := h.service.ClearQuietHours(r.Context(), id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusOK, toPrinterAdminResponse(p))
}

func (h *PrintersHandler) heartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id", "invalid printer ID")
	if !ok {
		return
	}

	var req struct {
		Error *string `json:"error"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.service.Heartbeat(r.Context(), id, req.Error)
	if err != nil {
		h.handleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func toPrinterPublicResponse(p *printers.Printer) printerPublicResponse {
	return printerPublicResponse{
		ID:            p.ID,
		Model:         p.Model,
		RecipientName: p.RecipientName,
		QuietStart:    p.QuietStart,
		QuietEnd:      p.QuietEnd,
		Timezone:      p.Timezone,
		Online:        p.IsOnline(time.Now()),
		CreatedAt:     p.CreatedAt,
		LastUpdatedAt: p.LastUpdatedAt,
	}
}

func toPrinterAdminResponse(p *printers.Printer) printerAdminResponse {
	return printerAdminResponse{
		ID:              p.ID,
		Model:           p.Model,
		RecipientName:   p.RecipientName,
		QuietStart:      p.QuietStart,
		QuietEnd:        p.QuietEnd,
		Timezone:        p.Timezone,
		Error:           p.Error,
		Online:          p.IsOnline(time.Now()),
		CreatedAt:       p.CreatedAt,
		LastUpdatedAt:   p.LastUpdatedAt,
		LastHeartbeatAt: p.LastHeartbeatAt,
	}
}

func toPrinterStatsResponse(s *printers.PrinterStats) printerStatsResponse {
	return printerStatsResponse{
		ID:              s.ID,
		MessagesClaimed: s.MessagesClaimed,
		MessagesPrinted: s.MessagesPrinted,
		MessagesFailed:  s.MessagesFailed,
		PrintAttempts:   s.PrintAttempts,
		LastClaimedAt:   s.LastClaimedAt,
		LastPrintedAt:   s.LastPrintedAt,
	}
}

func (h *PrintersHandler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, printers.ErrEmptyModel):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrModelTooLong):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrEmptyRecipientName):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrRecipientNameTooLong):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrInvalidQuietHours):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrEqualQuietHours):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrInvalidTimezone):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrInvalidCharacters):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
