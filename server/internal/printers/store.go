package printers

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, params CreateParams) (*Printer, error)

	Get(ctx context.Context, id uuid.UUID) (*Printer, error)

	List(ctx context.Context, limit, offset int) ([]*Printer, error)

	GetStats(ctx context.Context, id uuid.UUID) (*PrinterStats, error)

	Heartbeat(ctx context.Context, id uuid.UUID, errorMsg *string) error

	Update(ctx context.Context, id uuid.UUID, params UpdateParams) (*Printer, error)

	SetQuietHours(ctx context.Context, id uuid.UUID, params QuietHoursParams) (*Printer, error)

	GetIDByTokenHash(ctx context.Context, hash []byte) (uuid.UUID, error)

	SetTokenHash(ctx context.Context, id uuid.UUID, hash []byte) error
}

type CreateParams struct {
	Model         string
	RecipientName string
	QuietStart    *TimeOfDay
	QuietEnd      *TimeOfDay
	Timezone      *string
	TokenHash     []byte
}

type UpdateParams struct {
	Model         *string
	RecipientName *string
}

type QuietHoursParams struct {
	QuietStart *TimeOfDay
	QuietEnd   *TimeOfDay
	Timezone   *string
}
