package printers

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrEmptyModel   = errors.New("model cannot be empty")
	ErrModelTooLong = errors.New("model exceeds maximum length")

	ErrEmptyRecipientName   = errors.New("recipient name cannot be empty")
	ErrRecipientNameTooLong = errors.New("recipient name exceeds maximum length")

	ErrInvalidQuietHours = errors.New("quiet hours must have both start and end times")
	ErrEqualQuietHours   = errors.New("quiet start and end times cannot be equal")
	ErrInvalidTimezone   = errors.New("invalid timezone")

	ErrInvalidCharacters = errors.New("text contains control characters")

	ErrNotFound = errors.New("printer not found")
)

const (
	MaxModelLength         = 50
	MaxRecipientNameLength = 20
	DefaultTimezone        = "Europe/Brussels"
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
	QuietStart    *TimeOfDay
	QuietEnd      *TimeOfDay
	Timezone      *string
}

func normalizeTimezone(timezone *string) (string, bool) {
	if timezone == nil {
		return DefaultTimezone, true
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

func validateOptionalString(s *string, maxLength int, errEmpty, errTooLong error) (*string, error) {
	if s == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil, errEmpty
	}
	if strings.ContainsFunc(trimmed, unicode.IsControl) {
		return nil, ErrInvalidCharacters
	}
	if utf8.RuneCountInString(trimmed) > maxLength {
		return nil, errTooLong
	}
	return &trimmed, nil
}

func (s *Service) SubmitPrinter(ctx context.Context, params SubmitPrinterParams) (*Printer, error) {

	model, err := validateOptionalString(&params.Model, MaxModelLength, ErrEmptyModel, ErrModelTooLong)
	if err != nil {
		return nil, err
	}
	if model == nil {
		return nil, ErrEmptyModel
	}

	recipientName, err := validateOptionalString(&params.RecipientName, MaxRecipientNameLength, ErrEmptyRecipientName, ErrRecipientNameTooLong)
	if err != nil {
		return nil, err
	}
	if recipientName == nil {
		return nil, ErrEmptyRecipientName
	}

	if (params.QuietStart == nil && params.QuietEnd != nil) || (params.QuietStart != nil && params.QuietEnd == nil) {
		return nil, ErrInvalidQuietHours
	}
	if params.QuietStart != nil && params.QuietEnd != nil && *params.QuietStart == *params.QuietEnd {
		return nil, ErrEqualQuietHours
	}

	timezone, ok := normalizeTimezone(params.Timezone)
	if !ok {
		return nil, ErrInvalidTimezone
	}

	printerParams := CreatePrinterParams{
		Model:         *model,
		RecipientName: *recipientName,
		QuietStart:    params.QuietStart,
		QuietEnd:      params.QuietEnd,
		Timezone:      &timezone,
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

func (s *Service) GetPrinterStats(ctx context.Context, id uuid.UUID) (*PrinterStats, error) {
	return s.store.GetPrinterStats(ctx, id)
}

func (s *Service) Heartbeat(ctx context.Context, id uuid.UUID, errorMsg *string) error {
	if errorMsg != nil {
		cleaned := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\t' || !unicode.IsControl(r) {
				return r
			}
			return -1 // drop bad characters
		}, *errorMsg)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			errorMsg = nil
		} else {
			errorMsg = &cleaned
		}
	}
	return s.store.Heartbeat(ctx, id, errorMsg)
}

func (s *Service) UpdatePrinter(ctx context.Context, id uuid.UUID, params UpdatePrinterParams) (*Printer, error) {
	model, err := validateOptionalString(params.Model, MaxModelLength, ErrEmptyModel, ErrModelTooLong)
	if err != nil {
		return nil, err
	}

	recipientName, err := validateOptionalString(params.RecipientName, MaxRecipientNameLength, ErrEmptyRecipientName, ErrRecipientNameTooLong)
	if err != nil {
		return nil, err
	}

	return s.store.UpdatePrinter(ctx, id, UpdatePrinterParams{
		Model:         model,
		RecipientName: recipientName,
	})
}

func (s *Service) SetQuietHours(ctx context.Context, id uuid.UUID, params QuietHoursParams) (*Printer, error) {
	timezone, ok := normalizeTimezone(params.Timezone)
	if !ok {
		return nil, ErrInvalidTimezone
	}

	if params.QuietStart == nil || params.QuietEnd == nil {
		return nil, ErrInvalidQuietHours
	}
	if *params.QuietStart == *params.QuietEnd {
		return nil, ErrEqualQuietHours
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
