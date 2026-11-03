package printers

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrEmptyModel   = errors.New("model cannot be empty")
	ErrModelTooLong = errors.New("model exceeds maximum length")

	ErrEmptyRecipientName   = errors.New("recipient name cannot be empty")
	ErrRecipientNameTooLong = errors.New("recipient name exceeds maximum length")

	ErrInvalidTimezone = errors.New("invalid timezone")

	ErrNotFound = errors.New("printer not found")
)

const (
	MaxModelLength         = 50
	MaxRecipientNameLength = 20
)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

type SubmitPrinterParams struct {
	Model         string
	RecipientName string
	QuietStart    *time.Time
	QuietEnd      *time.Time
	Timezone      string
}

func validateTimezone(timezone *string) (string, bool) {
	if timezone == nil {
		return "", false
	}
	timezoneStr := strings.TrimSpace(*timezone)
	if timezoneStr == "" || timezoneStr == "Local" {
		return "", false
	}
	_, err := time.LoadLocation(timezoneStr)
	if err != nil {
		return "", false
	}
	return timezoneStr, true
}

func (s *Service) SubmitPrinter(ctx context.Context, params SubmitPrinterParams) (*Printer, error) {

	model := strings.TrimSpace(params.Model)
	if model == "" {
		return nil, ErrEmptyModel
	}
	if utf8.RuneCountInString(model) > MaxModelLength {
		return nil, ErrModelTooLong
	}

	recipientName := strings.TrimSpace(params.RecipientName)
	if recipientName == "" {
		return nil, ErrEmptyRecipientName
	}
	if utf8.RuneCountInString(recipientName) > MaxRecipientNameLength {
		return nil, ErrRecipientNameTooLong
	}

	timezone, ok := validateTimezone(&params.Timezone)
	if !ok {
		return nil, ErrInvalidTimezone
	}

	printerParams := CreatePrinterParams{
		Model:         model,
		RecipientName: recipientName,
		QuietStart:    params.QuietStart,
		QuietEnd:      params.QuietEnd,
		Timezone:      timezone,
	}

	return s.store.CreatePrinter(ctx, printerParams)
}

func (s *Service) GetPrinterByID(ctx context.Context, id uuid.UUID) (*Printer, error) {
	return s.store.GetPrinterByID(ctx, id)
}

func (s *Service) ListPrinters(ctx context.Context, limit, offset int) ([]*Printer, error) {
	if limit <= 0 || limit > 100 {
		limit = 10 // default limit
	}
	if offset < 0 {
		offset = 0 // default offset
	}
	return s.store.ListPrinters(ctx, limit, offset)
}

func (s *Service) Heartbeat(ctx context.Context, id uuid.UUID, errorMsg *string) error {
	if errorMsg != nil {
		trimmed := strings.TrimSpace(*errorMsg)
		if trimmed == "" {
			errorMsg = nil
		} else {
			errorMsg = &trimmed
		}
	}
	return s.store.Heartbeat(ctx, id, errorMsg)
}

func (s *Service) UpdatePrinter(ctx context.Context, id uuid.UUID, params UpdatePrinterParams) (*Printer, error) {
	return s.store.UpdatePrinter(ctx, id, params)
}

func (s *Service) SetQuietHours(ctx context.Context, id uuid.UUID, params QuietHoursParams) (*Printer, error) {
	timezone, ok := validateTimezone(params.Timezone)
	if !ok {
		return nil, ErrInvalidTimezone
	}

	return s.store.SetQuietHours(ctx, id, QuietHoursParams{
		QuietStart: params.QuietStart,
		QuietEnd:   params.QuietEnd,
		Timezone:   &timezone,
	})
}

func (s *Service) ClearQuietHours(ctx context.Context, id uuid.UUID) (*Printer, error) {
	return s.store.SetQuietHours(ctx, id, QuietHoursParams{
		QuietStart: nil,
		QuietEnd:   nil,
		Timezone:   nil,
	})
}
