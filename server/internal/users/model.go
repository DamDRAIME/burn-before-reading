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
	Role Role
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
