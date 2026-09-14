package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

// StreamConfig defines stream configuration
type StreamConfig struct {
	Name        string
	Description string
	Subjects    []string
	MaxAge      time.Duration // How long to keep messages
	MaxMsgs     int64         // Max messages per subject
	MaxBytes    int64         // Max total bytes
	Replicas    int           // Number of replicas (1 for single node)
}

// ConsumerConfig defines consumer configuration
type ConsumerConfig struct {
	Name          string
	Stream        string
	FilterSubject string
	AckWait       time.Duration
	MaxDeliver    int
	MaxAckPending int
}

// JetStreamClient wraps NATS JetStream functionality
type JetStreamClient struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	logger *zap.Logger
}

// OutboxPublisher adapts JetStreamClient to lib-common/outbox.Processor.
type OutboxPublisher struct {
	client *JetStreamClient
}

// NewOutboxPublisher creates a publisher compatible with outbox.Processor.
func NewOutboxPublisher(client *JetStreamClient) *OutboxPublisher {
	return &OutboxPublisher{client: client}
}

// Publish publishes without additional headers.
func (p *OutboxPublisher) Publish(subject string, data []byte) error {
	return p.client.publish(context.Background(), subject, data, nil)
}

// PublishWithHeaders publishes with NATS headers, including Nats-Msg-Id.
func (p *OutboxPublisher) PublishWithHeaders(subject string, data []byte, headers map[string]string) error {
	return p.client.publish(context.Background(), subject, data, headers)
}

// Default stream configurations for Niaga services. Phase 10 retired the
// legacy ORDERS/INVENTORY/PRODUCTS streams (and the older NOTIFICATIONS
// catch-all on events.>) — every publisher now writes to the canonical
// per-domain EVENTS_* streams below.
var DefaultStreams = []StreamConfig{
	{
		Name:        "EVENTS_USER",
		Description: "User account and authentication events",
		Subjects:    []string{"events.user.>"},
		MaxAge:      30 * 24 * time.Hour,
		MaxMsgs:     50000,
		MaxBytes:    50 * 1024 * 1024,
		Replicas:    1,
	},
	{
		Name:        "EVENTS_ORDER",
		Description: "Order and payment lifecycle events",
		Subjects:    []string{"events.order.>"},
		MaxAge:      30 * 24 * time.Hour,
		MaxMsgs:     100000,
		MaxBytes:    200 * 1024 * 1024,
		Replicas:    1,
	},
	{
		Name:        "EVENTS_INVENTORY",
		Description: "Inventory and stock events",
		Subjects:    []string{"events.inventory.>"},
		MaxAge:      14 * 24 * time.Hour,
		MaxMsgs:     100000,
		MaxBytes:    150 * 1024 * 1024,
		Replicas:    1,
	},
	{
		Name:        "EVENTS_CATALOG",
		Description: "Catalog product and promotion events",
		Subjects:    []string{"events.catalog.>"},
		MaxAge:      7 * 24 * time.Hour,
		MaxMsgs:     50000,
		MaxBytes:    100 * 1024 * 1024,
		Replicas:    1,
	},
	{
		Name:        "EVENTS_SUPPORT",
		Description: "Support ticket events",
		Subjects:    []string{"events.support.>"},
		MaxAge:      7 * 24 * time.Hour,
		MaxMsgs:     50000,
		MaxBytes:    50 * 1024 * 1024,
		Replicas:    1,
	},
	{
		Name:        "EVENTS_MARKETPLACE",
		Description: "Marketplace synchronization events",
		Subjects:    []string{"events.marketplace.>"},
		MaxAge:      3 * 24 * time.Hour,
		MaxMsgs:     50000,
		MaxBytes:    50 * 1024 * 1024,
		Replicas:    1,
	},
	{
		// A SUBJECT WITH NO STREAM IS NOT DELIVERED, AND THE PUBLISH FAILS
		// (NIAGA-123). publish() uses js.PublishMsg, which waits for a stream to
		// acknowledge; nothing captured events.customer.> until this entry, so
		// events.customer.back_in_stock could be enqueued in the outbox and then
		// never leave it.
		//
		// Such a row is now retried once per tick until it has failed MaxRetries
		// times (default 5), then left in outbox.events with its error for a person
		// to read. Before NIAGA-207 it was retried INDEFINITELY: the main query had
		// no cap, and the capped retry pass was a second attempt in the same tick
		// that limited only itself.
		//
		// That is the loud half of the failure. The quiet half is the one to
		// remember: a consumer bound to a subject nothing publishes is a HEALTHY
		// consumer that never fires (NIAGA-116). A missing stream errors; a
		// missing publisher does not.
		Name:        "EVENTS_CUSTOMER",
		Description: "Customer lifecycle events — back-in-stock notifications, and whatever follows",
		Subjects:    []string{"events.customer.>"},
		// 30 days, matching EVENTS_USER rather than EVENTS_MARKETPLACE's 3. A
		// back-in-stock event is the only record that a named customer asked to
		// be told about a product; losing one is losing that request, not losing
		// a sync that will happen again on the next tick.
		MaxAge:   30 * 24 * time.Hour,
		MaxMsgs:  50000,
		MaxBytes: 50 * 1024 * 1024,
		Replicas: 1,
	},
}

