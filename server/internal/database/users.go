package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"bbr/internal/messages"
	"bbr/internal/users"
)

type UserStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewUserStore(pool *pgxpool.Pool, logger *slog.Logger) *UserStore {
	return &UserStore{pool: pool, logger: logger}
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
		SELECT (up.printer_id, up.role)
		FROM user_printers up
		WHERE up.user_id = users.id
		ORDER BY up.printer_id
	) AS printer_accesses
`

func scanUser(row pgx.Row) (users.User, error) {
	var u users.User
	err := row.Scan(
		&u.ID,
		&u.Name,
		&u.CreatedAt,
		&u.LastUpdatedAt,
		&u.PrinterAccesses,
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

		if len(params.PrinterAccesses) > 0 {
			if err := insertUserPrinters(ctx, tx, userID, params.PrinterAccesses); err != nil {
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

func (s *UserStore) RemoveMember(ctx context.Context, printerID, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(
			ctx,
			`DELETE FROM user_printers WHERE printer_id = $1 AND user_id = $2 AND role = $3`,
			printerID,
			userID,
			users.RoleMember,
		)
		if err != nil {
			return fmt.Errorf("remove member: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return users.ErrMemberNotFound
		}

		// If the user has no more printer memberships, revoke all their sessions and clear their invite hash and expiration.
		err = revokeAccessIfNoPrinters(ctx, tx, userID)
		if err != nil {
			return err
		}

		// Reject all his pending messages to that printer.
		_, err = tx.Exec(ctx,
			`UPDATE messages
			SET status = $1, moderated_at = NOW()
			WHERE sender_id = $2
				AND printer_id = $3
				AND status = ANY($4)`,
			messages.StatusRejected,
			userID,
			printerID,
			[]messages.Status{messages.StatusPending, messages.StatusAwaitingApproval},
		)
		if err != nil {
			return fmt.Errorf("reject messages: %w", err)
		}

		return nil
	})
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

func (s *UserStore) ListMembers(ctx context.Context, printerID uuid.UUID) ([]*users.User, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+userColumns+` FROM users
		WHERE id IN (SELECT user_id FROM user_printers WHERE printer_id = $1 and role = $2)
		ORDER BY created_at DESC`,
		printerID,
		users.RoleMember,
	)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	us, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*users.User, error) {
		u, err := scanUser(row)
		return &u, err
	})
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}

	return us, nil
}

func (s *UserStore) SetPrinters(ctx context.Context, userID uuid.UUID, printerAccesses []users.PrinterAccess) (*users.User, error) {
	var u users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock the user row, bump last_updated_at and check the user exists: an unknown
		// user is a 404, so a later foreign key violation can only come from an unknown printer ID.
		err := tx.QueryRow(
			ctx,
			`UPDATE users SET last_updated_at = NOW() WHERE id = $1 RETURNING id`,
			userID,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return users.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}

		_, err = tx.Exec(
			ctx,
			`DELETE FROM user_printers WHERE user_id = $1`,
			userID,
		)
		if err != nil {
			return fmt.Errorf("delete user printers: %w", err)
		}

		if len(printerAccesses) > 0 {
			if err := insertUserPrinters(ctx, tx, userID, printerAccesses); err != nil {
				return err
			}
		}

		// If the user has no more printer memberships, revoke all their sessions and clear their invite hash and expiration.
		err = revokeAccessIfNoPrinters(ctx, tx, userID)
		if err != nil {
			return err
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
		u = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &u, err
}

func (s *UserStore) SetOwner(ctx context.Context, userID, printerID uuid.UUID) (*users.User, error) {
	var u *users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock the user row, bump last_updated_at and check the user exists: an unknown
		// user is a 404, so a later foreign key violation can only come from an unknown printer ID.
		err := tx.QueryRow(
			ctx,
			`UPDATE users SET last_updated_at = NOW() WHERE id = $1 RETURNING id`,
			userID,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return users.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}

		_, err = tx.Exec(
			ctx,
			`INSERT INTO user_printers (user_id, printer_id, role) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, printer_id) DO UPDATE SET role = 'owner'`,
			userID,
			printerID,
			users.RoleOwner,
		)
		if err != nil {
			if mappedErr := mapUserPrinterError(err); mappedErr != err {
				return mappedErr
			}
			return fmt.Errorf("set owner: %w", err)
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
	if _, err = s.pool.Exec(
		ctx,
		`UPDATE user_sessions SET last_used_at = NOW() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '5 minute')`,
		sessionID,
	); err != nil {
		s.logger.Warn("failed to update session last_used_at", "session_id", sessionID, "error", err)
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

func (s *UserStore) IsOwner(ctx context.Context, userID, printerID uuid.UUID) (bool, error) {
	var isOwner bool
	err := s.pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM user_printers WHERE user_id = $1 AND printer_id = $2 AND role = 'owner')`,
		userID,
		printerID,
	).Scan(&isOwner)
	if err != nil {
		return false, fmt.Errorf("is owner check: %w", err)
	}
	return isOwner, nil
}

// -- Helpers --

// insertUserPrinters links userID to printerIDs with the specified roles.
// Callers must make sure userID exists, so a foreign key violation means an unknown printer: ErrUnknownPrinter.
func insertUserPrinters(ctx context.Context, tx pgx.Tx, userID uuid.UUID, printerAccesses []users.PrinterAccess) error {
	printerIDs, roles := printerAccessToArrays(printerAccesses)
	_, err := tx.Exec(ctx,
		`INSERT INTO user_printers (user_id, printer_id, role)
		 SELECT $1, unnest($2::uuid[]), unnest($3::varchar[])`,
		userID, printerIDs, roles,
	)
	if err != nil {
		if mappedErr := mapUserPrinterError(err); mappedErr != err {
			return mappedErr
		}
		return fmt.Errorf("insert user printers: %w", err)
	}
	return nil
}

func printerAccessToArrays(printerAccesses []users.PrinterAccess) ([]uuid.UUID, []string) {
	printerIDs := make([]uuid.UUID, len(printerAccesses))
	roles := make([]string, len(printerAccesses))
	for i, pa := range printerAccesses {
		printerIDs[i] = pa.PrinterID
		roles[i] = string(pa.Role)
	}
	return printerIDs, roles
}

// mapUserPrinterError translates user_printers constraint violations into domain errors.
func mapUserPrinterError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23503": // foreign_key_violation
			return users.ErrUnknownPrinter
		case pgErr.ConstraintName == "uq_user_printers_one_owner_per_printer",
			pgErr.ConstraintName == "uq_user_printers_one_printer_per_owner":
			return users.ErrPrinterOwnershipViolation
		case pgErr.ConstraintName == "user_printers_pkey":
			return users.ErrConflictingRoles
		}
	}
	return err
}

func revokeAccessIfNoPrinters(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	// If the user has no more printer memberships, revoke all their sessions.
	_, err := tx.Exec(
		ctx,
		`UPDATE user_sessions
		SET revoked_at = NOW()
		WHERE revoked_at IS NULL
			AND user_id = $1
			AND NOT EXISTS(SELECT 1 FROM user_printers WHERE user_id = $1)`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}

	// If the user has no more printer memberships, clear their invite hash and expiration.
	_, err = tx.Exec(ctx,
		`UPDATE users
		SET invite_hash = NULL, invite_expires_at = NULL
		WHERE id = $1
		AND invite_hash IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM user_printers WHERE user_id = $1)`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("clear invite: %w", err)
	}
	return nil
}
