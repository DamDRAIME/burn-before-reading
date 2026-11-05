package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bbr/internal/printers"
)

type PrinterStore struct {
	pool *pgxpool.Pool
}

func NewPrinterStore(pool *pgxpool.Pool) *PrinterStore {
	return &PrinterStore{pool: pool}
}

// Compile-time check to ensure PrinterStore implements the printers.Store interface
var _ printers.Store = (*PrinterStore)(nil)

const printerColumns = `
	id,
	model,
	recipient_name,
	quiet_start,
	quiet_end,
	timezone,
	error,
	created_at,
	last_seen_at
`

func scanPrinter(row pgx.Row) (printers.Printer, error) {
	var p printers.Printer

	err := row.Scan(
		&p.ID,
		&p.Model,
		&p.RecipientName,
		&p.QuietStart,
		&p.QuietEnd,
		&p.Timezone,
		&p.Error,
		&p.CreatedAt,
		&p.LastSeenAt,
	)

	return p, err
}

func (s *PrinterStore) CreatePrinter(ctx context.Context, params printers.CreatePrinterParams) (*printers.Printer, error) {

	row := s.pool.QueryRow(
		ctx,
		`INSERT INTO printers (model, recipient_name, quiet_start, quiet_end, timezone) VALUES ($1, $2, $3, $4, $5) RETURNING `+printerColumns,
		params.Model,
		params.RecipientName,
		params.QuietStart,
		params.QuietEnd,
		params.Timezone,
	)

	p, err := scanPrinter(row)
	if err != nil {
		return nil, fmt.Errorf("insert printer: %w", err)
	}

	return &p, nil
}

func (s *PrinterStore) GetPrinterByID(ctx context.Context, id uuid.UUID) (*printers.Printer, error) {
	row := s.pool.QueryRow(
		ctx,
		`SELECT `+printerColumns+` FROM printers WHERE id = $1`,
		id,
	)

	p, err := scanPrinter(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, printers.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get printer by id: %w", err)
	}

	return &p, nil
}

func (s *PrinterStore) ListPrinters(ctx context.Context, limit int, offset int) ([]*printers.Printer, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+printerColumns+` FROM printers ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list printers: %w", err)
	}
	defer rows.Close()

	printers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*printers.Printer, error) {
		p, err := scanPrinter(row)
		return &p, err
	})
	if err != nil {
		return nil, fmt.Errorf("list printers: %w", err)
	}

	return printers, nil
}

func (s *PrinterStore) GetPrinterStats(ctx context.Context, id uuid.UUID) (*printers.PrinterStats, error) {
	row := s.pool.QueryRow(
		ctx,
		`SELECT
			id,
			COUNT(*) FILTER (WHERE attempts > 0)        AS messages_claimed,
			COUNT(*) FILTER (WHERE status = 'printed')  AS messages_printed,
			COUNT(*) FILTER (WHERE status = 'failed')   AS messages_failed,
			COALESCE(SUM(attempts), 0)                  AS print_attempts,
			MAX(claimed_at)                             AS last_claimed_at,
			MAX(printed_at)                             AS last_printed_at
		FROM messages
		WHERE printer_id = $1`,
		id,
	)

	var stats printers.PrinterStats
	err := row.Scan(
		&stats.MessagesClaimed,
		&stats.MessagesPrinted,
		&stats.MessagesFailed,
		&stats.PrintAttempts,
		&stats.LastClaimedAt,
		&stats.LastPrintedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get printer stats: %w", err)
	}

	return &stats, nil
}

func (s *PrinterStore) Heartbeat(ctx context.Context, id uuid.UUID, errorMsg *string) error {
	tag, err := s.pool.Exec(
		ctx,
		`UPDATE printers
		SET last_seen_at = NOW(),
			error = $1
		WHERE id = $2
		RETURNING `+printerColumns,
		errorMsg,
		id,
	)

	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return printers.ErrNotFound
	}

	return nil
}

func (s *PrinterStore) UpdatePrinter(ctx context.Context, id uuid.UUID, params printers.UpdatePrinterParams) (*printers.Printer, error) {
	row := s.pool.QueryRow(
		ctx,
		`UPDATE printers
		SET model = COALESCE($1, model),
			recipient_name = COALESCE($2, recipient_name)
		WHERE id = $3
		RETURNING `+printerColumns,
		params.Model,
		params.RecipientName,
		id,
	)

	p, err := scanPrinter(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, printers.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("update printer: %w", err)
	}

	return &p, nil
}

func (s *PrinterStore) SetQuietHours(ctx context.Context, id uuid.UUID, params printers.QuietHoursParams) (*printers.Printer, error) {
	row := s.pool.QueryRow(
		ctx,
		`UPDATE printers
		SET quiet_start = $1,
			quiet_end = $2,
			timezone = $3
		WHERE id = $4
		RETURNING `+printerColumns,
		params.QuietStart,
		params.QuietEnd,
		params.Timezone,
		id,
	)

	p, err := scanPrinter(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, printers.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("set quiet hours: %w", err)
	}

	return &p, nil
}
