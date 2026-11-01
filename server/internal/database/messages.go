package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bbr/internal/messages"
)

type MessageStore struct {
	pool *pgxpool.Pool
}

func NewMessageStore(pool *pgxpool.Pool) *MessageStore {
	return &MessageStore{pool: pool}
}

// Compile-time check to ensure MessageStore implements the messages.Store interface
var _ messages.Store = (*MessageStore)(nil)

// statusTimestampColumn maps a target status to the timestamp column stamped on entry.
// Column names can't be bound as query parameters, so they come only from this fixed map.
var statusTimestampColumn = map[messages.Status]string{
	messages.StatusPending:  "approved_at",
	messages.StatusPrinting: "claimed_at",
	messages.StatusPrinted:  "printed_at",
}

const messageColumns = `
	id,
	sender_name,
	sender_ip,
	source,
	content,
	status,
	created_at,
	approved_at,
	claimed_at,
	printed_at,
	attempts,
	last_error,
	printer_id
`

func scanMessage(row pgx.Row) (messages.Message, error) {
	var msg messages.Message

	err := row.Scan(
		&msg.ID,
		&msg.SenderName,
		&msg.SenderIP,
		&msg.Source,
		&msg.Content,
		&msg.Status,
		&msg.CreatedAt,
		&msg.ApprovedAt,
		&msg.ClaimedAt,
		&msg.PrintedAt,
		&msg.Attempts,
		&msg.LastError,
		&msg.PrinterID,
	)

	return msg, err
}

func (s *MessageStore) CreateMessage(ctx context.Context, params messages.CreateMessageParams) (*messages.Message, error) {

	row := s.pool.QueryRow(
		ctx,
		`INSERT INTO messages (sender_name, sender_ip, source, content, status) VALUES ($1, $2, $3, $4, $5) RETURNING `+messageColumns,
		params.SenderName,
		params.SenderIP,
		params.Source,
		params.Content,
		params.Status,
	)

	msg, err := scanMessage(row)
	if err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}

	return &msg, nil
}

func (s *MessageStore) GetMessageByID(ctx context.Context, id uuid.UUID) (*messages.Message, error) {
	row := s.pool.QueryRow(
		ctx,
		`SELECT `+messageColumns+` FROM messages WHERE id = $1`,
		id,
	)

	msg, err := scanMessage(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, messages.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get message by id: %w", err)
	}

	return &msg, nil
}



func (s *MessageStore) TransitionMessageStatus(ctx context.Context, id uuid.UUID, from, to messages.Status) (*messages.Message, error) {
	// Determine the timestamp column to set based on the target status
	setTimestampColumn := ""
	if column, ok := statusTimestampColumn[to]; ok {
		setTimestampColumn += ", " + column + " = NOW()"
	}

	// Compare and swap in one atomic operation to ensure the status transition is safe and prevent race conditions
	row := s.pool.QueryRow(
		ctx,
		`UPDATE messages SET status = $1`+setTimestampColumn+` WHERE id = $2 AND status = $3 RETURNING `+messageColumns,
		to,
		id,
		from,
	)

	msg, err := scanMessage(row)

	if errors.Is(err, pgx.ErrNoRows) {
		// No rows were updated, meaning the message was not found or the status did not match the expected 'from' status.
		// Note that this follow-up check is separate from the update hence it may not reflect the exact state at the time of the update.
		// but this is harmless as it only helps to distinguish between "not found" and "invalid status" errors.
		var exists bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1)`, id).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("transition message status: %w", err)
		}
		if !exists {
			return nil, messages.ErrNotFound
		}
		return nil, messages.ErrInvalidStatus
	}

	if err != nil {
		return nil, fmt.Errorf("transition message status: %w", err)
	}

	return &msg, nil
}

func (s *MessageStore) ListMessages(ctx context.Context, limit int, offset int) ([]*messages.Message, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+messageColumns+` FROM messages ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	msgs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*messages.Message, error) {
		msg, err := scanMessage(row)
		return &msg, err
	})
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	return msgs, nil
}
