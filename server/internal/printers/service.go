package printers

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"bbr/internal/token"
	"bbr/internal/validate"
)

var (
	ErrEmptyModel   = errors.New("model cannot be empty")
	ErrModelTooLong = errors.New("model exceeds maximum length")

	ErrEmptyRecipientName   = errors.New("recipient name cannot be empty")
	ErrRecipientNameTooLong = errors.New("recipient name exceeds maximum length")

	ErrInvalidQuietHours = errors.New("quiet hours must have both start and end times")
	ErrEqualQuietHours   = errors.New("quiet start and end times cannot be equal")
	ErrInvalidTimezone   = errors.New("invalid timezone")

	ErrInvalidToken = errors.New("invalid printer token")

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

// -- Methods ----

func (s *Service) Create(ctx context.Context, params CreateParams) (*Printer, string, error) {

	model, err := validate.RequiredString(params.Model, MaxModelLength, ErrEmptyModel, ErrModelTooLong)
	if err != nil {
		return nil, "", err
	}

	recipientName, err := validate.RequiredString(params.RecipientName, MaxRecipientNameLength, ErrEmptyRecipientName, ErrRecipientNameTooLong)
	if err != nil {
		return nil, "", err
	}

	if (params.QuietStart == nil && params.QuietEnd != nil) || (params.QuietStart != nil && params.QuietEnd == nil) {
		return nil, "", ErrInvalidQuietHours
	}
	if params.QuietStart != nil && params.QuietEnd != nil && *params.QuietStart == *params.QuietEnd {
		return nil, "", ErrEqualQuietHours
	}

	timezone, ok := normalizeTimezone(params.Timezone)
	if !ok {
		return nil, "", ErrInvalidTimezone
	}

	t, hash := token.Generate(token.PrinterPrefix)

	printerParams := CreateParams{
		Model:         model,
		RecipientName: recipientName,
		QuietStart:    params.QuietStart,
		QuietEnd:      params.QuietEnd,
		Timezone:      &timezone,
		TokenHash:     hash,
	}

	printer, err := s.store.Create(ctx, printerParams)
	if err != nil {
		return nil, "", err
	}

	return printer, t, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Printer, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]*Printer, error) {
	if limit <= 0 || limit > 10 {
		limit = 10 // default limit
	}
	if offset < 0 {
		offset = 0 // default offset
	}
	return s.store.List(ctx, limit, offset)
}

func (s *Service) GetStats(ctx context.Context, id uuid.UUID) (*PrinterStats, error) {
	return s.store.GetStats(ctx, id)
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

func (s *Service) Update(ctx context.Context, id uuid.UUID, params UpdateParams) (*Printer, error) {
	model, err := validate.OptionalString(params.Model, MaxModelLength, ErrEmptyModel, ErrModelTooLong)
	if err != nil {
		return nil, err
	}

	recipientName, err := validate.OptionalString(params.RecipientName, MaxRecipientNameLength, ErrEmptyRecipientName, ErrRecipientNameTooLong)
	if err != nil {
		return nil, err
	}

	return s.store.Update(ctx, id, UpdateParams{
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

// -- Authentication and Token Management --

func (s *Service) Authenticate(ctx context.Context, gotToken string) (uuid.UUID, error) {
	if !strings.HasPrefix(gotToken, string(token.PrinterPrefix)) {
		return uuid.Nil, ErrInvalidToken
	}

	id, err := s.store.GetIDByTokenHash(ctx, token.Hash(gotToken))
	if errors.Is(err, ErrNotFound) {
		return uuid.Nil, ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, err
	}

	return id, nil
}

func (s *Service) RotateToken(ctx context.Context, id uuid.UUID) (string, error) {
	token, hash := token.Generate(token.PrinterPrefix)
	if err := s.store.SetTokenHash(ctx, id, hash); err != nil {
		return "", err
	}

	return token, nil
}
