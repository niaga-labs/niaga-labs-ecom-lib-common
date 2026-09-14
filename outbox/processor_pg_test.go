package outbox

// Tests that need a real Postgres: FOR UPDATE SKIP LOCKED is the whole point,
// and no in-memory database implements it (NIAGA-207).
//
//	OUTBOX_TEST_DSN="host=localhost port=5432 user=... dbname=outbox_test sslmode=disable" go test ./outbox/
//
// They create outbox.events in that database if it is missing and TRUNCATE it,
// so they refuse to run against niaga_db. Unset, they skip.

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const outboxDDL = `
CREATE SCHEMA IF NOT EXISTS outbox;
CREATE TABLE IF NOT EXISTS outbox.events (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    aggregate_type character varying(100) NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type character varying(100) NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    error text,
    retry_count integer DEFAULT 0 NOT NULL
);`

func pgTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DSN")
	if dsn == "" {
		t.Skip("OUTBOX_TEST_DSN not set: these tests need a real Postgres for SKIP LOCKED")
	}
	if strings.Contains(dsn, "dbname=niaga_db") {
		t.Fatal("refusing to TRUNCATE outbox.events in niaga_db; point OUTBOX_TEST_DSN at a scratch database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Exec(outboxDDL).Error; err != nil {
		t.Fatalf("ddl: %v", err)
	}
	if err := db.Exec("TRUNCATE outbox.events").Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

// countingPublisher counts publish ATTEMPTS per event ID (the Nats-Msg-Id header),
// successful or not, and fails every attempt for the IDs in fail.
type countingPublisher struct {
	mu       sync.Mutex
	attempts map[string]int
	fail     map[string]bool
	delay    time.Duration
}

func newCountingPublisher(delay time.Duration) *countingPublisher {
	return &countingPublisher{attempts: map[string]int{}, fail: map[string]bool{}, delay: delay}
}

func (c *countingPublisher) Publish(string, []byte) error {
	return errors.New("countingPublisher needs the Nats-Msg-Id header")
}

func (c *countingPublisher) PublishWithHeaders(_ string, _ []byte, headers map[string]string) error {
	time.Sleep(c.delay) // widen the window in which two processors overlap
	id := headers["Nats-Msg-Id"]
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts[id]++
	if c.fail[id] {
		return errors.New("publish refused")
	}
	return nil
}

func (c *countingPublisher) count(id uuid.UUID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts[id.String()]
}

func insertEvents(t *testing.T, db *gorm.DB, n int, mutate func(i int, e *Event)) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, n)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		e := &Event{
			ID:            uuid.New(),
			AggregateType: "test",
			AggregateID:   uuid.New(),
			EventType:     "events.test.created",
			Payload:       []byte(`{"n":1}`),
			CreatedAt:     base.Add(time.Duration(i) * time.Millisecond),
		}
		if mutate != nil {
			mutate(i, e)
		}
		if err := db.Create(e).Error; err != nil {
			t.Fatalf("insert: %v", err)
		}
		ids = append(ids, e.ID)
	}
	return ids
}

func unprocessed(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&Event{}).Where("processed_at IS NULL").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestTwoConcurrentProcessorsPublishEachRowExactlyOnce(t *testing.T) {
	db := pgTestDB(t)
	ids := insertEvents(t, db, 300, nil)
	pub := newCountingPublisher(2 * time.Millisecond)
	cfg := DefaultProcessorConfig()
	cfg.BatchSize = 50

	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		p := NewProcessor(NewOutbox(db), pub, zap.NewNop(), cfg)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				p.ProcessNow()
			}
		}()
	}
	wg.Wait()

	dupes := 0
	for _, id := range ids {
		if got := pub.count(id); got != 1 {
			dupes++
			if dupes <= 3 {
				t.Errorf("event %s published %d times, want exactly 1", id, got)
			}
		}
	}
	if dupes > 0 {
		t.Errorf("%d of %d events were not published exactly once", dupes, len(ids))
	}
	if left := unprocessed(t, db); left != 0 {
		t.Errorf("%d events left unprocessed, want 0", left)
	}
}

func TestAFailingRowIsAttemptedOncePerTick(t *testing.T) {
	db := pgTestDB(t)
	ids := insertEvents(t, db, 1, nil)
	pub := newCountingPublisher(0)
	pub.fail[ids[0].String()] = true

	NewProcessor(NewOutbox(db), pub, zap.NewNop(), DefaultProcessorConfig()).ProcessNow()

	if got := pub.count(ids[0]); got != 1 {
		t.Fatalf("a failing row was attempted %d times in one tick, want 1", got)
	}
	var e Event
	db.First(&e, "id = ?", ids[0])
	if e.RetryCount != 1 || e.Error == nil || e.ProcessedAt != nil {
		t.Fatalf("after one failed tick: retry_count=%d error-set=%v processed=%v, want 1/true/false",
			e.RetryCount, e.Error != nil, e.ProcessedAt != nil)
	}
}

func TestAFailingRowDoesNotStopTheRowsBehindIt(t *testing.T) {
	db := pgTestDB(t)
	ids := insertEvents(t, db, 4, nil) // ids[0] is the oldest
	pub := newCountingPublisher(0)
	pub.fail[ids[0].String()] = true

	NewProcessor(NewOutbox(db), pub, zap.NewNop(), DefaultProcessorConfig()).ProcessNow()

	if got := pub.count(ids[0]); got != 1 {
		t.Errorf("failing head row attempted %d times in one tick, want 1", got)
	}
	for _, id := range ids[1:] {
		if got := pub.count(id); got != 1 {
			t.Errorf("row %s behind the failing one attempted %d times, want 1", id, got)
		}
	}
	if left := unprocessed(t, db); left != 1 {
		t.Errorf("%d rows unprocessed, want 1 (the failing one)", left)
	}
}

func TestARowAtTheRetryCapIsNotAttempted(t *testing.T) {
	db := pgTestDB(t)
	errMsg := "earlier failures"
	cfg := DefaultProcessorConfig()
	ids := insertEvents(t, db, 1, func(_ int, e *Event) {
		e.RetryCount = cfg.MaxRetries
		e.Error = &errMsg
	})
	pub := newCountingPublisher(0)

	NewProcessor(NewOutbox(db), pub, zap.NewNop(), cfg).ProcessNow()

	if got := pub.count(ids[0]); got != 0 {
		t.Fatalf("a row at retry_count == MaxRetries (%d) was attempted %d times, want 0", cfg.MaxRetries, got)
	}
}

func TestAZeroConfigStillPublishes(t *testing.T) {
	db := pgTestDB(t)
	ids := insertEvents(t, db, 3, nil)
	pub := newCountingPublisher(0)

	// No MaxRetries and no BatchSize: a cap of 0 would match nothing and a batch of
	// 0 would take nothing, so both fall back to the defaults.
	NewProcessor(NewOutbox(db), pub, zap.NewNop(), ProcessorConfig{Interval: time.Second}).ProcessNow()

	for _, id := range ids {
		if got := pub.count(id); got != 1 {
			t.Errorf("event %s published %d times with a zero config, want 1", id, got)
		}
	}
}
