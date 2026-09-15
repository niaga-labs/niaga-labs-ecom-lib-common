package monitoring

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// serve runs one request through a gin engine that uses both of the monitor's middlewares, with a
// handler that either answers 200 or panics.
func serve(t *testing.T, m *SentryMonitor, panics bool) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(m.GinMiddleware(), m.RecoveryMiddleware())
	r.GET("/", func(c *gin.Context) {
		if panics {
			panic("handler blew up")
		}
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	return w.Code
}

// exercise calls every method a service calls. Any nil dereference panics the test.
func exercise(t *testing.T, m *SentryMonitor) {
	t.Helper()
	if got := serve(t, m, false); got != http.StatusOK {
		t.Fatalf("request through the middlewares = %d, want 200", got)
	}
	if got := serve(t, m, true); got != http.StatusInternalServerError {
		t.Fatalf("panicking handler = %d, want 500 from RecoveryMiddleware", got)
	}
	m.CaptureError(errors.New("probe"))
	m.CaptureMessage("probe")
	m.Flush(10 * time.Millisecond)
}

// TestNewSentryMonitor_MalformedDSN is NIAGA-309: a DSN that sentry.Init rejects used to return a nil
// monitor, and every service then panicked at boot in GinMiddleware. It must come back as a working
// no-op monitor, with the error still returned for the caller to log.
func TestNewSentryMonitor_MalformedDSN(t *testing.T) {
	cfg := &SentryConfig{DSN: "not-a-dsn", Environment: "test"}
	m, err := NewSentryMonitor(cfg, zap.NewNop())
	if err == nil {
		t.Fatal("err = nil for a malformed DSN, want sentry.Init's error so the caller can log it")
	}
	if m == nil {
		t.Fatal("monitor = nil for a malformed DSN; every caller dereferences it at boot")
	}
	if m.enabled() {
		t.Fatal("monitor reports Sentry enabled after sentry.Init failed")
	}
	if cfg.DSN != "not-a-dsn" {
		t.Fatalf("the caller's config was modified: DSN = %q", cfg.DSN)
	}
	exercise(t, m)
}

func TestNewSentryMonitor_EmptyDSN(t *testing.T) {
	m, err := NewSentryMonitor(&SentryConfig{}, zap.NewNop())
	if err != nil || m == nil || m.enabled() {
		t.Fatalf("got monitor=%v err=%v enabled=%v, want a no-op monitor and nil error", m, err, m != nil && m.enabled())
	}
	exercise(t, m)
}

func TestNewSentryMonitor_NilConfig(t *testing.T) {
	m, err := NewSentryMonitor(nil, nil)
	if err != nil || m == nil || m.enabled() {
		t.Fatalf("got monitor=%v err=%v, want a no-op monitor and nil error", m, err)
	}
	exercise(t, m)
}

// TestSentryMonitor_NilReceiver is the belt: a caller that still ends up with a nil *SentryMonitor
// gets "Sentry off", not a panic.
func TestSentryMonitor_NilReceiver(t *testing.T) {
	var m *SentryMonitor
	exercise(t, m)
}

// TestNewSentryMonitor_ValidDSN keeps the working path as it was. The DSN only has to parse; nothing
// is sent, because no event is captured before the client is reset.
func TestNewSentryMonitor_ValidDSN(t *testing.T) {
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })
	m, err := NewSentryMonitor(&SentryConfig{DSN: "https://public@example.invalid/1"}, zap.NewNop())
	if err != nil {
		t.Fatalf("err = %v for a valid-shaped DSN", err)
	}
	if m == nil || !m.enabled() {
		t.Fatal("monitor not enabled for a valid DSN")
	}
	if got := serve(t, m, false); got != http.StatusOK {
		t.Fatalf("request through the Sentry middleware = %d, want 200", got)
	}
}
