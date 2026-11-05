package messages

import (
	"context"
	"net"

	"github.com/google/uuid"
)

type Store interface {
	CreateMessage(ctx context.Context, params CreateMessageParams) (*Message, error)

	GetMessageByID(ctx context.Context, id uuid.UUID) (*Message, error)

	TransitionMessageStatus(ctx context.Context, id uuid.UUID, from, to Status) (*Message, error)

	ListMessages(ctx context.Context, limit, offset int) ([]*Message, error)

	MarkAsFailed(ctx context.Context, id uuid.UUID, errorMsg string) (*Message, error)

	ClaimMessage(ctx context.Context, id uuid.UUID) (*Message, error)

	ClaimOldestMessage(ctx context.Context, printer_id uuid.UUID) (*Message, error)
}

type CreateMessageParams struct {
	PrinterID  uuid.UUID
	SenderName string
	SenderIP   *net.IP
	Source     Source
	Content    string
	Status     Status
}
