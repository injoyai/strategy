package worker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/worker"
)

func TestPoolDrainWaitsForInFlight(t *testing.T) {
	var pool worker.Pool
	var finished atomic.Bool
	release := make(chan struct{})

	pool.Go(context.Background(), func(ctx context.Context) {
		<-release
		finished.Store(true)
	})

	drainCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if pool.Drain(drainCtx) {
		t.Fatal("drain must not succeed while task is blocked")
	}

	close(release)
	if !pool.Drain(context.Background()) {
		t.Fatal("drain must succeed after task completes")
	}
	if !finished.Load() {
		t.Fatal("task must have run to completion")
	}
}

func TestPoolDrainRespectsContext(t *testing.T) {
	var pool worker.Pool
	pool.Go(context.Background(), func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	})
	drainCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if pool.Drain(drainCtx) {
		t.Fatal("drain must report timeout")
	}
}
