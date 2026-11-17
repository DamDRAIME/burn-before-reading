package users

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, params CreateParams) (*User, error)

	RemoveMember(ctx context.Context, printerID, userID uuid.UUID) error

	Get(ctx context.Context, userID uuid.UUID) (*User, error)

	List(ctx context.Context, limit, offset int) ([]*User, error)

	ListMembers(ctx context.Context, printerID uuid.UUID) ([]*User, error)

	SetPrinters(ctx context.Context, userID uuid.UUID, printerAccesses []PrinterAccess) (*User, error)

	SetOwner(ctx context.Context, userID, printerID uuid.UUID) (*User, error)

	SetInvite(ctx context.Context, userID uuid.UUID, inviteHash []byte, inviteExpiresAt time.Time) (*User, error)

	ClaimInvite(ctx context.Context, inviteHash, sessionHash []byte, deviceName string) (uuid.UUID, uuid.UUID, error)

	GetUserIDBySessionHash(ctx context.Context, hash []byte) (uuid.UUID, uuid.UUID, error)

	ListSessions(ctx context.Context, userID uuid.UUID) ([]*Session, error)

	RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error

	RevokeAllSessions(ctx context.Context, userID uuid.UUID) error

	CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error)

	IsOwner(ctx context.Context, userID, printerID uuid.UUID) (bool, error)
}

type CreateParams struct {
	Name            string
	PrinterAccesses []PrinterAccess
	InviteHash      []byte
	InviteExpiresAt time.Time
}
