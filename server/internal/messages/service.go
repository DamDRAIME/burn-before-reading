package messages

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/google/uuid"

	"bbr/internal/moderation"
	"bbr/internal/printers"
	"bbr/internal/validate"
)

var (
	ErrEmptyContent   = errors.New("content cannot be empty")
	ErrContentTooLong = errors.New("content exceeds maximum length")

	ErrNotFound      = errors.New("message not found")
	ErrInvalidStatus = errors.New("operation not allowed for the current message status")

	ErrInvalidStatusFilter = errors.New("invalid status filter")
	ErrInvalidOrder        = errors.New("invalid order")

	ErrMissingSender = errors.New("sender ID is required")

	ErrInQuietHours = errors.New("operation not allowed during quiet hours")
)

const (
	MaxContentLength     = 500
	ContentAllowNewLines = true
	ContentMaxNewLines   = 10
)

type PrinterGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*printers.Printer, error)
}

type PrinterAccessChecker interface {
	CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error)
}

type Classifier interface {
	Classify(ctx context.Context, content string) (moderation.LLMResult, error)
}

type Service struct {
	store             Store
	printers          PrinterGetter
	access            PrinterAccessChecker
	moderationEnabled atomic.Bool
	classifier        Classifier
	reviewWake        chan struct{}
	now               func() time.Time
}

func NewService(store Store, printers PrinterGetter, access PrinterAccessChecker, classifier Classifier) *Service {
	s := Service{
		store:      store,
		printers:   printers,
		access:     access,
		classifier: classifier,
		reviewWake: make(chan struct{}, 1), // buffered: Submit never waits on it
		now:        time.Now,
	}
	s.moderationEnabled.Store(true)
	return &s
}

// -- Methods ----

type SubmitParams struct {
	PrinterID uuid.UUID
	SenderID  uuid.UUID
	SenderIP  *net.IP
	Source    Source
	Content   string
}

func (s *Service) Submit(ctx context.Context, params SubmitParams) (*Message, error) {
	if params.SenderID == uuid.Nil {
		return nil, ErrMissingSender
	}
	if params.Source != SourceAdmin {
		canSend, err := s.access.CanSend(ctx, params.SenderID, params.PrinterID)
		if err != nil {
			return nil, err
		}
		if !canSend {
			return nil, printers.ErrNotFound
		}
	}

	sanitized_content := sanitizeString(params.Content, ContentAllowNewLines, ContentMaxNewLines)
	content, err := validate.RequiredString(sanitized_content, MaxContentLength, ErrEmptyContent, ErrContentTooLong, ContentAllowNewLines)
	if err != nil {
		return nil, err
	}

	status := StatusAwaitingReview
	if params.Source == SourceAdmin {
		status = StatusPending
	}

	messageParams := CreateParams{
		PrinterID: params.PrinterID,
		SenderID:  params.SenderID,
		SenderIP:  params.SenderIP,
		Source:    params.Source,
		Content:   content,
		Status:    status,
	}

	msg, err := s.store.Create(ctx, messageParams)
	if err != nil {
		return nil, err
	}

	// Wake up the Review Worker
	if msg.Status == StatusAwaitingReview {
		select {
		case s.reviewWake <- struct{}{}:
		default:
		}
	}

	return msg, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) GetForSender(ctx context.Context, id, senderID uuid.UUID) (*Message, error) {
	return s.store.GetForSender(ctx, id, senderID)
}

func (s *Service) List(ctx context.Context, senderID *uuid.UUID, rawStatuses []string, rawOrder string, limit, offset int) ([]*Message, error) {
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
		SenderID: senderID,
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
	errorMsg = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1 // drop bad characters
	}, errorMsg)
	errorMsg = strings.TrimSpace(errorMsg)
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

// -- Review related ----

func (s *Service) review(ctx context.Context, msg *Message) error {
	if !s.moderationEnabled.Load() {
		flags := []moderation.Flag{{Code: moderation.ModerationDisabled, Detail: "moderation disabled by admin"}}
		_, err := s.store.ApplyReview(ctx, msg.ID, StatusPending, flags)
		if errors.Is(err, ErrInvalidStatus) {
			return nil
		}
		return err
	}

	flags := moderation.CheckRules(msg.Content)
	to := StatusAwaitingApproval
	if len(flags) == 0 {
		res, err := s.classifier.Classify(ctx, msg.Content)
		if err != nil {
			flags = []moderation.Flag{{Code: moderation.LLMUnavailable, Detail: err.Error()}}
		} else if res.Verdict == moderation.VerdictSafe {
			to = StatusPending
		} else {
			detail := "LLM classifier flagged content as " + string(res.Verdict) + ", with categories: " + strings.Join(res.Categories, " / ")
			flags = []moderation.Flag{{Code: moderation.LLMFlagged, Detail: detail}}
		}
	}
	_, err := s.store.ApplyReview(ctx, msg.ID, to, flags)
	if errors.Is(err, ErrInvalidStatus) {
		return nil // Admin acted first
	}
	return err
}

func (s *Service) SetModerationEnabled(ctx context.Context, enabled bool) {
	s.moderationEnabled.Store(enabled)

	// Wake up the Review Worker
	select {
	case s.reviewWake <- struct{}{}:
	default:
	}
}

func (s *Service) ModerationEnabled() bool {
	return s.moderationEnabled.Load()
}

// -- Automated review ----

// RunReviewWorker moves web messages out of awaiting_review by running the
// moderation rules and the classifier on them. It drains the queue once at
// startup (messages left over from a restart), then again whenever Submit
// signals reviewWake or interval passes, until ctx is cancelled.
func (s *Service) RunReviewWorker(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		s.drainReviewQueue(ctx, logger)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C: // safety net, e.g. a wake-up was missed
		case <-s.reviewWake: // a new message was submitted
		}
	}
}

// drainReviewQueue reviews awaiting_review messages oldest first until none
// are left. It stops at the first store error, so a database outage doesn't
// become a tight retry loop; the next wake-up or tick tries again.
func (s *Service) drainReviewQueue(ctx context.Context, logger *slog.Logger) {
	for ctx.Err() == nil {
		next, err := s.store.List(ctx, ListParams{
			Statuses: []Status{StatusAwaitingReview},
			Order:    OrderAscending,
			Limit:    1,
		})
		if err != nil {
			if ctx.Err() == nil { // don't log the cancellation itself on shutdown
				logger.Error("list messages awaiting review", "error", err)
			}
			return
		}
		if len(next) == 0 {
			return // queue empty
		}

		if err := s.review(ctx, next[0]); err != nil {
			logger.Error("review message", "id", next[0].ID, "error", err)
			return
		}
	}
}
