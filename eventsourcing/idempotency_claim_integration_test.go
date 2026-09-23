//go:build integration

package eventsourcing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NIAGA-357. The claim is a lease now: in flight until Complete, taken over once
// it expires, and only a COMPLETED row is a duplicate. These run on real
// Postgres against events.processed with completed_at (infra-database
// 20260924_add_events_processed_completed_at). Every row uses a fresh event id
// and is deleted afterwards, so the shared table keeps nothing.
//
//	DB_HOST=... DB_NAME=... go test -tags=integration ./eventsourcing/ -run Claim -v

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func claimTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; skipping Postgres claim tests")
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"), envOr("DB_PORT", "5432"), envOr("DB_USER", "niaga"),
		envOr("DB_PASSWORD", "niaga_secret"), envOr("DB_NAME", "niaga_db"))
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var n int64
	db.Raw(`SELECT count(*) FROM information_schema.columns
	         WHERE table_schema='events' AND table_name='processed' AND column_name='completed_at'`).Scan(&n)
	if n != 1 {
		t.Fatal("events.processed has no completed_at: apply infra-database's NIAGA-357 migration first")
	}
	return db
}

func freshEvent(t *testing.T, db *gorm.DB) (string, string) {
	t.Helper()
	id, consumer := "niaga357-"+uuid.NewString(), "niaga357-test-consumer"
	t.Cleanup(func() {
		db.Exec(`DELETE FROM events.processed WHERE event_id = ?`, id)
	})
	return id, consumer
}

func TestClaimLifecycle(t *testing.T) {
	db := claimTestDB(t)
	c := NewIdempotencyChecker(db)
	ctx := context.Background()
	id, consumer := freshEvent(t, db)

	got, _, err := c.Claim(ctx, id, consumer, time.Minute)
	if err != nil || got != ClaimAcquired {
		t.Fatalf("first claim = %v, %v; want acquired", got, err)
	}

	got, retry, err := c.Claim(ctx, id, consumer, time.Minute)
	if err != nil || got != ClaimInProgress {
		t.Fatalf("second claim on a live lease = %v, %v; want in_progress (NOT a duplicate to ack)", got, err)
	}
	if retry < 50*time.Second || retry > time.Minute {
		t.Errorf("retry after = %v, want about the lease's remaining minute", retry)
	}

	if err := c.Complete(ctx, id, consumer); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got, _, _ := c.Claim(ctx, id, consumer, time.Minute); got != ClaimCompleted {
		t.Errorf("claim after complete = %v, want completed", got)
	}
	if err := c.Complete(ctx, id, consumer); !errors.Is(err, ErrNoClaim) {
		t.Errorf("second complete = %v, want ErrNoClaim", err)
	}
}

// THE NIAGA-357 BUG, as a row: a consumer claimed and died. Its claim is in
// flight and its lease has run out. The redelivery must take it over and run,
// not be told "duplicate" and ack.
func TestAStrandedClaimIsTakenOverOnceItsLeaseExpires(t *testing.T) {
	db := claimTestDB(t)
	c := NewIdempotencyChecker(db)
	ctx := context.Background()
	id, consumer := freshEvent(t, db)

	if err := db.Exec(`INSERT INTO events.processed (event_id, consumer_name, processed_at, completed_at)
	                   VALUES (?, ?, now() - interval '3 minutes', NULL)`, id, consumer).Error; err != nil {
		t.Fatalf("seed stranded claim: %v", err)
	}

	got, _, err := c.Claim(ctx, id, consumer, 2*time.Minute)
	if err != nil || got != ClaimAcquired {
		t.Fatalf("claim on a stranded, expired lease = %v, %v; want acquired", got, err)
	}
	var claimedAt time.Time
	db.Raw(`SELECT processed_at FROM events.processed WHERE event_id = ?`, id).Scan(&claimedAt)
	if time.Since(claimedAt) > 10*time.Second {
		t.Errorf("takeover did not renew the lease: processed_at %v", claimedAt)
	}
}

func TestOnlyOneOfManyConcurrentDeliveriesAcquires(t *testing.T) {
	db := claimTestDB(t)
	c := NewIdempotencyChecker(db)
	id, consumer := freshEvent(t, db)

	const n = 20
	var wg sync.WaitGroup
	results := make(chan ClaimResult, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, err := c.Claim(context.Background(), id, consumer, time.Minute)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	counts := map[ClaimResult]int{}
	for r := range results {
		counts[r]++
	}
	if counts[ClaimAcquired] != 1 || counts[ClaimInProgress] != n-1 {
		t.Errorf("results = %v, want exactly 1 acquired and %d in_progress", counts, n-1)
	}
}

// Two stale-takeover deliveries racing must not both win either: the update is
// guarded by the same lease condition in one statement.
func TestOnlyOneConcurrentTakeoverOfAStaleClaimWins(t *testing.T) {
	db := claimTestDB(t)
	c := NewIdempotencyChecker(db)
	id, consumer := freshEvent(t, db)
	db.Exec(`INSERT INTO events.processed (event_id, consumer_name, processed_at, completed_at)
	         VALUES (?, ?, now() - interval '10 minutes', NULL)`, id, consumer)

	const n = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	acquired := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, _, _ := c.Claim(context.Background(), id, consumer, time.Minute); r == ClaimAcquired {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if acquired != 1 {
		t.Errorf("%d deliveries took over the same stale claim, want 1", acquired)
	}
}

func TestReleaseLetsTheNextDeliveryAcquire(t *testing.T) {
	db := claimTestDB(t)
	c := NewIdempotencyChecker(db)
	ctx := context.Background()
	id, consumer := freshEvent(t, db)

	c.Claim(ctx, id, consumer, time.Minute)
	if err := c.Release(ctx, id, consumer); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got, _, _ := c.Claim(ctx, id, consumer, time.Minute); got != ClaimAcquired {
		t.Errorf("claim after release = %v, want acquired", got)
	}
}

// Callers still on CheckAndMark keep their old meaning: the row is born
// completed, so the new API reads it as a duplicate, never as in flight.
func TestACheckAndMarkRowReadsAsCompleted(t *testing.T) {
	db := claimTestDB(t)
	c := NewIdempotencyChecker(db)
	ctx := context.Background()
	id, consumer := freshEvent(t, db)

	if ok, err := c.CheckAndMark(ctx, id, consumer); !ok || err != nil {
		t.Fatalf("CheckAndMark = %v, %v", ok, err)
	}
	if ok, _ := c.CheckAndMark(ctx, id, consumer); ok {
		t.Error("CheckAndMark twice returned true: the old dedupe broke")
	}
	if got, _, _ := c.Claim(ctx, id, consumer, time.Minute); got != ClaimCompleted {
		t.Errorf("claim over a CheckAndMark row = %v, want completed", got)
	}
}
