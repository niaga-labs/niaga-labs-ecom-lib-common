package monitoring

import (
	"time"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SentryConfig holds configuration for Sentry
type SentryConfig struct {
	DSN              string
	Environment      string
	Release          string
	ServiceName      string
	TracesSampleRate float64
}

// SentryMonitor provides Sentry integration for monitoring.
//
// Every method is safe on a nil *SentryMonitor and on one built with no DSN: both act as
// "Sentry off". Sentry is optional, so nothing about it may take a service down.
type SentryMonitor struct {
	logger *zap.Logger
	config *SentryConfig
}

// NewSentryMonitor creates a new Sentry monitor instance.
//
// It never returns a nil monitor. An empty DSN gives a no-op monitor and a nil error. A DSN that
// sentry.Init rejects (a typo, a malformed value) gives the SAME no-op monitor together with the error,
// so the caller can log it and keep booting. Returning nil there used to panic all nine services at
// boot, because every caller goes on to call GinMiddleware, RecoveryMiddleware and a deferred Flush
// (NIAGA-309).
func NewSentryMonitor(config *SentryConfig, logger *zap.Logger) (*SentryMonitor, error) {
	if config == nil {
		config = &SentryConfig{}
	}
	if config.DSN == "" {
		// Skip Sentry initialization if DSN is not provided
		return &SentryMonitor{
			logger: logger,
			config: config,
		}, nil
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              config.DSN,
		Environment:      config.Environment,
		Release:          config.Release,
		TracesSampleRate: config.TracesSampleRate,
		AttachStacktrace: true,
	})
	if err != nil {
		// A copy with the DSN cleared, so every method takes its no-op branch. The caller's config is
		// not modified.
		off := *config
		off.DSN = ""
		return &SentryMonitor{
			logger: logger,
			config: &off,
		}, err
	}

	return &SentryMonitor{
		logger: logger,
		config: config,
	}, nil
}

// enabled reports whether Sentry was started. False for a nil monitor, a nil config, or no DSN.
func (m *SentryMonitor) enabled() bool {
	return m != nil && m.config != nil && m.config.DSN != ""
}

// log returns the monitor's logger, or nil for a nil monitor.
func (m *SentryMonitor) log() *zap.Logger {
	if m == nil {
		return nil
	}
	return m.logger
}

// GinMiddleware returns a Gin middleware for Sentry
func (m *SentryMonitor) GinMiddleware() gin.HandlerFunc {
	if !m.enabled() {
		// Return a no-op middleware if Sentry is not configured
		return func(c *gin.Context) {
			c.Next()
		}
	}
	return sentrygin.New(sentrygin.Options{
		Repanic: true,
	})
}

// RecoveryMiddleware returns a recovery middleware that reports panics to Sentry
func (m *SentryMonitor) RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				if m.enabled() {
					sentry.CurrentHub().Recover(err)
				}
				if l := m.log(); l != nil {
					l.Error("Panic recovered", zap.Any("error", err))
				}
				c.AbortWithStatus(500)
			}
		}()
		c.Next()
	}
}

// Flush flushes any buffered events to Sentry
func (m *SentryMonitor) Flush(timeout time.Duration) {
	if m.enabled() {
		sentry.Flush(timeout)
	}
}

// CaptureError reports an error to Sentry
func (m *SentryMonitor) CaptureError(err error) {
	if m.enabled() {
		sentry.CaptureException(err)
	}
	if l := m.log(); l != nil {
		l.Error("Error captured", zap.Error(err))
	}
}

// CaptureMessage reports a message to Sentry
func (m *SentryMonitor) CaptureMessage(msg string) {
	if m.enabled() {
		sentry.CaptureMessage(msg)
	}
	if l := m.log(); l != nil {
		l.Info("Message captured", zap.String("message", msg))
	}
}
