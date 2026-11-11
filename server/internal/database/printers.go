package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"bbr/internal/printers"
)

const microsPerMinute = 60_000_000

func toPgTime(t *printers.TimeOfDay) pgtype.Time {
	if t == nil {
		return pgtype.Time{} // NULL
	}
	return pgtype.Time{Microseconds: int64(t.Hour*60+t.Minute) * microsPerMinute, Valid: true}
}

func fromPgTime(v pgtype.Time) *printers.TimeOfDay {
	if !v.Valid {
		return nil
	}
	m := int(v.Microseconds / microsPerMinute)
	return &printers.TimeOfDay{Hour: m / 60, Minute: m % 60}
}

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
	last_updated_at,
	last_heartbeat_at
`

func scanPrinter(row pgx.Row) (printers.Printer, error) {
	var p printers.Printer
	var quietStart, quietEnd pgtype.Time

	err := row.Scan(
		&p.ID,
		&p.Model,
		&p.RecipientName,
		&quietStart,
		&quietEnd,
		&p.Timezone,
		&p.Error,
		&p.CreatedAt,
		&p.LastUpdatedAt,
		&p.LastHeartbeatAt,
	)

	p.QuietStart = fromPgTime(quietStart)
	p.QuietEnd = fromPgTime(quietEnd)

	return p, err
}

func (s *PrinterStore) Create(ctx context.Context, params printers.CreateParams) (*printers.Printer, error) {

	row := s.pool.QueryRow(
		ctx,
		`INSERT INTO printers (model, recipient_name, quiet_start, quiet_end, timezone, token_hash) VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+printerColumns,
		params.Model,
		params.RecipientName,
		toPgTime(params.QuietStart),
		toPgTime(params.QuietEnd),
		params.Timezone,
		params.TokenHash,
	)

	p, err := scanPrinter(row)
	if err != nil {
		return nil, fmt.Errorf("insert printer: %w", err)
	}

	return &p, nil
}

func (s *PrinterStore) Get(ctx context.Context, id uuid.UUID) (*printers.Printer, error) {
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

func (s *PrinterStore) List(ctx context.Context, limit int, offset int) ([]*printers.Printer, error) {
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

	ps, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*printers.Printer, error) {
		p, err := scanPrinter(row)
		return &p, err
	})
	if err != nil {
		return nil, fmt.Errorf("list printers: %w", err)
	}

	return ps, nil
}

func (s *PrinterStore) GetStats(ctx context.Context, id uuid.UUID) (*printers.PrinterStats, error) {
	row := s.pool.QueryRow(
		ctx,
		`SELECT
			p.id,
			COUNT(*) FILTER (WHERE attempts > 0)        AS messages_claimed,
			COUNT(*) FILTER (WHERE status = 'printed')  AS messages_printed,
			COUNT(*) FILTER (WHERE status = 'failed')   AS messages_failed,
			COALESCE(SUM(attempts), 0)                  AS print_attempts,
			MAX(claimed_at)                             AS last_claimed_at,
			MAX(printed_at)                             AS last_printed_at
		FROM printers p
		LEFT JOIN messages m ON p.id = m.printer_id
		WHERE p.id = $1
		GROUP BY p.id`,
		id,
	)

	var stats printers.PrinterStats
	err := row.Scan(
		&stats.ID,
		&stats.MessagesClaimed,
		&stats.MessagesPrinted,
		&stats.MessagesFailed,
		&stats.PrintAttempts,
		&stats.LastClaimedAt,
		&stats.LastPrintedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, printers.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get printer stats: %w", err)
	}

	return &stats, nil
}

func (s *PrinterStore) Heartbeat(ctx context.Context, id uuid.UUID, errorMsg *string) error {
	tag, err := s.pool.Exec(
		ctx,
		`UPDATE printers
		SET last_heartbeat_at = NOW(),
			error = $1
		WHERE id = $2`,
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

func (s *PrinterStore) Update(ctx context.Context, id uuid.UUID, params printers.UpdateParams) (*printers.Printer, error) {
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
		toPgTime(params.QuietStart),
		toPgTime(params.QuietEnd),
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

func (s *PrinterStore) GetIDByTokenHash(ctx context.Context, hash []byte) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(
		ctx,
		`SELECT id
		FROM printers
		WHERE token_hash = $1`,
		hash,
	).Scan(&id)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, printers.ErrNotFound
	}

	if err != nil {
		return uuid.Nil, fmt.Errorf("get printer ID by token hash: %w", err)
	}

	return id, nil
}

func (s *PrinterStore) SetTokenHash(ctx context.Context, id uuid.UUID, hash []byte) error {
	tag, err := s.pool.Exec(
		ctx,
		`UPDATE printers
		SET token_hash = $1
		WHERE id = $2`,
		hash,
		id,
	)

	if err != nil {
		return fmt.Errorf("set token hash: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return printers.ErrNotFound
	}

	return nil
}
