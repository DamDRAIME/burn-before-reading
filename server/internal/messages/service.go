package messages

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	ErrInvalidStatusFilter = errors.New("invalid status filter")
	ErrInvalidOrder        = errors.New("invalid order")

	ErrInQuietHours = errors.New("operation not allowed during quiet hours")
)

const (
	MaxContentLength    = 500
	MaxSenderNameLength = 20
)

type PrinterGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*printers.Printer, error)
}

type Service struct {
	store    Store
	printers PrinterGetter
	now      func() time.Time
}

func NewService(store Store, printers PrinterGetter) *Service {
	return &Service{store: store, printers: printers, now: time.Now}
}

// -- Methods ----

type SubmitParams struct {
	PrinterID  uuid.UUID
	SenderName string
	SenderIP   *net.IP
	Source     Source
	Content    string
}

func (s *Service) Submit(ctx context.Context, params SubmitParams) (*Message, error) {

	senderName := strings.TrimSpace(sanitizeString(params.SenderName, false))
	if senderName == "" {
		return nil, ErrEmptySenderName
	}
	if utf8.RuneCountInString(senderName) > MaxSenderNameLength {
		return nil, ErrSenderNameTooLong
	}

	content := strings.TrimSpace(sanitizeString(params.Content, true))
	if content == "" {
		return nil, ErrEmptyContent
	}
	if utf8.RuneCountInString(content) > MaxContentLength {
		return nil, ErrContentTooLong
	}

	status := StatusAwaitingApproval
	if params.Source == SourceAdmin {
		status = StatusPending
	}

	messageParams := CreateParams{
		PrinterID:  params.PrinterID,
		SenderName: senderName,
		SenderIP:   params.SenderIP,
		Source:     params.Source,
		Content:    content,
		Status:     status,
	}

	return s.store.Create(ctx, messageParams)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, rawStatuses []string, rawOrder string, limit, offset int) ([]*Message, error) {
	if limit <= 0 || limit > 10 {
		limit = 10 // default limit
	}
	if offset < 0 {
		offset = 0 // default offset
	}

	order := OrderDescending
	if raw := strings.ToLower(strings.TrimSpace(rawOrder)); raw != "" {
		var ok bool
		if order, ok = ParseOrder(raw); !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidOrder, rawOrder)
		}
	}

	var statuses []Status
	for _, rawStatus := range rawStatuses {
		status, ok := ParseStatus(strings.TrimSpace(rawStatus))
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidStatusFilter, rawStatus)
		}
		statuses = append(statuses, status)
	}

	return s.store.List(ctx, ListParams{
		Statuses: statuses,
		Order:    order,
		Limit:    limit,
		Offset:   offset,
	})
}

func (s *Service) Approve(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionStatus(ctx, id, StatusAwaitingApproval, StatusPending)
}

func (s *Service) Reject(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionStatus(ctx, id, StatusAwaitingApproval, StatusRejected)
}

func (s *Service) Requeue(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.TransitionStatus(ctx, id, StatusFailed, StatusPending)
}

func (s *Service) MarkAsProcessed(ctx context.Context, id, printerID uuid.UUID) (*Message, error) {
	return s.store.TransitionStatusForPrinter(ctx, id, StatusClaimed, StatusProcessed, printerID)
}

func (s *Service) MarkAsPrinted(ctx context.Context, id, printerID uuid.UUID) (*Message, error) {
	return s.store.TransitionStatusForPrinter(ctx, id, StatusProcessed, StatusPrinted, printerID)
}

func (s *Service) MarkAsFailed(ctx context.Context, id, printerID uuid.UUID, errorMsg string) (*Message, error) {
	return s.store.MarkAsFailed(ctx, id, printerID, errorMsg)
}

func (s *Service) ClaimOldest(ctx context.Context, printerID uuid.UUID) (*Message, error) {
	p, err := s.printers.Get(ctx, printerID)
	if err != nil {
		return nil, err
	}

	if p.InQuietHours(s.now()) {
		return nil, ErrInQuietHours
	}
	return s.store.ClaimOldest(ctx, printerID)
}

// -- Recovering stuck claims ----

// RunClaimReaper periodically returns messages whose claim is older than claimTimeout
// to the queue (or to failed after too many attempts), e.g. when a printer died mid-job.
// It runs once immediately, to recover claims left over from before a restart,
// then every interval until ctx is cancelled.
func (s *Service) RunClaimReaper(ctx context.Context, claimTimeout, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		n, err := s.store.ReleaseStaleClaims(ctx, claimTimeout)
		switch {
		case ctx.Err() != nil:
			return // shutting down; the error, if any, is just the cancellation
		case err != nil:
			logger.Error("release stale claims", "error", err)
		case n > 0:
			logger.Info("released stale claims", "count", n)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
