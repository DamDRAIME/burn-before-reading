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

	ListMessages(ctx context.Context, limit int, offset int) ([]*Message, error)
}

type CreateMessageParams struct {
	SenderName string
	SenderIP   *net.IP
	Source     Source
	Content    string
	Status     Status
}
