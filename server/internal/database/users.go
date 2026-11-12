package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bbr/internal/users"
)

type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// Compile-time check to ensure UserStore implements the users.Store interface
var _ users.Store = (*UserStore)(nil)

const userColumns = `
	u.id,
	u.name,
	u.created_at,
	u.last_updated_at,
	ARRAY(
		SELECT up.printer_id
		FROM user_printers up
		WHERE up.user_id = u.id
		ORDER BY up.printer_id
	) AS printer_ids
`

func scanUser(row pgx.Row) (users.User, error) {
	var u users.User
	err := row.Scan(
		&u.ID,
		&u.Name,
		&u.CreatedAt,
		&u.LastUpdatedAt,
		&u.PrinterIDs,
	)
	return u, err
}

func (s *UserStore) Create(ctx context.Context, params users.CreateParams) (*users.User, error) {

	var u users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(
			ctx,
			`INSERT INTO users (name, invite_hash, invite_expires_at) VALUES ($1, $2, $3) RETURNING `+userColumns,
			params.Name,
			params.InviteHash,
			params.InviteExpiresAt,
		)
		user, err := scanUser(row)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		u = user

		if len(params.PrinterIDs) > 0 {
			_, err := tx.CopyFrom(
				ctx,
				pgx.Identifier{"user_printers"},
				[]string{"user_id", "printer_id"},
				pgx.CopyFromRows(func() [][]any {
					rows := make([][]any, len(params.PrinterIDs))
					for i, printerID := range params.PrinterIDs {
						rows[i] = []any{u.ID, printerID}
					}
					return rows
				}()),
			)
			if err != nil {
				return fmt.Errorf("create user printers: %w", err)
			}
		}
		return nil
	})

	return &u, err
}

func (s *UserStore) Get(ctx context.Context, id uuid.UUID) (*users.User, error) {
	row := s.pool.QueryRow(
		ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`,
		id,
	)

	u, err := scanUser(row)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, users.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return &u, nil
}

func (s *UserStore) List(ctx context.Context, limit int, offset int) ([]*users.User, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+userColumns+` FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	us, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*users.User, error) {
		u, err := scanUser(row)
		return &u, err
	})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	return us, nil
}

func (s *UserStore) SetPrinters(ctx context.Context, userID uuid.UUID, printerIDs []uuid.UUID) (*users.User, error) {
	var u *users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(
			ctx,
			`DELETE FROM user_printers WHERE user_id = $1`,
			userID,
		)
		if err != nil {
			return fmt.Errorf("delete user printers: %w", err)
		}

		if len(printerIDs) > 0 {
			_, err := tx.CopyFrom(
				ctx,
				pgx.Identifier{"user_printers"},
				[]string{"user_id", "printer_id"},
				pgx.CopyFromRows(func() [][]any {
					rows := make([][]any, len(printerIDs))
					for i, printerID := range printerIDs {
						rows[i] = []any{userID, printerID}
					}
					return rows
				}()),
			)
			if err != nil {
				return fmt.Errorf("create user printers: %w", err)
			}
		}

		row := tx.QueryRow(
			ctx,
			`SELECT `+userColumns+` FROM users WHERE id = $1`,
			userID,
		)
		user, err := scanUser(row)
		if err != nil {
			return fmt.Errorf("get user by id: %w", err)
		}
		u = &user
		return nil
	})
	return u, err
}

func (s *UserStore) SetInvite(ctx context.Context, userID uuid.UUID, inviteHash []byte, inviteExpiresAt time.Time) (*users.User, error) {
	row := s.pool.QueryRow(
		ctx,
		`UPDATE users SET invite_hash = $1, invite_expires_at = $2 WHERE id = $3 RETURNING `+userColumns,
		inviteHash,
		inviteExpiresAt,
		userID,
	)
	user, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("set invite: %w", err)
	}
	return &user, nil
}

func (s *UserStore) ClaimInvite(ctx context.Context, inviteHash, sessionHash []byte) (uuid.UUID, uuid.UUID, error) {
	var userID uuid.UUID
	var sessionID uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(
			ctx,
			`UPDATE users
			SET invite_hash = NULL, invite_expires_at = NULL
			WHERE invite_hash = $1 AND invite_expires_at > NOW()
			RETURNING id`,
			inviteHash,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return users.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("consume invite: %w", err)
		}

		err = tx.QueryRow(
			ctx,
			`INSERT INTO user_sessions (user_id, token_hash) VALUES ($1, $2) RETURNING id`,
			userID,
			sessionHash,
		).Scan(&sessionID)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		return nil
	})
	return userID, sessionID, err
}
