package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HealthCheck represents a single readiness dependency (DB, NATS, etc).
type HealthCheck interface {
	Name() string
	Check(ctx context.Context) error
}

// HealthCheckFunc adapts a plain function into a HealthCheck.
type HealthCheckFunc struct {
	CheckName string
	Fn        func(ctx context.Context) error
}

func (f HealthCheckFunc) Name() string                    { return f.CheckName }
func (f HealthCheckFunc) Check(ctx context.Context) error { return f.Fn(ctx) }

// HealthStatus is the canonical body returned by /health and /health/ready.
type HealthStatus struct {
	Status string            `json:"status"`           // "ok" | "degraded"
	Checks map[string]string `json:"checks,omitempty"` // <name> -> "ok" | "fail: <reason>"
}

// readinessTimeout caps how long a single readiness probe will wait for all
// dependencies to respond. Individual check implementations should still
// respect ctx cancellation for hard cutoff guarantees.
const readinessTimeout = 5 * time.Second

// RegisterHealth wires the canonical health endpoints on the given router.
//
//   - GET /health         — liveness, always 200; never touches deps.
//   - GET /health/ready   — readiness, runs every dep concurrently with a
//     short timeout. Returns 200 when all pass, 503
//     when any fails. Body is HealthStatus either way.
//
// Pass any number of HealthCheck instances — typical wiring is one per
// out-of-process dependency (Postgres, Redis, NATS, MinIO, Meilisearch).
func RegisterHealth(r gin.IRouter, deps ...HealthCheck) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, HealthStatus{Status: "ok"})
	})

	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()

		results := runChecks(ctx, deps)
		status := HealthStatus{Status: "ok", Checks: results}
		code := http.StatusOK
		for _, r := range results {
			if r != "ok" {
				status.Status = "degraded"
				code = http.StatusServiceUnavailable
				break
			}
		}
		c.JSON(code, status)
	})
}

// runChecks fans the deps out concurrently so a slow probe doesn't serialize
// the whole readiness budget. Returns a name -> result map.
func runChecks(ctx context.Context, deps []HealthCheck) map[string]string {
	results := make(map[string]string, len(deps))
	if len(deps) == 0 {
		return results
	}

	type outcome struct {
		name string
		err  error
	}
	ch := make(chan outcome, len(deps))
	for _, d := range deps {
		go func() {
			ch <- outcome{name: d.Name(), err: d.Check(ctx)}
		}()
	}

	for range deps {
		o := <-ch
		if o.err == nil {
			results[o.name] = "ok"
		} else {
			results[o.name] = "fail: " + o.err.Error()
		}
	}
	return results
}
