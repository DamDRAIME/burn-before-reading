package users

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID uuid.UUID

	Name string

	PrinterIDs []uuid.UUID

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
