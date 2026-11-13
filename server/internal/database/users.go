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

const sessionColumns = `
	id,
	user_id,
	device_name,
	created_at,
	last_used_at,
	revoked_at
`

const userColumns = `
	users.id,
	users.name,
	users.created_at,
	users.last_updated_at,
	ARRAY(
		SELECT up.printer_id
		FROM user_printers up
		WHERE up.user_id = users.id
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
		var userID uuid.UUID
		err := tx.QueryRow(
			ctx,
			`INSERT INTO users (name, invite_hash, invite_expires_at) VALUES ($1, $2, $3) RETURNING id`,
			params.Name,
			params.InviteHash,
			params.InviteExpiresAt,
		).Scan(&userID)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		if len(params.PrinterIDs) > 0 {
			if err := insertUserPrinters(ctx, tx, userID, params.PrinterIDs); err != nil {
				return err
			}
		}

		row := tx.QueryRow(
			ctx,
			`SELECT `+userColumns+` FROM users WHERE id = $1`,
			userID,
		)
		user, err := scanUser(row)
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		u = user
		return nil
	})
	if err != nil {
		return nil, err
	}

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
		return nil, users.ErrUserNotFound
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
		// Lock the user row first: an unknown user is a 404, so a later foreign key
		// violation can only come from an unknown printer ID in the body.
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 FOR UPDATE)`, userID).Scan(&exists)
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		if !exists {
			return users.ErrUserNotFound
		}

		_, err = tx.Exec(
			ctx,
			`DELETE FROM user_printers WHERE user_id = $1`,
			userID,
		)
		if err != nil {
			return fmt.Errorf("delete user printers: %w", err)
		}

		if len(printerIDs) > 0 {
			if err := insertUserPrinters(ctx, tx, userID, printerIDs); err != nil {
				return err
			}
		}

		row := tx.QueryRow(
			ctx,
			`SELECT `+userColumns+` FROM users WHERE id = $1`,
			userID,
		)
		user, err := scanUser(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return users.ErrUserNotFound
		}
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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, users.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("set invite: %w", err)
	}
	return &user, nil
}

func (s *UserStore) ClaimInvite(ctx context.Context, inviteHash, sessionHash []byte, deviceName string) (uuid.UUID, uuid.UUID, error) {
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
			return users.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("consume invite: %w", err)
		}

		err = tx.QueryRow(
			ctx,
			`INSERT INTO user_sessions (user_id, token_hash, device_name) VALUES ($1, $2, $3) RETURNING id`,
			userID,
			sessionHash,
			deviceName,
		).Scan(&sessionID)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		return nil
	})
	return userID, sessionID, err
}

func (s *UserStore) GetUserIDBySessionHash(ctx context.Context, hash []byte) (uuid.UUID, uuid.UUID, error) {
	var userID uuid.UUID
	var sessionID uuid.UUID
	err := s.pool.QueryRow(
		ctx,
		`SELECT user_id, id FROM user_sessions WHERE revoked_at IS NULL AND token_hash = $1`,
		hash,
	).Scan(&userID, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return userID, sessionID, users.ErrInvalidToken
	}
	if err != nil {
		return userID, sessionID, fmt.Errorf("get user by session hash: %w", err)
	}
	return userID, sessionID, nil
}

func (s *UserStore) ListSessions(ctx context.Context, userID uuid.UUID) ([]*users.Session, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+sessionColumns+` FROM user_sessions WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	sessions, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[users.Session])
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	// No sessions: tell an unknown user (404) apart from a user who has none (200 []).
	if len(sessions) == 0 {
		if err := s.requireUser(ctx, userID); err != nil {
			return nil, err
		}
	}

	return sessions, nil
}

// requireUser returns ErrUserNotFound when no user has this ID.
func (s *UserStore) requireUser(ctx context.Context, userID uuid.UUID) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("check user exists: %w", err)
	}
	if !exists {
		return users.ErrUserNotFound
	}
	return nil
}

func (s *UserStore) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	var id uuid.UUID
	row := s.pool.QueryRow(
		ctx,
		`UPDATE user_sessions SET revoked_at = COALESCE(revoked_at, NOW()) WHERE id = $1 AND user_id = $2 RETURNING id`,
		sessionID,
		userID,
	)
	err := row.Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (s *UserStore) RevokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE user_sessions SET revoked_at = NOW() WHERE revoked_at IS NULL AND user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	// Nothing revoked: tell an unknown user (404) apart from one with no active session (204).
	if tag.RowsAffected() == 0 {
		return s.requireUser(ctx, userID)
	}
	return nil
}

func (s *UserStore) CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error) {
	var validPair bool
	err := s.pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM user_printers WHERE user_id = $1 AND printer_id = $2)`,
		userID,
		printerID,
	).Scan(&validPair)
	if err != nil {
		return false, fmt.Errorf("can send check: %w", err)
	}
	return validPair, nil
}

// -- Helpers --

// insertUserPrinters links userID to printerIDs. Duplicates are ignored;
// Callers must make sure userID exists, so a foreign key violation means an unknown printer: ErrUnknownPrinter.
func insertUserPrinters(ctx context.Context, tx pgx.Tx, userID uuid.UUID, printerIDs []uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO user_printers (user_id, printer_id)
		 SELECT $1, unnest($2::uuid[])
		 ON CONFLICT DO NOTHING`,
		userID, printerIDs,
	)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return users.ErrUnknownPrinter
	}
	if err != nil {
		return fmt.Errorf("insert user printers: %w", err)
	}
	return nil
}
