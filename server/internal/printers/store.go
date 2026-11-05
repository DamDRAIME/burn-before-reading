package printers

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	CreatePrinter(ctx context.Context, params CreatePrinterParams) (*Printer, error)

	GetPrinterByID(ctx context.Context, id uuid.UUID) (*Printer, error)

	ListPrinters(ctx context.Context, limit, offset int) ([]*Printer, error)

	GetPrinterStats(ctx context.Context, id uuid.UUID) (*PrinterStats, error)

	Heartbeat(ctx context.Context, id uuid.UUID, errorMsg *string) error

	UpdatePrinter(ctx context.Context, id uuid.UUID, params UpdatePrinterParams) (*Printer, error)

	SetQuietHours(ctx context.Context, id uuid.UUID, params QuietHoursParams) (*Printer, error)
}

type CreatePrinterParams struct {
	Model         string
	RecipientName string
	QuietStart    *TimeOfDay
	QuietEnd      *TimeOfDay
	Timezone      *string
}

type UpdatePrinterParams struct {
	Model         *string
	RecipientName *string
}

type QuietHoursParams struct {
	QuietStart *TimeOfDay
	QuietEnd   *TimeOfDay
	Timezone   *string
}
