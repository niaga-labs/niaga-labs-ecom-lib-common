package middleware

import (
	"context"
	"errors"
	"testing"
	"time"
)

// NIAGA-564: a check that ignores its context must not hold /health/ready
// past the readiness budget.

type stubCheck struct {
	name  string
	err   error
	block chan struct{} // nil: answer at once; otherwise wait on it, ignoring ctx
}

func (s stubCheck) Name() string { return s.name }
func (s stubCheck) Check(context.Context) error {
	if s.block != nil {
		<-s.block
	}
	return s.err
}

func TestAHungCheckIsReportedWhenTheBudgetRunsOut(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := runChecks(ctx, []HealthCheck{
		stubCheck{name: "postgres"},
		stubCheck{name: "nats", err: errors.New("down")},
		stubCheck{name: "stuck", block: hang},
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("runChecks took %v; it must return when the budget ends", elapsed)
	}
	if got["postgres"] != "ok" || got["nats"] != "fail: down" {
		t.Fatalf("answered checks: %v", got)
	}
	if got["stuck"] != "fail: no answer within the readiness budget" {
		t.Fatalf("stuck = %q, want the timeout result", got["stuck"])
	}
}

func TestEveryCheckAnsweringInTimeIsReportedAsItAnswered(t *testing.T) {
	got := runChecks(context.Background(), []HealthCheck{stubCheck{name: "a"}, stubCheck{name: "b", err: errors.New("x")}})
	if len(got) != 2 || got["a"] != "ok" || got["b"] != "fail: x" {
		t.Fatalf("results = %v", got)
	}
}
