package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"bbr/internal/messages"
	"bbr/internal/printers"
)

type MessageStore struct {
	pool *pgxpool.Pool
}

func NewMessageStore(pool *pgxpool.Pool) *MessageStore {
	return &MessageStore{pool: pool}
}

// Compile-time check to ensure MessageStore implements the messages.Store interface
var _ messages.Store = (*MessageStore)(nil)

type transition struct {
	from messages.Status
	to   messages.Status
}

// transitionTimestampColumn maps a status transition (from -> to) to the timestamp column stamped on entry.
// Column names can't be bound as query parameters, so they come only from this fixed map.
var transitionTimestampColumn = map[transition]string{
	{messages.StatusAwaitingApproval, messages.StatusPending}:  "moderated_at",
	{messages.StatusAwaitingApproval, messages.StatusRejected}: "moderated_at",
	{messages.StatusClaimed, messages.StatusProcessed}:         "processed_at",
	{messages.StatusProcessed, messages.StatusPrinted}:         "printed_at",
	// pending → claimed: set by the claim endpoint directly, not by a status transition
	// failed  → pending (requeue): nothing
}

// maxAttempts is the number of print attempts after which a failed message is no longer retried.
const maxAttempts = 3

const messageColumns = `
	id,
	sequence_number,
	sender_name,
	sender_ip,
	source,
	content,
	status,
	created_at,
	moderated_at,
	claimed_at,
	processed_at,
	printed_at,
	last_updated_at,
	attempts,
	last_error,
	printer_id
`

func scanMessage(row pgx.Row) (messages.Message, error) {
	var msg messages.Message

	err := row.Scan(
		&msg.ID,
		&msg.SequenceNumber,
		&msg.SenderName,
		&msg.SenderIP,
		&msg.Source,
		&msg.Content,
		&msg.Status,
		&msg.CreatedAt,
		&msg.ModeratedAt,
		&msg.ClaimedAt,
		&msg.ProcessedAt,
		&msg.PrintedAt,
		&msg.LastUpdatedAt,
		&msg.Attempts,
		&msg.LastError,
		&msg.PrinterID,
	)

	return msg, err
}

// resolveNoRowsUpdated is called when a conditional update on a message matched no rows,
// to distinguish a missing message (i.e.: wrong ID) from one whose current status didn't allow the update.
// The check runs separately from the update, so it may not reflect the exact state at update time;
// this is harmless as it only picks which error to report.
func (s *MessageStore) resolveNoRowsUpdated(ctx context.Context, id uuid.UUID, errorPrefix string) error {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("%s: %w", errorPrefix, err)
	}
	if !exists {
		return messages.ErrNotFound
	}
	return messages.ErrInvalidStatus
}

func (s *MessageStore) CreateMessage(ctx context.Context, params messages.CreateMessageParams) (*messages.Message, error) {

	row := s.pool.QueryRow(
		ctx,
		`INSERT INTO messages (sender_name, sender_ip, source, content, status, printer_id) VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+messageColumns,
		params.SenderName,
		params.SenderIP,
		params.Source,
		params.Content,
		params.Status,
		params.PrinterID,
	)

	msg, err := scanMessage(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			return nil, printers.ErrNotFound
		}
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
	if column, ok := transitionTimestampColumn[transition{from: from, to: to}]; ok {
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
		return nil, s.resolveNoRowsUpdated(ctx, id, "transition message status")
	}

	if err != nil {
		return nil, fmt.Errorf("transition message status: %w", err)
	}

	return &msg, nil
}

func (s *MessageStore) ListMessages(ctx context.Context, params messages.ListMessagesParams) ([]*messages.Message, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+messageColumns+` FROM messages WHERE ($1::text[] IS NULL OR status = ANY($1)) ORDER BY created_at `+string(params.Order)+` LIMIT $2 OFFSET $3`,
		params.Statuses,
		params.Limit,
		params.Offset,
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

func (s *MessageStore) MarkAsFailed(ctx context.Context, id uuid.UUID, errorMsg string) (*messages.Message, error) {
	row := s.pool.QueryRow(
		ctx,
		`UPDATE messages
		SET status = CASE WHEN attempts < $1 THEN $2 ELSE $3 END,
			last_error = $4
		WHERE id = $5 AND status = ANY($6)
		RETURNING `+messageColumns,
		maxAttempts,
		messages.StatusPending,
		messages.StatusFailed,
		errorMsg,
		id,
		[]messages.Status{messages.StatusClaimed, messages.StatusProcessed},
	)

	msg, err := scanMessage(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, s.resolveNoRowsUpdated(ctx, id, "failed message")
	}

	if err != nil {
		return nil, fmt.Errorf("failed message: %w", err)
	}

	return &msg, nil
}

func (s *MessageStore) ClaimOldestMessage(ctx context.Context, printerID uuid.UUID) (*messages.Message, error) {
	row := s.pool.QueryRow(
		ctx,
		`
		WITH beat AS (UPDATE printers SET last_heartbeat_at = NOW() WHERE id = $1)
		UPDATE messages
		SET status = $2,
			claimed_at = NOW(),
			attempts = attempts + 1
		WHERE id = (
			SELECT id
			FROM messages
			WHERE status = $3 AND printer_id = $1
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+messageColumns,
		printerID,
		messages.StatusClaimed,
		messages.StatusPending,
	)

	msg, err := scanMessage(row)

	if errors.Is(err, pgx.ErrNoRows) {
		// No pending message available (or all candidates are currently locked by other claimers).
		return nil, messages.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("claim oldest message: %w", err)
	}

	return &msg, nil
}

// ReleaseStaleClaims returns messages that have been claimed for longer than timeout to the queue,
// or marks them failed once maxAttempts is reached (attempts was already incremented by the claim).
// The cutoff is computed by the database (NOW()), so it doesn't depend on the API server's clock.
// It returns the number of messages released.
func (s *MessageStore) ReleaseStaleClaims(ctx context.Context, timeout time.Duration) (int64, error) {
	tag, err := s.pool.Exec(
		ctx,
		`UPDATE messages
		SET status = CASE WHEN attempts < $1 THEN $2 ELSE $3 END,
			last_error = 'claim timed out'
		WHERE status = ANY($4)
			AND claimed_at < NOW() - make_interval(secs => $5)`,
		maxAttempts,
		messages.StatusPending,
		messages.StatusFailed,
		[]messages.Status{messages.StatusClaimed, messages.StatusProcessed},
		timeout.Seconds(),
	)
	if err != nil {
		return 0, fmt.Errorf("release stale claims: %w", err)
	}

	return tag.RowsAffected(), nil
}
