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

// Implemented by both *pgxpool.Pool and pgx.Tx
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Lock order, to avoid deadlocks between current transactions:
// printers row (in ID order) --> users row --> user_printers / printer_join_tokens row

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

const joinTokenColumns = `
	t.id,
	t.printer_id,
	t.created_by,
	t.used_by,
	u.name,
	t.created_at,
	t.expires_at,
	t.used_at,
	t.revoked_at
`

// Is true for a join token (aliased t) that can still be redeemed: i.e. has not expired, been used, been revoked, and
// was created by Admin or a user who still owns the printer
const joinTokenValidCondition = `
	t.used_at IS NULL
	AND t.revoked_at IS NULL
	AND t.expires_at > NOW()
	AND (
		t.created_by IS NULL
		OR EXISTS (
			SELECT 1 FROM user_printers owner_link
			WHERE owner_link.user_id = t.created_by
				AND owner_link.printer_id = t.printer_id
				AND owner_link.role = 'owner'
		)
	)
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

func scanJoinToken(row pgx.Row) (users.JoinToken, error) {
	var t users.JoinToken
	err := row.Scan(
		&t.ID,
		&t.PrinterID,
		&t.CreatedBy,
		&t.UsedBy,
		&t.UsedByName,
		&t.CreatedAt,
		&t.ExpiresAt,
		&t.UsedAt,
		&t.RevokedAt,
	)
	return t, err
}

// -- Account ----

func (s *UserStore) Create(ctx context.Context, params users.CreateParams) (*users.User, error) {

	var u *users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		printerIDs := printerAccessesIDs(params.PrinterAccesses)
		if err := lockPrinters(ctx, tx, printerIDs, users.ErrUnknownPrinter); err != nil {
			return err
		}
		if err := ensureNameIsAvailableOnPrinters(ctx, tx, uuid.Nil, params.UserName, printerIDs); err != nil {
			return err
		}

		var userID uuid.UUID
		err := tx.QueryRow(
			ctx,
			`INSERT INTO users (name, invite_hash, invite_expires_at) VALUES ($1, $2, $3) RETURNING id`,
			params.UserName,
			params.InviteTokenHash,
			params.TokenExpiresAt,
		).Scan(&userID)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		if len(params.PrinterAccesses) > 0 {
			if err := insertUserPrinters(ctx, tx, userID, params.PrinterAccesses); err != nil {
				return err
			}
		}

		u, err = getUser(ctx, tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}

	return u, nil
}

func (s *UserStore) Get(ctx context.Context, id uuid.UUID) (*users.User, error) {
	return getUser(ctx, s.pool, id)
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

// -- Printer Links ----

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

func (s *UserStore) RevokeMembership(ctx context.Context, userID, printerID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPrinters(ctx, tx, []uuid.UUID{printerID}, users.ErrPrinterNotFound); err != nil {
			return err
		}
		revoked, err := revokeMembership(ctx, tx, userID, printerID)
		if err != nil {
			return err
		}
		if !revoked {
			return users.ErrMemberNotFound
		}
		return afterLinksRemoved(ctx, tx, userID, []uuid.UUID{printerID})
	})
}

func (s *UserStore) SelfRevokeMembership(ctx context.Context, userID, printerID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPrinters(ctx, tx, []uuid.UUID{printerID}, users.ErrMemberNotFound); err != nil {
			return err
		}
		revoked, err := revokeMembership(ctx, tx, userID, printerID)
		if err != nil {
			return err
		}
		if !revoked {
			owner, err := isOwner(ctx, tx, userID, printerID)
			if err != nil {
				return err
			}
			if owner {
				return users.ErrOwnerRoleImmutable
			}
			return users.ErrMemberNotFound
		}
		return afterLinksRemoved(ctx, tx, userID, []uuid.UUID{printerID})
	})
}

func (s *UserStore) SetPrinters(ctx context.Context, userID uuid.UUID, printerAccesses []users.PrinterAccess) (*users.User, error) {
	var u *users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		newIDs := printerAccessesIDs(printerAccesses)
		if err := lockPrinters(ctx, tx, newIDs, users.ErrUnknownPrinter); err != nil {
			return err
		}

		name, err := lockUser(ctx, tx, userID)
		if err != nil {
			return err
		}

		if err := ensureNameIsAvailableOnPrinters(ctx, tx, userID, name, newIDs); err != nil {
			return err
		}

		rows, err := tx.Query(
			ctx,
			`DELETE FROM user_printers WHERE user_id = $1 RETURNING printer_id`,
			userID,
		)
		if err != nil {
			return fmt.Errorf("delete user printers: %w", err)
		}
		oldIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return fmt.Errorf("delete user printers: %w", err)
		}

		if len(printerAccesses) > 0 {
			if err := insertUserPrinters(ctx, tx, userID, printerAccesses); err != nil {
				return err
			}
		}

		if err := rejectQueuedMessages(ctx, tx, userID, difference(oldIDs, newIDs)); err != nil {
			return err
		}
		if err := revokeAccessIfNoPrinters(ctx, tx, userID); err != nil {
			return err
		}
		if err := revokeStaleJoinTokens(ctx, tx, userID); err != nil {
			return err
		}

		u, err = getUser(ctx, tx, userID)
		return err

	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *UserStore) SetOwnership(ctx context.Context, userID, printerID uuid.UUID) (*users.User, error) {
	var u *users.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPrinters(ctx, tx, []uuid.UUID{printerID}, users.ErrPrinterNotFound); err != nil {
			return err
		}

		name, err := lockUser(ctx, tx, userID)
		if err != nil {
			return err
		}

		if err := ensureNameIsAvailableOnPrinters(ctx, tx, userID, name, []uuid.UUID{printerID}); err != nil {
			return err
		}

		// Demote the current owner, if it is someone else
		var previousOwner uuid.UUID
		err = tx.QueryRow(
			ctx,
			`UPDATE user_printers
			SET role = $1
			WHERE printer_id = $2
				AND role = $3
				AND user_id <> $4
			RETURNING user_id`,
			users.RoleMember,
			printerID,
			users.RoleOwner,
			userID,
		).Scan(&previousOwner)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No owner yet, or userID alread owns it
		case err != nil:
			return fmt.Errorf("demote owner: %w", err)
		default:
			if _, err := lockUser(ctx, tx, previousOwner); err != nil {
				return err
			}
			if err := revokeStaleJoinTokens(ctx, tx, previousOwner); err != nil {
				return err
			}
		}

		_, err = tx.Exec(
			ctx,
			`INSERT INTO user_printers (user_id, printer_id, role) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, printer_id) DO UPDATE SET role = EXCLUDED.role`,
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

		u, err = getUser(ctx, tx, userID)
		return err

	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *UserStore) DemoteOwner(ctx context.Context, printerID uuid.UUID, keepAsMember bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPrinters(ctx, tx, []uuid.UUID{printerID}, users.ErrPrinterNotFound); err != nil {
			return err
		}

		query := `DELETE FROM user_printers WHERE printer_id = $1 AND role = $2 RETURNING user_id`
		args := []any{printerID, users.RoleOwner}
		if keepAsMember {
			query = `UPDATE user_printers SET role = $3 WHERE printer_id = $1 AND role = $2 RETURNING user_id`
			args = append(args, users.RoleMember)
		}

		var ownerID uuid.UUID
		err := tx.QueryRow(ctx, query, args...).Scan(&ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return users.ErrOwnerNotFound
		}
		if err != nil {
			return fmt.Errorf("demote owner: %w", err)
		}

		if keepAsMember {
			if _, err := lockUser(ctx, tx, ownerID); err != nil {
				return err
			}
		} else if err := afterLinksRemoved(ctx, tx, ownerID, []uuid.UUID{printerID}); err != nil {
			return err
		}

		return revokeStaleJoinTokens(ctx, tx, ownerID)
	})
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
	return isOwner(ctx, s.pool, userID, printerID)
}

// -- Sessions ----

func (s *UserStore) GetUserIDBySessionHash(ctx context.Context, hash []byte) (uuid.UUID, uuid.UUID, error) {
	var userID uuid.UUID
	var sessionID uuid.UUID
	err := s.pool.QueryRow(
		ctx,
		`SELECT user_id, id FROM user_sessions WHERE revoked_at IS NULL AND token_hash = $1`,
		hash,
	).Scan(&userID, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return userID, sessionID, users.ErrInvalidSessionToken
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
		if err := ensureUserExists(ctx, s.pool, userID); err != nil {
			return nil, err
		}
	}

	return sessions, nil
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
		return ensureUserExists(ctx, s.pool, userID)
	}
	return nil
}

// -- Invite Tokens ----

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

		sessionID, err = insertSession(ctx, tx, userID, sessionHash, deviceName)
		return err
	})
	return userID, sessionID, err
}

func (s *UserStore) SetInvite(ctx context.Context, userID uuid.UUID, inviteHash []byte, inviteExpiresAt time.Time, revokeAllSessions bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// An invite only makes sense for a user who may send to at least one printer
		tag, err := tx.Exec(
			ctx,
			`UPDATE users
			SET invite_hash = $1, invite_expires_at = $2
			WHERE id = $3
				AND EXISTS (SELECT 1 FROM user_printers WHERE user_id = $3)`,
			inviteHash,
			inviteExpiresAt,
			userID,
		)
		if err != nil {
			return fmt.Errorf("set invite: %w", err)
		}
		if tag.RowsAffected() == 0 {
			if err := ensureUserExists(ctx, tx, userID); err != nil {
				return err
			}
			return users.ErrNoPrinters
		}

		if revokeAllSessions {
			_, err = tx.Exec(
				ctx,
				`UPDATE user_sessions SET revoked_at = NOW() WHERE revoked_at IS NULL and user_id = $1`,
				userID,
			)
			if err != nil {
				return fmt.Errorf("revoke sessions: %w", err)
			}
		}
		return nil
	})
}

// -- Join Tokens ----

func (s *UserStore) CreateJoinToken(ctx context.Context, params users.CreateJoinTokenParams) (*users.JoinToken, error) {
	row := s.pool.QueryRow(
		ctx,
		`INSERT INTO printer_join_tokens AS t (printer_id, created_by, token_hash, expires_at) VALUES ($1, $2, $3, $4)
		RETURNING t.id, t.printer_id, t.created_by, t.used_by, NULL::varchar, t.created_at, t.expires_at, t.used_at, t.revoked_at`,
		params.PrinterID,
		params.CreatedBy,
		params.JoinTokenHash,
		params.TokenExpiresAt,
	)
	t, err := scanJoinToken(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "printer_join_tokens_printer_id_fkey" {
			return nil, users.ErrPrinterNotFound
		}
		return nil, fmt.Errorf("create join token: %w", err)
	}
	return &t, nil
}

func (s *UserStore) ListJoinTokens(ctx context.Context, printerID uuid.UUID, limit, offset int) ([]*users.JoinToken, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT `+joinTokenColumns+`
		FROM printer_join_tokens t
		LEFT JOIN users u ON u.id = t.used_by
		WHERE t.printer_id = $1
		ORDER BY t.created_at DESC
		LIMIT $2 OFFSET $3`,
		printerID,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list join tokens: %w", err)
	}
	defer rows.Close()

	tokens, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*users.JoinToken, error) {
		t, err := scanJoinToken(row)
		return &t, err
	})
	if err != nil {
		return nil, fmt.Errorf("list join tokens: %w", err)
	}

	// If there is no token, check if this due to an unknown printer or a printer that doesn't have any
	if len(tokens) == 0 {
		var exists bool
		err := s.pool.QueryRow(
			ctx,
			`SELECT EXISTS (SELECT 1 FROM printers WHERE id = $1)`,
			printerID,
		).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("check printer exists: %w", err)
		}
		if !exists {
			return nil, users.ErrPrinterNotFound
		}
	}
	return tokens, nil
}

