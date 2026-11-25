package messages

import (
	"bbr/internal/moderation"
	"net"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusAwaitingReview   Status = "awaiting_review"
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusPending          Status = "pending"
	StatusClaimed          Status = "claimed"
	StatusProcessed        Status = "processed"
	StatusPrinted          Status = "printed"
	StatusRejected         Status = "rejected"
	StatusFailed           Status = "failed"
)

var validStatuses = map[Status]bool{
	StatusAwaitingReview:   true,
	StatusAwaitingApproval: true,
	StatusPending:          true,
	StatusClaimed:          true,
	StatusProcessed:        true,
	StatusPrinted:          true,
	StatusRejected:         true,
	StatusFailed:           true,
}

func ParseStatus(value string) (Status, bool) {
	status := Status(value)
	return status, validStatuses[status]
}

type Source string

const (
	SourceAdmin Source = "admin"
	SourceWeb   Source = "web"
)

type Message struct {
	ID             uuid.UUID
	SequenceNumber int64

	SenderName string
	SenderIP   *net.IP
	Source     Source

	Content string

	Status Status

	ModerationFlags []moderation.Flag
	ModeratedBy     *moderation.Moderator

	CreatedAt     time.Time
	ModeratedAt   *time.Time
	ClaimedAt     *time.Time
	ProcessedAt   *time.Time
	PrintedAt     *time.Time
	LastUpdatedAt time.Time

	Attempts  int
	LastError *string

	PrinterID uuid.UUID
}

type Order string

const (
	OrderAscending  Order = "asc"
	OrderDescending Order = "desc"
)

func ParseOrder(value string) (Order, bool) {
	order := Order(value)
	return order, order == OrderAscending || order == OrderDescending
}
