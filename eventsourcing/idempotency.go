package eventsourcing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProcessedEvent records that a durable consumer has claimed, and then
// completed, an event.
//
// ProcessedAt is when the CURRENT claim was taken (a lease takeover moves it).
// CompletedAt is nil while the claim is in flight and set once the handler
// succeeded (NIAGA-357). Rows written by CheckAndMark, the older API, are born
// completed, because to that API a claim and a completion are the same thing.
type ProcessedEvent struct {
	EventID      string     `gorm:"primaryKey;size:100;not null" json:"event_id"`
	ConsumerName string     `gorm:"primaryKey;size:150;not null" json:"consumer_name"`
	ProcessedAt  time.Time  `gorm:"not null" json:"processed_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// TableName returns the shared idempotency table name.
func (ProcessedEvent) TableName() string {
	return "events.processed"
}

// IdempotencyChecker provides per-consumer event deduplication.
type IdempotencyChecker struct {
	db *gorm.DB
}

// NewIdempotencyChecker creates a Postgres-backed idempotency checker.
func NewIdempotencyChecker(db *gorm.DB) *IdempotencyChecker {
	return &IdempotencyChecker{db: db}
}

// CheckAndMark inserts an event/consumer pair if it has not been processed.
// It returns true when the caller should process the event, and false when the
// event is a duplicate that should be acked and skipped.
//
// THE ROW IS WRITTEN BEFORE THE HANDLER RUNS. That is deliberate — it is what
// stops two concurrent deliveries of the same event from both being processed —
// but it means the row is a CLAIM on the event, not proof that the work is done.
// A handler that FAILS must give the claim back with Release, or the redelivery
// is treated as a duplicate and acked without ever running. See Release.
//
// NIAGA-357: a consumer that died between this call and Release left a claim
// that looked like a completed event forever, so its redelivery was acked
// without running. New callers use Claim and Complete, which tell the two
// apart. This keeps its old meaning — the row is born completed — so a caller
// that has not moved yet behaves exactly as before.
func (c *IdempotencyChecker) CheckAndMark(ctx context.Context, eventID, consumerName string) (bool, error) {
	now := time.Now()
	row := &ProcessedEvent{
		EventID:      eventID,
		ConsumerName: consumerName,
		ProcessedAt:  now,
		CompletedAt:  &now,
	}

	result := c.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(row)
	if result.Error != nil {
		return false, result.Error
	}

	return result.RowsAffected == 1, nil
}

// Release gives back the claim CheckAndMark took, so a later delivery of the same
// event is processed instead of being skipped as a duplicate.
//
// NIAGA-263. Without this, retry and dead-lettering were both dead for every
// consumer using this type, and silently: the handler returned an error, the
// consumer NAKed, JetStream redelivered, CheckAndMark reported "already
// processed", and the message was ACKED WITHOUT THE HANDLER RUNNING. Because the
// redelivery never reached the handler, NumDelivered never climbed to the
// max-deliver threshold either, so the DLQ branch was unreachable as well. The
// consumer logged "Handler error, will retry" — a promise the code could not
// keep, which is what made it invisible in operation.
//
// CALL IT ON THE HANDLER'S ERROR PATH, BEFORE NAKING — and NOT when routing to
// the DLQ. An event that has gone to the DLQ is finished, and its claim is what
// stops it being picked up again.
//
// Releasing is deliberately a delete rather than a status flag: the table means
// "these events are done or in flight", and the smallest correct change is to
// stop lying about the failed ones.
//
// THE HOLES, and which are closed. Holes 1 and 2 are closed for a caller that
// uses Claim/Complete (NIAGA-357; all four consumers do, as of 2026-09-24). They
// remain open ONLY for a caller still on CheckAndMark:
//
//  1. A process that dies between claiming and releasing strands that one event.
//     CLOSED BY Claim: an uncompleted claim is a lease, taken over once it runs
//     out, so the redelivery runs instead of being acked as a duplicate.
//  2. AckWait expiring while the handler is still running. CLOSED BY Claim: the
//     lease (DefaultClaimLease, 4x AckWait) makes that redelivery ClaimInProgress,
//     which is nak'd, never acked. What follows describes the CheckAndMark case. The redelivery finds
//     the claim, acks, and terminates a message the first delivery is still
//     working on; when that one then fails, Release deletes the claim and the NAK
//     lands on an already-acked message. The event is lost, never reaches the
//     DLQ, and now leaves no row behind either -- previously the stale row was at
//     least a trace. This predates NIAGA-263 and is not fixed by it.
//  3. RouteToDLQ itself failing. The caller returns without acking or releasing,
//     and NumDelivered is already at MaxDeliver, so nothing redelivers. It writes
//     the events.failed row BEFORE terminating the message, so that error does not
//     mean the row is missing -- check events.failed before calling such an event
//     lost.
//
// REPLAYING FROM THE DLQ NEEDS THE CLAIM DELETED FIRST. Republishing a
// dead-lettered event with the same Nats-Msg-Id will find the retained row, be
// acked and skipped, silently. No replay tooling exists in this workspace today;
// whoever writes it must delete the events.processed row as part of the replay.
//
// A delete affecting 0 rows is not treated as an error: callers only reach here
// after CheckAndMark returned true, so 0 means something else removed the claim,
// which is anomalous but not this function's business to fail on.
func (c *IdempotencyChecker) Release(ctx context.Context, eventID, consumerName string) error {
	return c.db.WithContext(ctx).
		Where("event_id = ? AND consumer_name = ?", eventID, consumerName).
		Delete(&ProcessedEvent{}).Error
}

// ClaimResult is what Claim found, and so what the consumer must do.
type ClaimResult int

const (
	// ClaimAcquired: this delivery owns the event. Run the handler, then
	// Complete on success or Release on failure.
	ClaimAcquired ClaimResult = iota + 1
	// ClaimCompleted: the event was already handled. Ack and skip; this is the
	// only answer that may ack without running the handler.
	ClaimCompleted
	// ClaimInProgress: another delivery holds a live lease. Do NOT ack — that is
	// the NIAGA-357 bug. Nak with a delay of at least RetryAfter, so the next
	// delivery lands after the lease has either been completed or expired.
	ClaimInProgress
)

func (r ClaimResult) String() string {
	switch r {
	case ClaimAcquired:
		return "acquired"
	case ClaimCompleted:
		return "completed"
	case ClaimInProgress:
		return "in_progress"
	}
	return fmt.Sprintf("ClaimResult(%d)", int(r))
}

// DefaultClaimLease is how long a claim protects an event before another
// delivery may take it over. It is 4x the consumers' 30 s AckWait: long enough
// that a slow but living handler is not raced by its own redelivery (which is
// hole 2 in Release's comment), short enough that a crashed consumer's event is
// retried within a couple of redeliveries, well inside MaxDeliver 5.
const DefaultClaimLease = 2 * time.Minute

// ErrNoClaim is returned by Complete when there is no in-flight claim to
// complete: it was never taken, it was released, or it was already completed.
var ErrNoClaim = errors.New("no in-flight idempotency claim to complete")

// Claim takes an expiring lease on an event for one consumer (NIAGA-357).
//
// One statement decides it, so two deliveries racing each other cannot both
// win: insert a new in-flight claim, or take over one whose lease has run out,
// or do nothing. When it does nothing, a second read says why — completed, or
// someone else's live lease — and RetryAfter says how long that lease has left.
//
// A lease that runs out is taken over, not honoured forever: that is how a
// consumer that died mid-handler gets its event retried instead of silently
// acked. The price is that a handler which outlives the lease can run twice.
// For this platform's consumers that is the right side to err on: they are
// idempotent at the business level (stock moves are keyed on order, emails are
// at-least-once), and a duplicate is visible where a lost event is not.
func (c *IdempotencyChecker) Claim(ctx context.Context, eventID, consumerName string, lease time.Duration) (ClaimResult, time.Duration, error) {
	if lease <= 0 {
		lease = DefaultClaimLease
	}
	now := time.Now()
	stale := now.Add(-lease)

	res := c.db.WithContext(ctx).Exec(`
		INSERT INTO events.processed (event_id, consumer_name, processed_at, completed_at)
		VALUES (?, ?, ?, NULL)
		ON CONFLICT (event_id, consumer_name) DO UPDATE
		   SET processed_at = EXCLUDED.processed_at
		 WHERE events.processed.completed_at IS NULL
		   AND events.processed.processed_at < ?`,
		eventID, consumerName, now, stale)
	if res.Error != nil {
		return 0, 0, res.Error
	}
	if res.RowsAffected == 1 {
		return ClaimAcquired, 0, nil
	}

	var row ProcessedEvent
	err := c.db.WithContext(ctx).
		Where("event_id = ? AND consumer_name = ?", eventID, consumerName).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Released between the two statements. Nothing holds it now; the next
		// delivery will acquire it, so ask for a short retry rather than guess.
		return ClaimInProgress, time.Second, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if row.CompletedAt != nil {
		return ClaimCompleted, 0, nil
	}
	retry := row.ProcessedAt.Add(lease).Sub(now)
	if retry < time.Second {
		retry = time.Second
	}
	return ClaimInProgress, retry, nil
}

// Complete marks a claimed event as handled, so every later delivery is acked
// as a duplicate. Call it after the handler succeeded and before acking.
//
// It completes only an IN-FLIGHT claim. ErrNoClaim means there was none: the
// caller's lease was taken over and the other delivery finished first, or the
// claim was released. The work has still been done, so the caller should log
// and ack rather than retry it.
func (c *IdempotencyChecker) Complete(ctx context.Context, eventID, consumerName string) error {
	res := c.db.WithContext(ctx).
		Model(&ProcessedEvent{}).
		Where("event_id = ? AND consumer_name = ? AND completed_at IS NULL", eventID, consumerName).
		Update("completed_at", time.Now())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNoClaim
	}
	return nil
}
