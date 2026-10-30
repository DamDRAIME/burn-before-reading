package messages

import (
	"net"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusPending          Status = "pending"
	StatusPrinting         Status = "printing"
	StatusPrinted          Status = "printed"
	StatusRejected         Status = "rejected"
	StatusFailed           Status = "failed"
)

type Source string

const (
	SourceAdmin  Source = "admin"
	SourceDirect Source = "direct"
	SourceWeb    Source = "web"
)

type Message struct {
	ID uuid.UUID

	SenderName string
	SenderIP   *net.IP
	Source     Source

	Content string

	Status Status

	CreatedAt  time.Time
	ApprovedAt *time.Time
	ClaimedAt  *time.Time
	PrintedAt  *time.Time

	Attempts  int
	LastError *string

	PrinterID *uuid.UUID
}
