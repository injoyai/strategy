package jobs

// Loop and Task behaviour tests: end-to-end claim -> handler -> terminal
// write, progress throttling, cancellation at safe points, lease-renewal
// failure propagation and shutdown semantics (at-least-once, leases kept).

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestLoop(t *testing.T, s *Store, handler HandlerFunc) (*Loop, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	loop := &Loop{
		Store:    s,
		Owner:    "loop-test",
		Handlers: map[string]HandlerFunc{"demo": handler},
		Lease:    5 * time.Second,
		Poll:     10 * time.Millisecond,
	}
	go func() {
		defer close(done)
		loop.Run(ctx)
	}()
	return loop, cancel, done
}

// waitState polls the store until the job reaches state, failing after a
// deadline; real wall-clock time is used, the store clock does not matter
// for these paths.
func waitState(t *testing.T, s *Store, jobID string, want State) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := s.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if job.State == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", jobID, want)
	return Job{}
}

func TestLoopRunsHandlerToSucceeded(t *testing.T) {
	s, _ := newTestStore(t)
	job := createJob(t, s, "demo")

	var progressed atomic.Bool
	_, cancel, done := newTestLoop(t, s, func(ctx context.Context, task *Task) error {
		if err := task.Progress("working", 1, nil); err != nil {
			return err
		}
		progressed.Store(true)
		task.SetResultRefs([]ResultRef{{Kind: "snapshot", ID: "snap1"}})
		return nil
	})
	defer cancel()

	final := waitState(t, s, job.ID, StateSucceeded)
	if !progressed.Load() {
		t.Error("handler did not run")
	}
	if final.Phase != "working" || final.Done != 1 {
		t.Errorf("progress not persisted: phase=%q done=%d", final.Phase, final.Done)
	}
	if len(final.ResultRefs) != 1 || final.ResultRefs[0].ID != "snap1" {
		t.Errorf("result refs not persisted: %+v", final.ResultRefs)
	}
	if final.Error != "" {
		t.Errorf("succeeded job carries error %q", final.Error)
	}
	cancel()
	<-done
}

func TestLoopFailsJobOnHandlerError(t *testing.T) {
	s, _ := newTestStore(t)
	job := createJob(t, s, "demo")

	_, cancel, done := newTestLoop(t, s, func(ctx context.Context, task *Task) error {
		return errors.New("synthetic handler boom")
	})
	defer cancel()

	final := waitState(t, s, job.ID, StateFailed)
	if !strings.Contains(final.Error, "synthetic handler boom") {
		t.Errorf("error = %q, want the handler message", final.Error)
	}
	cancel()
	<-done
}

func TestLoopStopsAtSafePointOnCancelRequest(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	job := createJob(t, s, "demo")

	started := make(chan struct{})
	_, cancel, done := newTestLoop(t, s, func(ctx context.Context, task *Task) error {
		close(started)
		for !task.Cancelled() {
			time.Sleep(5 * time.Millisecond)
		}
		return nil // the loop turns cancel_requested into cancelled
	})
	defer cancel()

	<-started
	if _, err := s.RequestCancel(ctx, job.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	final := waitState(t, s, job.ID, StateCancelled)
	if final.Error != "" {
		t.Errorf("cancelled job carries error %q", final.Error)
	}
	cancel()
	<-done
}

func TestLoopStopsHandlerWhenLeaseIsLost(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	job := createJob(t, s, "demo")

	started := make(chan struct{})
	handlerDone := make(chan struct{})
	loop := &Loop{
		Store: s,
		Owner: "loop-test",
		Handlers: map[string]HandlerFunc{"demo": func(ctx context.Context, task *Task) error {
			close(started)
			<-ctx.Done() // hold until renewal failure cancels the context
			close(handlerDone)
			return ctx.Err()
		}},
		Lease: 100 * time.Millisecond,
		Poll:  10 * time.Millisecond,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	go loop.Run(runCtx)
	defer cancel()

	<-started

	// Simulate a second owner stealing the job: the fencing token moves and
	// the next renewal from the original owner must fail (ErrStaleToken),
	// which cancels the handler context at its safe point.
	if _, err := s.db.Exec(`UPDATE jobs SET fencing_token = fencing_token + 1 WHERE id = ?`, job.ID); err != nil {
		t.Fatalf("bump token: %v", err)
	}

	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("handler kept running after the lease was lost")
	}

	// The terminal write from the loop loses its race; the job stays running
	// under the new owner's token until that owner (or recovery) closes it.
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != StateRunning {
		t.Errorf("state = %s, want running (stolen)", got.State)
	}
	cancel()
}

func TestLoopShutdownKeepsLeaseForRestart(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	job := createJob(t, s, "demo")

	release := make(chan struct{})
	loop := &Loop{
		Store: s,
		Owner: "loop-test",
		Handlers: map[string]HandlerFunc{"demo": func(ctx context.Context, task *Task) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}},
		Lease: 30 * time.Second,
		Poll:  10 * time.Millisecond,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(runCtx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := s.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.State == StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never claimed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Shut down while the handler is mid-flight: no terminal write may
	// happen, the lease survives and the next start re-claims the job.
	cancel()
	close(release) // let the handler observe the cancelled context
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop")
	}

	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != StateRunning {
		t.Errorf("state = %s, want running after shutdown", got.State)
	}
	if got.LeaseOwner != "loop-test" || got.LeaseUntil == nil {
		t.Errorf("lease not kept: owner=%q until=%v", got.LeaseOwner, got.LeaseUntil)
	}
}

func TestTaskProgressThrottleAndSafePoints(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	job := createJob(t, s, "demo")
	claimed, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	task := &Task{store: s, ctx: ctx, jobID: job.ID, token: claimed.Token, interval: time.Hour}
	total := int64(10)

	// First write goes through; the identical phase is then throttled for the
	// whole interval; a phase change is never swallowed.
	if err := task.Progress("step", 1, &total); err != nil {
		t.Fatalf("progress 1: %v", err)
	}
	if err := task.Progress("step", 2, &total); err != nil {
		t.Fatalf("progress 2 (throttled): %v", err)
	}
	if err := task.Progress("final", 10, &total); err != nil {
		t.Fatalf("progress 3: %v", err)
	}

	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Phase != "final" || got.Done != 10 || got.Total == nil || *got.Total != 10 {
		t.Errorf("progress state wrong: %+v", got)
	}
	// create + claim + two throttled-through writes = 4 events.
	if n := eventCount(t, s, job.ID); n != 4 {
		t.Errorf("events = %d, want 4", n)
	}

	// Cancelled is false while the job is owned and running.
	if task.Cancelled() {
		t.Error("task reports cancelled on a healthy running job")
	}

	// After a cancel request the safe point must report it.
	if _, err := s.RequestCancel(ctx, job.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !task.Cancelled() {
		t.Error("task did not observe the cancel request")
	}

	// A cancelled context also reports cancellation without touching the db.
	cCtx, cCancel := context.WithCancel(ctx)
	cCancel()
	task2 := &Task{store: s, ctx: cCtx, jobID: job.ID, token: claimed.Token, interval: time.Hour}
	if !task2.Cancelled() {
		t.Error("task did not observe the cancelled context")
	}

	// Progress with a valid token continues while cancelling
	// (cancel_requested is a writable state); fencing only bites on terminal
	// states or stale tokens.
	if err := task.Progress("cancelling", 99, nil); err != nil {
		t.Errorf("progress during cancel_requested: %v", err)
	}
}
