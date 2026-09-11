// Package worker defines the in-process job worker lifecycle.
//
// M0 shutdown protocol: when the process receives a cancellation signal the
// HTTP server stops accepting new requests first, then the pool stops taking
// new work and Drain waits a bounded time for in-flight tasks so a crash at
// exit cannot leave half-written job state. Persistent job semantics (leases,
// fencing, recovery) arrive in M0-05; this pool only owns the exit protocol.
package worker

import (
	"context"
	"sync"
)

// Pool tracks spawned worker tasks so shutdown can wait for them with a bound.
type Pool struct {
	wg sync.WaitGroup
}

// Go runs fn in a goroutine derived from ctx. Callers must stop calling Go
// before Drain; the HTTP layer guarantees this ordering during shutdown.
func (p *Pool) Go(ctx context.Context, fn func(ctx context.Context)) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		taskCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		fn(taskCtx)
	}()
}

// Drain waits for all in-flight tasks until ctx expires. It returns false if
// the context deadline hit first, in which case the caller logs a warning and
// exits; in-flight tasks are abandoned and recovered from persistent state on
// the next start (never silently dropped).
func (p *Pool) Drain(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
