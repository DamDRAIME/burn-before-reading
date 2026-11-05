package messages

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"bbr/internal/printers"
)

var (
	ErrEmptyContent   = errors.New("content cannot be empty")
	ErrContentTooLong = errors.New("content exceeds maximum length")

	ErrEmptySenderName   = errors.New("sender name cannot be empty")
	ErrSenderNameTooLong = errors.New("sender name exceeds maximum length")

	ErrNotFound      = errors.New("message not found")
	ErrInvalidStatus = errors.New("operation not allowed for the current message status")

	ErrInQuietHours = errors.New("operation not allowed during quiet hours")
)

const (
	MaxContentLength    = 500
	MaxSenderNameLength = 20
)

type PrinterGetter interface {
	GetPrinterByID(ctx context.Context, id uuid.UUID) (*printers.Printer, error)
}

type Service struct {
	store    Store
	printers PrinterGetter
	now      func() time.Time
}

func NewService(store Store, printers PrinterGetter) *Service {
	return &Service{store: store, printers: printers, now: time.Now}
}

// -- Submitting a new message ----

type SubmitMessageParams struct {
	PrinterID  uuid.UUID
	SenderName string
	SenderIP   *net.IP
	Source     Source
	Content    string
}

func (s *Service) SubmitMessage(ctx context.Context, params SubmitMessageParams) (*Message, error) {

	senderName := strings.TrimSpace(params.SenderName)
	if senderName == "" {
		return nil, ErrEmptySenderName
	}
	if utf8.RuneCountInString(senderName) > MaxSenderNameLength {
		return nil, ErrSenderNameTooLong
	}

	content := strings.TrimSpace(params.Content)
	if content == "" {
		return nil, ErrEmptyContent
	}
	if utf8.RuneCountInString(content) > MaxContentLength {
		return nil, ErrContentTooLong
	}

	messageParams := CreateMessageParams{
		PrinterID:  params.PrinterID,
		SenderName: senderName,
		SenderIP:   params.SenderIP,
		Source:     params.Source,
		Content:    content,
		Status:     StatusPending,
	}

	return s.store.CreateMessage(ctx, messageParams)
}

// -- Retrieving a message by ID ----

func (s *Service) GetMessageByID(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.GetMessageByID(ctx, id)
}

// -- Listing messages with pagination ----

func (s *Service) ListMessages(ctx context.Context, limit, offset int) ([]*Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 100 // default limit
	}
	if offset < 0 {
		offset = 0 // default offset
	}
	return s.store.ListMessages(ctx, limit, offset)
}

// -- Updating message status ----

func (s *Service) ApproveMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionMessageStatus(ctx, id, StatusAwaitingApproval, StatusPending)
}

func (s *Service) RejectMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionMessageStatus(ctx, id, StatusAwaitingApproval, StatusRejected)
}

func (s *Service) RequeueMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionMessageStatus(ctx, id, StatusFailed, StatusPending)
}

func (s *Service) PrintingMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionMessageStatus(ctx, id, StatusClaimed, StatusPrinting)
}

func (s *Service) PrintedMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionMessageStatus(ctx, id, StatusPrinting, StatusPrinted)
}

func (s *Service) FailedMessage(ctx context.Context, id uuid.UUID, errorMsg string) (*Message, error) {
	return s.store.FailedMessage(ctx, id, errorMsg)
}

// -- Claiming messages ----

func (s *Service) ClaimMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.ClaimMessage(ctx, id)
}

func (s *Service) ClaimOldestMessage(ctx context.Context, printerID uuid.UUID) (*Message, error) {
	p, err := s.printers.GetPrinterByID(ctx, printerID)
	if err != nil {
		return nil, err
	}

	if p.InQuietHours(s.now()) {
		return nil, ErrInQuietHours
	}
	return s.store.ClaimOldestMessage(ctx, printerID)
}