// NewJetStreamClient creates a new JetStream client with retry logic
func NewJetStreamClient(natsURL string, logger *zap.Logger) (*JetStreamClient, error) {
	opts := []nats.Option{
		nats.Name("niaga-service"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1), // Unlimited reconnects
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				logger.Warn("NATS disconnected", zap.Error(err))
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("NATS reconnected", zap.String("url", nc.ConnectedUrl()))
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			logger.Error("NATS error", zap.Error(err))
		}),
	}

	// Connect with retry
	var nc *nats.Conn
	var err error
	for i := 0; i < 10; i++ {
		nc, err = nats.Connect(natsURL, opts...)
		if err == nil {
			break
		}
		logger.Warn("Failed to connect to NATS, retrying...",
			zap.Int("attempt", i+1),
			zap.Error(err))
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS after retries: %w", err)
	}

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	logger.Info("Connected to NATS with JetStream", zap.String("url", natsURL))

	return &JetStreamClient{
		nc:     nc,
		js:     js,
		logger: logger,
	}, nil
}

// EnsureStream creates or updates a stream
func (c *JetStreamClient) EnsureStream(ctx context.Context, cfg StreamConfig) (jetstream.Stream, error) {
	streamCfg := jetstream.StreamConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Subjects:    cfg.Subjects,
		MaxAge:      cfg.MaxAge,
		MaxMsgs:     cfg.MaxMsgs,
		MaxBytes:    cfg.MaxBytes,
		Replicas:    cfg.Replicas,
		Storage:     jetstream.FileStorage, // Persist to disk
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardOld,
	}

	stream, err := c.js.CreateOrUpdateStream(ctx, streamCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create/update stream %s: %w", cfg.Name, err)
	}

	c.logger.Info("Stream ready",
		zap.String("name", cfg.Name),
		zap.Strings("subjects", cfg.Subjects))

	return stream, nil
}

// EnsureDefaultStreams creates all default streams
func (c *JetStreamClient) EnsureDefaultStreams(ctx context.Context) error {
	for _, cfg := range DefaultStreams {
		if _, err := c.EnsureStream(ctx, cfg); err != nil {
			return err
		}
	}
	return nil
}

// CreateDurableConsumer creates a durable consumer for reliable message delivery
func (c *JetStreamClient) CreateDurableConsumer(ctx context.Context, cfg ConsumerConfig) (jetstream.Consumer, error) {
	stream, err := c.js.Stream(ctx, cfg.Stream)
	if err != nil {
		return nil, fmt.Errorf("stream %s not found: %w", cfg.Stream, err)
	}

	consumerCfg := jetstream.ConsumerConfig{
		Name:          cfg.Name,
		Durable:       cfg.Name,
		FilterSubject: cfg.FilterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.MaxDeliver,
		MaxAckPending: cfg.MaxAckPending,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, consumerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer %s: %w", cfg.Name, err)
	}

	c.logger.Info("Consumer ready",
		zap.String("name", cfg.Name),
		zap.String("stream", cfg.Stream),
		zap.String("filter", cfg.FilterSubject))

	return consumer, nil
}

// Publish publishes a message to JetStream with acknowledgment
func (c *JetStreamClient) Publish(ctx context.Context, subject string, data []byte) error {
	return c.publish(ctx, subject, data, nil)
}

// PublishWithHeaders publishes a message to JetStream with NATS headers.
func (c *JetStreamClient) PublishWithHeaders(subject string, data []byte, headers map[string]string) error {
	return c.publish(context.Background(), subject, data, headers)
}

func (c *JetStreamClient) publish(ctx context.Context, subject string, data []byte, headers map[string]string) error {
	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
	}

	if len(headers) > 0 {
		msg.Header = make(nats.Header, len(headers))
		for key, value := range headers {
			msg.Header.Set(key, value)
		}
	}

	ack, err := c.js.PublishMsg(ctx, msg)
	if err != nil {
		c.logger.Error("Failed to publish message",
			zap.String("subject", subject),
			zap.Error(err))
		return err
	}

	c.logger.Debug("Message published",
		zap.String("subject", subject),
		zap.String("stream", ack.Stream),
		zap.Uint64("sequence", ack.Sequence))

	return nil
}

// PublishAsync publishes a message asynchronously
func (c *JetStreamClient) PublishAsync(subject string, data []byte) error {
	_, err := c.js.PublishAsync(subject, data)
	return err
}

// Subscribe creates a simple push-based subscription (for backward compatibility)
func (c *JetStreamClient) Subscribe(subject string, handler func(msg *nats.Msg)) (*nats.Subscription, error) {
	return c.nc.Subscribe(subject, handler)
}

// ConsumeMessages starts consuming messages from a consumer with a handler
func (c *JetStreamClient) ConsumeMessages(ctx context.Context, consumer jetstream.Consumer, handler func(msg jetstream.Msg)) (jetstream.ConsumeContext, error) {
	return consumer.Consume(func(msg jetstream.Msg) {
		handler(msg)
	})
}

// GetJetStream returns the underlying JetStream context
func (c *JetStreamClient) GetJetStream() jetstream.JetStream {
	return c.js
}

// GetConnection returns the underlying NATS connection
func (c *JetStreamClient) GetConnection() *nats.Conn {
	return c.nc
}

// Close closes the NATS connection
func (c *JetStreamClient) Close() {
	if c.nc != nil {
		c.nc.Drain()
		c.nc.Close()
		c.logger.Info("NATS connection closed")
	}
}

// HealthCheck checks if NATS and JetStream are healthy
func (c *JetStreamClient) HealthCheck(ctx context.Context) error {
	if !c.nc.IsConnected() {
		return fmt.Errorf("NATS not connected")
	}

	// Try to get account info to verify JetStream is working
	_, err := c.js.AccountInfo(ctx)
	if err != nil {
		return fmt.Errorf("JetStream not available: %w", err)
	}

	return nil
}
