package printers

import (
	"time"

	"github.com/google/uuid"
)

type Printer struct {
	ID uuid.UUID

	Model string

	RecipientName string

	QuietStart *TimeOfDay
	QuietEnd   *TimeOfDay
	Timezone   *string

	Error *string

	CreatedAt       time.Time
	LastUpdatedAt   time.Time
	LastHeartbeatAt *time.Time
}

type PrinterStats struct {
	ID              uuid.UUID
	MessagesClaimed int64      // messages claimed at least once (attempts > 0)
	MessagesPrinted int64      // status = printed
	MessagesFailed  int64      // status = failed
	PrintAttempts   int64      // SUM(attempts), retries included
	LastClaimedAt   *time.Time // MAX(claimed_at)
	LastPrintedAt   *time.Time // MAX(printed_at)
}

// InQuietHours reports whether now falls in [QuietStart, QuietEnd) in the printer's timezone.
// Handles ranges that cross midnight (e.g. 22:00–07:00).
func (p *Printer) InQuietHours(now time.Time) bool {
	if p.QuietStart == nil || p.QuietEnd == nil {
		return false
	}

	timezone := DefaultTimezone
	if p.Timezone != nil {
		timezone = *p.Timezone
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		// panic here because this should have been validated when the printer was created/updated
		panic(err)
	}

	now = now.In(loc)
	m := now.Hour()*60 + now.Minute()
	start := p.QuietStart.Hour*60 + p.QuietStart.Minute
	end := p.QuietEnd.Hour*60 + p.QuietEnd.Minute
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end // Quiet hours span midnight
}

// HeartbeatInterval is how often the Pi is expected to send a heartbeat (keep in sync with the Pi's config).
const HeartbeatInterval = 10 * time.Second

// OnlineTimeout: 2x the interval, so a single missed beat doesn't flip the status.
const OnlineTimeout = 2 * HeartbeatInterval

// IsOnline reports whether the printer's last heartbeat is within OnlineTimeout of now.
func (p *Printer) IsOnline(now time.Time) bool {
	return p.LastHeartbeatAt != nil && now.Sub(*p.LastHeartbeatAt) < OnlineTimeout
}
