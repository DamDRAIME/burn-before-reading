package messages

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	CreateMessage(ctx context.Context, params CreateMessageParams) (*Message, error)

	GetMessageByID(ctx context.Context, id uuid.UUID) (*Message, error)

	TransitionMessageStatus(ctx context.Context, id uuid.UUID, from, to Status) (*Message, error)

	ListMessages(ctx context.Context, params ListMessagesParams) ([]*Message, error)

	MarkAsFailed(ctx context.Context, id uuid.UUID, errorMsg string) (*Message, error)

	ClaimOldestMessage(ctx context.Context, printerID uuid.UUID) (*Message, error)

	ReleaseStaleClaims(ctx context.Context, timeout time.Duration) (int64, error)
}

type CreateMessageParams struct {
	PrinterID  uuid.UUID
	SenderName string
	SenderIP   *net.IP
	Source     Source
	Content    string
	Status     Status
}

type ListMessagesParams struct {
	Statuses []Status
	Order  Order
	Limit  int
	Offset int
}