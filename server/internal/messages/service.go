package messages

import (
	"context"
	"errors"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrEmptyContent   = errors.New("content cannot be empty")
	ErrContentTooLong = errors.New("content exceeds maximum length")

	ErrEmptySenderName   = errors.New("sender name cannot be empty")
	ErrSenderNameTooLong = errors.New("sender name exceeds maximum length")

	ErrMessageNotFound = errors.New("message not found")
	ErrInvalidStatus   = errors.New("operation not allowed for the current message status")
)

const (
	MaxContentLength    = 500
	MaxSenderNameLength = 20
)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// -- Submitting a new message ----

type SubmitMessageParams struct {
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

func (s *Service) ListMessages(ctx context.Context, limit int, offset int) ([]*Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 10 // default limit
	}
	if offset < 0 {
		offset = 0 // default offset
	}
	return s.store.ListMessages(ctx, limit, offset)
}

// -- Updating message status ----

func (s *Service) ApproveMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	msg, err := s.store.GetMessageByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if msg.Status != StatusAwaitingApproval {
		return nil, ErrInvalidStatus
	}

	return s.store.UpdateMessageStatus(ctx, id, StatusPending)
}
