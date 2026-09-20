package eventsourcing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// NATSEventPublisher publishes domain events to NATS.
type NATSEventPublisher struct {
	js          nats.JetStreamContext
	streamName  string
	subjectBase string
	logger      *zap.Logger
}

// NATSPublisherConfig holds configuration for the NATS event publisher.
type NATSPublisherConfig struct {
	StreamName  string // JetStream stream name (e.g., "DOMAIN_EVENTS")
	SubjectBase string // Base subject for events (e.g., "events")
}

// DefaultNATSPublisherConfig returns default configuration.
func DefaultNATSPublisherConfig() NATSPublisherConfig {
	return NATSPublisherConfig{
		StreamName:  "DOMAIN_EVENTS",
		SubjectBase: "events",
	}
}

// NewNATSEventPublisher creates a new NATS event publisher.
func NewNATSEventPublisher(js nats.JetStreamContext, config NATSPublisherConfig, logger *zap.Logger) (*NATSEventPublisher, error) {
	// Ensure stream exists
	_, err := js.StreamInfo(config.StreamName)
	if err != nil {
		if err == nats.ErrStreamNotFound {
			// Create the stream
			_, err = js.AddStream(&nats.StreamConfig{
				Name:      config.StreamName,
				Subjects:  []string{config.SubjectBase + ".>"},
				Storage:   nats.FileStorage,
				Retention: nats.LimitsPolicy,
				MaxAge:    24 * 60 * 60 * 1e9, // 24 hours
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create stream: %w", err)
			}
			logger.Info("Created JetStream stream for domain events",
				zap.String("stream", config.StreamName))
		} else {
			return nil, fmt.Errorf("failed to get stream info: %w", err)
		}
	}

	return &NATSEventPublisher{
		js:          js,
		streamName:  config.StreamName,
		subjectBase: config.SubjectBase,
		logger:      logger,
	}, nil
}

// Publish publishes events to NATS JetStream.
func (p *NATSEventPublisher) Publish(ctx context.Context, events []*DomainEvent) error {
	for _, event := range events {
		// Build subject: events.<aggregate_type>.<event_type>
		subject := fmt.Sprintf("%s.%s.%s", p.subjectBase, event.AggregateType, event.EventType)

		data, err := json.Marshal(event)
		if err != nil {
			p.logger.Error("Failed to marshal event",
				zap.String("event_type", event.EventType),
				zap.Error(err))
			return err
		}

		// Publish with acknowledgment
		_, err = p.js.Publish(subject, data)
		if err != nil {
			p.logger.Error("Failed to publish event",
				zap.String("subject", subject),
				zap.String("event_id", event.ID),
				zap.Error(err))
			return err
		}

		p.logger.Debug("Event published",
			zap.String("subject", subject),
			zap.String("event_id", event.ID),
			zap.String("event_type", event.EventType))
	}

	return nil
}

// NATSEventSubscriber subscribes to domain events from NATS.
type NATSEventSubscriber struct {
	js          nats.JetStreamContext
	subjectBase string
	logger      *zap.Logger
}

// NewNATSEventSubscriber creates a new NATS event subscriber.
func NewNATSEventSubscriber(js nats.JetStreamContext, config NATSPublisherConfig, logger *zap.Logger) *NATSEventSubscriber {
	return &NATSEventSubscriber{
		js:          js,
		subjectBase: config.SubjectBase,
		logger:      logger,
	}
}

// Subscribe subscribes to events matching the given event types.
func (s *NATSEventSubscriber) Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error {
	for _, eventType := range eventTypes {
		// Subscribe to events.*.{eventType}
		subject := fmt.Sprintf("%s.*.%s", s.subjectBase, eventType)

		_, err := s.js.Subscribe(subject, func(msg *nats.Msg) {
			var event DomainEvent
			if err := json.Unmarshal(msg.Data, &event); err != nil {
				s.logger.Error("Failed to unmarshal event",
					zap.String("subject", msg.Subject),
					zap.Error(err))
				msg.Nak()
				return
			}

			if err := handler(context.Background(), &event); err != nil {
				s.logger.Error("Failed to handle event",
					zap.String("event_id", event.ID),
					zap.String("event_type", event.EventType),
					zap.Error(err))
				msg.Nak()
				return
			}

			msg.Ack()
		}, nats.Durable(fmt.Sprintf("handler-%s", eventType)))

		if err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
		}

		s.logger.Info("Subscribed to event type",
			zap.String("event_type", eventType),
			zap.String("subject", subject))
	}

	return nil
}

// SubscribeAll subscribes to all events.
func (s *NATSEventSubscriber) SubscribeAll(ctx context.Context, consumerName string, handler EventHandler) error {
	subject := fmt.Sprintf("%s.>", s.subjectBase)

	_, err := s.js.Subscribe(subject, func(msg *nats.Msg) {
		var event DomainEvent
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			s.logger.Error("Failed to unmarshal event",
				zap.String("subject", msg.Subject),
				zap.Error(err))
			msg.Nak()
			return
		}

		if err := handler(context.Background(), &event); err != nil {
			s.logger.Error("Failed to handle event",
				zap.String("event_id", event.ID),
				zap.String("event_type", event.EventType),
				zap.Error(err))
			msg.Nak()
			return
		}

		msg.Ack()
	}, nats.Durable(consumerName))

	if err != nil {
		return fmt.Errorf("failed to subscribe to all events: %w", err)
	}

	s.logger.Info("Subscribed to all events",
		zap.String("consumer", consumerName),
		zap.String("subject", subject))

	return nil
}
