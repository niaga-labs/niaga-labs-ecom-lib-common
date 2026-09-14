package outbox

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Event represents an outbox event to be published
type Event struct {
	ID            uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	AggregateType string          `gorm:"type:varchar(100);not null;index" json:"aggregate_type"`
	AggregateID   uuid.UUID       `gorm:"type:uuid;not null;index" json:"aggregate_id"`
	EventType     string          `gorm:"type:varchar(100);not null;index" json:"event_type"`
	Payload       json.RawMessage `gorm:"type:jsonb;not null" json:"payload"`
	CreatedAt     time.Time       `gorm:"not null;index" json:"created_at"`
	ProcessedAt   *time.Time      `gorm:"index" json:"processed_at,omitempty"`
	Error         *string         `gorm:"type:text" json:"error,omitempty"`
	RetryCount    int             `gorm:"default:0" json:"retry_count"`
}

// TableName specifies the table name for Event
func (Event) TableName() string {
	return "outbox.events"
}

// BeforeCreate hook to generate UUID and set created time
func (e *Event) BeforeCreate(tx *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	return nil
}

// Outbox provides transactional outbox pattern implementation
type Outbox struct {
	db *gorm.DB
}

// NewOutbox creates a new Outbox instance
func NewOutbox(db *gorm.DB) *Outbox {
	return &Outbox{db: db}
}

// PublishInTransaction saves an event within the given transaction
// This ensures the event is atomically saved with the business operation
func (o *Outbox) PublishInTransaction(tx *gorm.DB, event *Event) error {
	return tx.Create(event).Error
}

// Publish saves an event using the default database connection
// Use PublishInTransaction when you need to include the event in an existing transaction
func (o *Outbox) Publish(event *Event) error {
	return o.db.Create(event).Error
}

// CreateEvent is a helper to create an Event with proper JSON marshaling
func CreateEvent(aggregateType string, aggregateID uuid.UUID, eventType string, payload interface{}) (*Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &Event{
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		Payload:       data,
		CreatedAt:     time.Now(),
	}, nil
}

// ClaimResult is what ProcessNext did with the row it claimed.
type ClaimResult struct {
	Event      Event
	Claimed    bool  // false when no row was left to claim
	PublishErr error // non-nil when publish failed; the row was marked failed instead
}

// ProcessNext claims ONE unprocessed event, hands it to publish, and records the
// outcome, all in one transaction (NIAGA-207).
//
// Locking. The row is selected with FOR UPDATE SKIP LOCKED, so while one processor
// holds it every other processor skips it and takes the next row instead. Every
// service that starts a Processor drains the same shared outbox.events table, and
// before this two of them could read, publish and mark the same row: an event
// delivered twice, silently. One row per transaction keeps the lock held for one
// publish, not a whole batch.
//
// Shared drain, on purpose. The query does not filter by owning service: any
// running processor may publish any service's row. With the lock that is safe, and
// it means a service's events still leave while its own processor is down. There is
// no owner column and none should be added for this.
//
// Retry cap. Only rows with retry_count < maxRetries are claimed. A row that has
// failed maxRetries times stays in the table with its error and processed_at NULL,
// for a person to read. It is no longer attempted.
//
// Crash semantics: at-least-once. publish runs before the mark commits. If the
// process dies between the two, the transaction rolls back, the lock is released
// and the row is published again later. Marking first would drop the event instead.
// The Nats-Msg-Id header (the event ID) lets JetStream dedupe that repeat inside its
// duplicate window.
//
// exclude lists rows the caller has already tried in this tick, so one failing row
// is not retried in a burst.
func (o *Outbox) ProcessNext(maxRetries int, exclude []uuid.UUID, publish func(Event) error) (ClaimResult, error) {
	var res ClaimResult
	err := o.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("processed_at IS NULL AND retry_count < ?", maxRetries)
		if len(exclude) > 0 {
			q = q.Where("id NOT IN ?", exclude)
		}
		var events []Event
		if err := q.Order("created_at ASC").Limit(1).Find(&events).Error; err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		res.Event, res.Claimed = events[0], true
		if perr := publish(res.Event); perr != nil {
			res.PublishErr = perr
			return tx.Model(&Event{}).
				Where("id = ?", res.Event.ID).
				Updates(map[string]interface{}{
					"error":       perr.Error(),
					"retry_count": gorm.Expr("retry_count + 1"),
				}).Error
		}
		return tx.Model(&Event{}).
			Where("id = ?", res.Event.ID).
			Update("processed_at", time.Now()).Error
	})
	return res, err
}

// GetUnprocessedEvents retrieves events that haven't been processed yet.
//
// Deprecated: it takes no lock and has no retry cap, so two readers get the same
// rows. The Processor uses ProcessNext. Kept only so no caller breaks.
func (o *Outbox) GetUnprocessedEvents(limit int) ([]Event, error) {
	var events []Event
	err := o.db.Where("processed_at IS NULL").
		Order("created_at ASC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

// MarkProcessed marks an event as successfully processed
func (o *Outbox) MarkProcessed(eventID uuid.UUID) error {
	now := time.Now()
	return o.db.Model(&Event{}).
		Where("id = ?", eventID).
		Update("processed_at", now).Error
}

// MarkFailed marks an event as failed with an error message
func (o *Outbox) MarkFailed(eventID uuid.UUID, errMsg string) error {
	return o.db.Model(&Event{}).
		Where("id = ?", eventID).
		Updates(map[string]interface{}{
			"error":       errMsg,
			"retry_count": gorm.Expr("retry_count + 1"),
		}).Error
}

// GetFailedEvents retrieves events that failed processing.
//
// Deprecated: no lock, and the Processor no longer has a separate retry pass
// (NIAGA-207). Kept only so no caller breaks.
func (o *Outbox) GetFailedEvents(maxRetries int, limit int) ([]Event, error) {
	var events []Event
	err := o.db.Where("processed_at IS NULL AND error IS NOT NULL AND retry_count < ?", maxRetries).
		Order("created_at ASC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

// CleanupProcessedEvents removes old processed events
func (o *Outbox) CleanupProcessedEvents(olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	return o.db.Where("processed_at IS NOT NULL AND processed_at < ?", cutoff).
		Delete(&Event{}).Error
}
