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

func (s *MessageStore) UpdateMessageStatus(ctx context.Context, id uuid.UUID, newStatus messages.Status) (*messages.Message, error) {
	row := s.pool.QueryRow(
		ctx,
		`UPDATE messages SET status = $1 WHERE id = $2 RETURNING `+messageColumns,
		newStatus,
		id,
	)

	msg, err := scanMessage(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, messages.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("update message status: %w", err)
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
