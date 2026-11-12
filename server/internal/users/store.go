package users

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, params CreateParams) (*User, error)

	Get(ctx context.Context, userID uuid.UUID) (*User, error)

	List(ctx context.Context, limit, offset int) ([]*User, error)

	SetPrinters(ctx context.Context, userID uuid.UUID, printerIDs []uuid.UUID) (*User, error)

	SetInvite(ctx context.Context, userID uuid.UUID, inviteHash []byte, inviteExpiresAt time.Time) (*User, error)

	ClaimInvite(ctx context.Context, inviteHash, sessionHash []byte, deviceName string) (uuid.UUID, uuid.UUID, error)

	GetUserIDBySessionHash(ctx context.Context, hash []byte) (uuid.UUID, error)

	ListSessions(ctx context.Context, userID uuid.UUID) ([]*Session, error)

	RevokeSession(ctx context.Context, sessionID uuid.UUID) error

	RevokeAllSessions(ctx context.Context, userID uuid.UUID) error

	CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error)
}

type CreateParams struct {
	Name            string
	PrinterIDs      []uuid.UUID
	InviteHash      []byte
	InviteExpiresAt time.Time
}
