package users

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleMember Role = "member"
	RoleOwner  Role = "owner"
)

func (r Role) IsValid() bool {
	return r == RoleMember || r == RoleOwner
}

type PrinterAccess struct {
	PrinterID uuid.UUID
	Role      Role
}

type User struct {
	ID uuid.UUID

	Name string

	PrinterAccesses []PrinterAccess

	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

type Session struct {
	ID     uuid.UUID
	UserID uuid.UUID `db:"user_id"`

	DeviceName string

	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type JoinToken struct {
	ID        uuid.UUID
	PrinterID uuid.UUID

	CreatedBy  *uuid.UUID // nil = Admin-generated token
	UsedBy     *uuid.UUID
	UsedByName *string

	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

type JoinTokenStatus string

const (
	JoinTokenPending JoinTokenStatus = "pending"
	JoinTokenUsed    JoinTokenStatus = "used"
	JoinTokenRevoked JoinTokenStatus = "revoked"
	JoinTokenExpired JoinTokenStatus = "expired"
)

func (t *JoinToken) Status(now time.Time) JoinTokenStatus {
	switch {
	case t.UsedAt != nil:
		return JoinTokenUsed
	case t.RevokedAt != nil:
		return JoinTokenRevoked
	case !now.Before(t.ExpiresAt):
		return JoinTokenExpired
	default:
		return JoinTokenPending
	}
}
