package web

import (
	"encoding/json"
	"errors"
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
	r.Get("/printers/{id}/stats", h.getPrinterStats)
	r.Patch("/printers/{id}/update", h.updatePrinter)
	r.Patch("/printers/{id}/set_quiet_hours", h.setQuietHours)
	r.Patch("/printers/{id}/clear_quiet_hours", h.clearQuietHours)
}

func (h *PrintersHandler) RegisterPrinterRoutes(r chi.Router) {
	r.Post("/heartbeat", h.heartbeat)
}

type printerPublicResponse struct {
	ID            uuid.UUID  `json:"id"`
	Model         string     `json:"model"`
	RecipientName string     `json:"recipient_name"`
	QuietStart    *time.Time `json:"quiet_start"`
	QuietEnd      *time.Time `json:"quiet_end"`
	Timezone      *string    `json:"timezone"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
}

type printerAdminResponse struct {
	ID            uuid.UUID  `json:"id"`
	Model         string     `json:"model"`
	RecipientName string     `json:"recipient_name"`
	QuietStart    *time.Time `json:"quiet_start"`
	QuietEnd      *time.Time `json:"quiet_end"`
	Timezone      *string    `json:"timezone"`
	Error         *string    `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
}

func (h *PrintersHandler) createPrinter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model         string     `json:"model"`
		RecipientName string     `json:"recipient_name"`
		QuietStart    *time.Time `json:"quiet_start"`
		QuietEnd      *time.Time `json:"quiet_end"`
		Timezone      *string    `json:"timezone"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10) // Limit request body to 16KB
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields() // Disallow unknown fields in the JSON request

	if err := decoder.Decode(&req); err != nil {
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

	msg, err := h.service.SubmitPrinter(r.Context(), submitParams)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, toPrinterPublicResponse(msg))
}

func toPrinterPublicResponse(p *printers.Printer) printerPublicResponse {
	return printerPublicResponse{
		ID:            p.ID,
		Model:         p.Model,
		RecipientName: p.RecipientName,
		QuietStart:    p.QuietStart,
		QuietEnd:      p.QuietEnd,
		Timezone:      p.Timezone,
		CreatedAt:     p.CreatedAt,
		LastSeenAt:    p.LastSeenAt,
	}
}

func toPrinterAdminResponse(p *printers.Printer) printerAdminResponse {
	return printerAdminResponse{
		ID:            p.ID,
		Model:         p.Model,
		RecipientName: p.RecipientName,
		QuietStart:    p.QuietStart,
		QuietEnd:      p.QuietEnd,
		Timezone:      p.Timezone,
		Error:         p.Error,
		CreatedAt:     p.CreatedAt,
		LastSeenAt:    p.LastSeenAt,
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
	case errors.Is(err, printers.ErrInvalidTimezone):
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printers.ErrNotFound):
		writeErrorResponse(w, http.StatusNotFound, err.Error())
	default:
		h.logger.Error("internal server error", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "internal server error")
	}
}
