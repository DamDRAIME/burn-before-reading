package messages

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, params CreateParams) (*Message, error)

	Get(ctx context.Context, id uuid.UUID) (*Message, error)

	GetForSender(ctx context.Context, id, senderID uuid.UUID) (*Message, error)

	TransitionStatus(ctx context.Context, id uuid.UUID, from, to Status) (*Message, error)

	TransitionStatusForPrinter(ctx context.Context, id uuid.UUID, from, to Status, printerID uuid.UUID) (*Message, error)

	List(ctx context.Context, params ListParams) ([]*Message, error)

	MarkAsFailed(ctx context.Context, id, printerID uuid.UUID, errorMsg string) (*Message, error)

	ClaimOldest(ctx context.Context, printerID uuid.UUID) (*Message, error)

	ReleaseStaleClaims(ctx context.Context, timeout time.Duration) (int64, error)
}

type CreateParams struct {
	PrinterID uuid.UUID
	SenderID  *uuid.UUID
	SenderIP  *net.IP
	Source    Source
	Content   string
	Status    Status
}

type ListParams struct {
	SenderID *uuid.UUID
	Statuses []Status
	Order    Order
	Limit    int
	Offset   int
}