func (s *UserStore) RevokeJoinToken(ctx context.Context, printerID, tokenID uuid.UUID) error {
	var id uuid.UUID
	err := s.pool.QueryRow(
		ctx,
		`UPDATE printer_join_tokens
		SET revoked_at = COALESCE(revoked_at, NOW())
		WHERE id = $1
			AND printer_id = $2
			AND used_at IS NULL
		RETURNING id`,
		tokenID,
		printerID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.ErrJoinTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("revoke join token: %w", err)
	}
	return nil
}

func (s *UserStore) SignupWithJoinToken(ctx context.Context, params users.SignupParams) (uuid.UUID, uuid.UUID, error) {
	var userID uuid.UUID
	var sessionID uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		printerID, err := lockJoinTokenPrinter(ctx, tx, params.JoinTokenHash)
		if err != nil {
			return err
		}
		err = ensureNameIsAvailableOnPrinters(ctx, tx, uuid.Nil, params.UserName, []uuid.UUID{printerID})
		if err != nil {
			return err
		}

		err = tx.QueryRow(ctx, `INSERT INTO users (name) VALUEs ($1) RETURNING id`, params.UserName).Scan(&userID)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		// Any error from here on rolls back the transaction, so no orphan user is left behind
		if err := consumeJoinToken(ctx, tx, params.JoinTokenHash, userID); err != nil {
			return err
		}
		err = insertUserPrinters(ctx, tx, userID, []users.PrinterAccess{{PrinterID: printerID, Role: users.RoleMember}})
		if err != nil {
			return err
		}

		sessionID, err = insertSession(ctx, tx, userID, params.SessionTokenHash, params.DeviceName)
		return err
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return userID, sessionID, nil
}

