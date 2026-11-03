package printers

import (
	"time"

	"github.com/google/uuid"
)

type Printer struct {
	ID uuid.UUID

	Model string

	RecipientName string

	QuietStart *time.Time
	QuietEnd   *time.Time
	Timezone   *string

	Error *string

	CreatedAt  time.Time
	LastSeenAt time.Time
}

type PrinterStats struct {
	MessagesClaimed int64      // messages claimed at least once (attempts > 0)
	MessagesPrinted int64      // status = printed
	MessagesFailed  int64      // status = failed
	PrintAttempts   int64      // SUM(attempts), retries included
	LastClaimedAt   *time.Time // MAX(claimed_at)
	LastPrintedAt   *time.Time // MAX(printed_at)
}
