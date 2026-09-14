package outbox

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Publisher defines the interface for publishing events
type Publisher interface {
	Publish(subject string, data []byte) error
}

// HeaderPublisher can publish an event with NATS headers.
type HeaderPublisher interface {
	PublishWithHeaders(subject string, data []byte, headers map[string]string) error
}

// Processor polls the outbox table and publishes events
type Processor struct {
	outbox     *Outbox
	publisher  Publisher
	logger     *zap.Logger
	interval   time.Duration
	batchSize  int
	maxRetries int
	done       chan struct{}
}

// ProcessorConfig holds configuration for the Processor
type ProcessorConfig struct {
	Interval   time.Duration // How often to poll for new events
	BatchSize  int           // Number of events to process per batch
	MaxRetries int           // Maximum retry attempts for failed events
}

// DefaultProcessorConfig returns default configuration
func DefaultProcessorConfig() ProcessorConfig {
	return ProcessorConfig{
		Interval:   5 * time.Second,
		BatchSize:  100,
		MaxRetries: 5,
	}
}

// NewProcessor creates a new outbox processor. A zero BatchSize or MaxRetries falls
// back to the default: a cap of 0 would claim nothing and a batch of 0 would take
// nothing, so a caller that set only the interval would publish silently never.
func NewProcessor(outbox *Outbox, publisher Publisher, logger *zap.Logger, config ProcessorConfig) *Processor {
	def := DefaultProcessorConfig()
	if config.BatchSize <= 0 {
		config.BatchSize = def.BatchSize
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = def.MaxRetries
	}
	if config.Interval <= 0 {
		config.Interval = def.Interval
	}
	return &Processor{
		outbox:     outbox,
		publisher:  publisher,
		logger:     logger,
		interval:   config.Interval,
		batchSize:  config.BatchSize,
		maxRetries: config.MaxRetries,
		done:       make(chan struct{}),
	}
}

// Start begins processing outbox events in a background goroutine
func (p *Processor) Start(ctx context.Context) {
	go p.run(ctx)
}

// Stop gracefully stops the processor
func (p *Processor) Stop() {
	close(p.done)
}

func (p *Processor) run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Process immediately on start
	p.processBatch()

	for {
		select {
		case <-ticker.C:
			p.processBatch()
		case <-ctx.Done():
			p.logger.Info("Outbox processor stopping due to context cancellation")
			return
		case <-p.done:
			p.logger.Info("Outbox processor stopping")
			return
		}
	}
}

// processBatch claims and publishes up to batchSize rows, one per transaction
// (Outbox.ProcessNext). A row is tried at most once per tick: a failed row is
// excluded from the rest of this tick and waits for the next one, until it has
// failed maxRetries times. There is no separate retry pass. Before NIAGA-207 one
// re-selected every failed row in the same tick, attempting it twice, while the
// main query had no cap at all, so the cap never stopped anything.
func (p *Processor) processBatch() {
	var tried []uuid.UUID
	published := 0
	for len(tried) < p.batchSize {
		res, err := p.outbox.ProcessNext(p.maxRetries, tried, p.processEvent)
		if err != nil {
			// End the tick rather than continue. A DB error here may come after a
			// publish whose mark rolled back; that row is not in `tried`, so the next
			// ProcessNext would claim and publish it again this very tick.
			p.logger.Error("Failed to claim an outbox event", zap.Error(err))
			return
		}
		if !res.Claimed {
			break
		}
		tried = append(tried, res.Event.ID)
		if res.PublishErr == nil {
			published++
			continue
		}
		attempts := res.Event.RetryCount + 1
		p.logger.Error("Failed to publish outbox event",
			zap.String("event_id", res.Event.ID.String()),
			zap.String("event_type", res.Event.EventType),
			zap.Int("attempt", attempts),
			zap.Int("max_retries", p.maxRetries),
			zap.Error(res.PublishErr))
		if attempts >= p.maxRetries {
			p.logger.Error("Outbox event reached MaxRetries and will not be attempted again; it stays in outbox.events with its error",
				zap.String("event_id", res.Event.ID.String()),
				zap.String("event_type", res.Event.EventType))
		}
	}
	if len(tried) > 0 {
		p.logger.Debug("Processed outbox events",
			zap.Int("claimed", len(tried)), zap.Int("published", published))
	}
}

func (p *Processor) processEvent(event Event) error {
	// Build the subject from aggregate type and event type
	// e.g., "order.created", "inventory.restocked"
	subject := event.EventType

	headers := map[string]string{
		"Nats-Msg-Id": event.ID.String(),
	}

	if publisher, ok := p.publisher.(HeaderPublisher); ok {
		return publisher.PublishWithHeaders(subject, event.Payload, headers)
	}

	// Backward-compatible fallback for existing publishers. Prefer implementing
	// HeaderPublisher so JetStream can dedupe outbox retries by event ID.
	return p.publisher.Publish(subject, event.Payload)
}

// ProcessNow forces immediate processing (useful for testing)
func (p *Processor) ProcessNow() {
	p.processBatch()
}