func (s *UserStore) RedeemJoinToken(ctx context.Context, userID uuid.UUID, joinTokenHash []byte) (uuid.UUID, error) {
	var printerID uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		printerID, err = lockJoinTokenPrinter(ctx, tx, joinTokenHash)
		if err != nil {
			return err
		}

		name, err := lockUser(ctx, tx, userID)
		if err != nil {
			return err
		}

		var linked bool
		err = tx.QueryRow(
			ctx,
			`SELECT EXISTS (SELECT 1 FROM user_printers WHERE user_id = $1 AND printer_id = $2)`,
			userID,
			printerID,
		).Scan(&linked)
		if err != nil {
			return fmt.Errorf("check membership: %w", err)
		}

		if linked {
			return users.ErrAlreadyMember
		}

		if err := ensureNameIsAvailableOnPrinters(ctx, tx, userID, name, []uuid.UUID{printerID}); err != nil {
			return err
		}
		if err := consumeJoinToken(ctx, tx, joinTokenHash, userID); err != nil {
			return err
		}
		return insertUserPrinters(ctx, tx, userID, []users.PrinterAccess{{PrinterID: printerID, Role: users.RoleMember}})
	})
	if err != nil {
		return uuid.Nil, err
	}
	return printerID, nil
}

// -- Locks ----

// lockUser locks the user row, bumps last_updated_at (the user's printers are part of the user)
// and returns the user's name, or ErrUserNotFound
func lockUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	var name string
	err := tx.QueryRow(ctx, `UPDATE users SET last_updated_at = NOW() WHERE id = $1 RETURNING name`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", users.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock user: %w", err)
	}
	return name, nil
}

// lockPrinters locks the printer rows in ID order, which serializes membership changes per printer
// (needed for the per-printer name check). It returns notFoundErr when one of the printers doesn't exist.
// ids shouldn't contain duplicates.
func lockPrinters(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, notFoundErr error) error {
	if len(ids) == 0 {
		return nil
	}
	var n int
	err := tx.QueryRow(
		ctx,
		`SELECT count(*) FROM (SELECT id FROM printers WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE) locked`,
		ids,
	).Scan(&n)
	if err != nil {
		return fmt.Errorf("lock printers: %w", err)
	}
	if n != len(ids) {
		return notFoundErr
	}
	return nil
}

// lockJoinTokenPrinter finds the printer of a redeemable join token and locks it. The token is checked again
// when it is consumed
func lockJoinTokenPrinter(ctx context.Context, tx pgx.Tx, joinTokenHash []byte) (uuid.UUID, error) {
	var printerID uuid.UUID
	err := tx.QueryRow(
		ctx,
		`SELECT t.printer_id FROM printer_join_tokens t WHERE t.token_hash = $1 AND `+joinTokenValidCondition,
		joinTokenHash,
	).Scan(&printerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, users.ErrInvalidJoinToken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("find join token: %w", err)
	}
	if err := lockPrinters(ctx, tx, []uuid.UUID{printerID}, users.ErrInvalidJoinToken); err != nil {
		return uuid.Nil, err
	}
	return printerID, nil
}

// -- Helpers ----

func getUser(ctx context.Context, q queryRower, id uuid.UUID) (*users.User, error) {
	u, err := scanUser(q.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, users.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

// ensureUserExists returns ErrUserNotFound when no user has this ID
func ensureUserExists(ctx context.Context, q queryRower, id uuid.UUID) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check user exists: %w", err)
	}
	if !exists {
		return users.ErrUserNotFound
	}
	return nil
}

// ensureNameIsAvailableOnPrinters returns ErrNameAlreadyTaken when another user linked to one of the printers
// has the same name, ignoring case. Names are unique per printer, not globally.
// Callers must hold the printers' locks.
func ensureNameIsAvailableOnPrinters(ctx context.Context, tx pgx.Tx, excludeUserID uuid.UUID, name string, printerIDs []uuid.UUID) error {
	if len(printerIDs) == 0 {
		return nil
	}
	var taken bool
	err := tx.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1 FROM user_printers up
			JOIN users u ON u.id = up.user_id
			WHERE up.printer_id = ANY($1::uuid[])
				AND up.user_id <> $2
				AND lower(u.name) = lower($3)
		)`,
		printerIDs,
		excludeUserID,
		name,
	).Scan(&taken)
	if err != nil {
		return fmt.Errorf("check name availability on printers: %w", err)
	}
	if taken {
		return users.ErrNameAlreadyTaken
	}
	return nil
}

// consumeJoinToken marks a redeemable join token as used by userID or returns ErrInvalidJoinToken
func consumeJoinToken(ctx context.Context, tx pgx.Tx, joinTokenHash []byte, userID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE printer_join_tokens t
		SET used_at = NOW(), used_by = $1
		WHERE t.token_hash = $2
			AND `+joinTokenValidCondition,
		userID,
		joinTokenHash,
	)
	if err != nil {
		return fmt.Errorf("consume join token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return users.ErrInvalidJoinToken
	}
	return nil
}

// revokeStaleJoinTokens revokes the pending join tokens userID created for printers they no longer own.
// Reedeming already refuses them based on joinTokenValidCondition, this keeps the token lists accurate
func revokeStaleJoinTokens(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(
		ctx,
		`UPDATE printer_join_tokens t
		SET revoked_at = NOW()
		WHERE t.created_by = $1
			AND t.used_at IS NULL
			AND t.revoked_at IS NULL
			AND NOT EXISTS (
				SELECT 1 FROM user_printers up
				WHERE up.user_id = $1
					AND up.printer_id = t.printer_id
					AND up.role = 'owner'
			)`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("revoke stale join token: %w", err)
	}
	return nil
}

// rejectQueuedMessages rejects userID's messages to printerIDs that haven't been printed yet
func rejectQueuedMessages(ctx context.Context, tx pgx.Tx, userID uuid.UUID, printerIDs []uuid.UUID) error {
	if len(printerIDs) == 0 {
		return nil
	}

	_, err := tx.Exec(ctx,
		`UPDATE messages
		SET status = $1, moderated_at = NOW()
		WHERE sender_id = $2
			AND printer_id = ANY($3::uuid[])
			AND status = ANY($4)`,
		messages.StatusRejected,
		userID,
		printerIDs,
		[]messages.Status{messages.StatusPending, messages.StatusAwaitingApproval},
	)
	if err != nil {
		return fmt.Errorf("reject queued messages: %w", err)
	}
	return nil
}

func insertSession(ctx context.Context, tx pgx.Tx, userID uuid.UUID, sessionTokenHash []byte, deviceName string) (uuid.UUID, error) {
	var sessionID uuid.UUID
	err := tx.QueryRow(
		ctx,
		`INSERT INTO user_sessions (user_id, token_hash, device_name) VALUES ($1, $2, $3) RETURNING id`,
		userID,
		sessionTokenHash,
		deviceName,
	).Scan(&sessionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create session: %w", err)
	}
	return sessionID, nil
}

// insertUserPrinters links userID to printerIDs with the specified roles.
// Callers must make sure userID exists, so a foreign key violation means an unknown printer: ErrUnknownPrinter.
func insertUserPrinters(ctx context.Context, tx pgx.Tx, userID uuid.UUID, printerAccesses []users.PrinterAccess) error {
	printerIDs, roles := printerAccessesToArrays(printerAccesses)
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

func printerAccessesIDs(printerAccesses []users.PrinterAccess) []uuid.UUID {
	ids := make([]uuid.UUID, len(printerAccesses))
	for i, pa := range printerAccesses {
		ids[i] = pa.PrinterID
	}
	return ids
}

func printerAccessesToArrays(printerAccesses []users.PrinterAccess) ([]uuid.UUID, []string) {
	printerIDs := make([]uuid.UUID, len(printerAccesses))
	roles := make([]string, len(printerAccesses))
	for i, pa := range printerAccesses {
		printerIDs[i] = pa.PrinterID
		roles[i] = string(pa.Role)
	}
	return printerIDs, roles
}

// difference returns the IDs in a that are not in b
func difference(a, b []uuid.UUID) []uuid.UUID {
	inB := make(map[uuid.UUID]struct{}, len(b))
	for _, id := range b {
		inB[id] = struct{}{}
	}
	var out []uuid.UUID
	for _, id := range a {
		if _, ok := inB[id]; !ok {
			out = append(out, id)
		}
	}
	return out
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
			AND NOT EXISTS (SELECT 1 FROM user_printers WHERE user_id = $1)`,
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

func isOwner(ctx context.Context, q queryRower, userID, printerID uuid.UUID) (bool, error) {
	var owner bool
	err := q.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM user_printers WHERE user_id = $1 AND printer_id = $2 AND role = $3)`,
		userID,
		printerID,
		users.RoleOwner,
	).Scan(&owner)
	if err != nil {
		return false, fmt.Errorf("is owner check: %w", err)
	}
	return owner, nil
}

// revokeMembership removes a member (never owner) link and reports whether there was one
func revokeMembership(ctx context.Context, tx pgx.Tx, userID, printerID uuid.UUID) (bool, error) {
	tag, err := tx.Exec(
		ctx,
		`DELETE FROM user_printers WHERE user_id = $1 AND printer_id = $2 AND role = $3`,
		userID,
		printerID,
		users.RoleMember,
	)
	if err != nil {
		return false, fmt.Errorf("remove member: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func afterLinksRemoved(ctx context.Context, tx pgx.Tx, userID uuid.UUID, printerIDs []uuid.UUID) error {
	if _, err := lockUser(ctx, tx, userID); err != nil {
		return err
	}
	if err := rejectQueuedMessages(ctx, tx, userID, printerIDs); err != nil {
		return err
	}
	return revokeAccessIfNoPrinters(ctx, tx, userID)
}
