package jobs

// Store-level tests for the M0-05 job lifecycle: the state machine table,
// atomic creation, FIFO claiming with fencing tokens, the cancel matrix,
// frozen retries, startup recovery and the bounded event window behind SSE.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/store"
)

var testBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestStore returns a store over a migrated temporary database with a
// pinned clock so lease expiry is fully deterministic.
func newTestStore(t *testing.T) (*Store, *ports.FixedClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db, silentLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	clk := ports.NewFixedClock(testBase)
	return NewStore(db, clk, nil), clk
}

func createJob(t *testing.T, s *Store, kind string) Job {
	t.Helper()
	job, err := s.Create(context.Background(), kind, []byte(`{"kind":`+kind+`}`), "")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func eventCount(t *testing.T, s *Store, jobID string) int {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), jobID, 0, 1000)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return len(events)
}

func TestValidateTransitionTable(t *testing.T) {
	legal := map[State][]State{
		StateQueued:          {StateRunning, StateCancelled},
		StateRunning:         {StateSucceeded, StateFailed, StateCancelRequested},
		StateCancelRequested: {StateCancelled, StateFailed},
	}
	for from, nexts := range legal {
		for _, to := range nexts {
			if !ValidateTransition(from, to) {
				t.Errorf("%s -> %s: want legal", from, to)
			}
		}
	}
	for _, pair := range [][2]State{
		{StateQueued, StateSucceeded},
		{StateQueued, StateFailed},
		{StateQueued, StateCancelRequested},
		{StateRunning, StateRunning},
		{StateRunning, StateCancelled},
		{StateRunning, StateQueued},
		{StateCancelRequested, StateRunning},
		{StateCancelRequested, StateCancelRequested},
		{StateSucceeded, StateFailed},
		{StateFailed, StateSucceeded},
		{StateCancelled, StateRunning},
		{StateSucceeded, StateQueued},
	} {
		if ValidateTransition(pair[0], pair[1]) {
			t.Errorf("%s -> %s: want illegal", pair[0], pair[1])
		}
	}
	for _, s := range []State{StateSucceeded, StateFailed, StateCancelled} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []State{StateQueued, StateRunning, StateCancelRequested} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestCreatePersistsRunJobAndFirstEventAtomically(t *testing.T) {
	s, _ := newTestStore(t)
	job, err := s.Create(context.Background(), "demo", []byte(`{"demo":true}`), "job_parent")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if job.State != StateQueued || job.Kind != "demo" || job.ParentJobID != "job_parent" {
		t.Fatalf("unexpected created job: %+v", job)
	}
	if job.ConfigHash != configHash([]byte(`{"demo":true}`)) {
		t.Errorf("config hash mismatch")
	}

	got, err := s.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != StateQueued || got.FencingToken != 0 || got.LeaseOwner != "" {
		t.Errorf("stored job mismatch: %+v", got)
	}

	// The run row carries the frozen immutable config.
	var config []byte
	if err := s.db.QueryRow(`SELECT immutable_config FROM runs WHERE id = ?`, job.RunID).
		Scan(&config); err != nil {
		t.Fatalf("run row: %v", err)
	}
	if string(config) != `{"demo":true}` {
		t.Errorf("immutable_config = %s", config)
	}

	// Exactly one event with sequence 1 exists.
	events, err := s.EventsAfter(context.Background(), job.ID, 0, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("want exactly event 1, got %+v", events)
	}
	if events[0].Job.State != StateQueued || events[0].JobID != job.ID {
		t.Errorf("first event snapshot mismatch: %+v", events[0])
	}
}

func TestClaimIsFIFOAndBumpsFencing(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()

	first := createJob(t, s, "demo")
	clk.Advance(time.Minute)
	second := createJob(t, s, "demo")
	clk.Advance(time.Minute)
	_ = createJob(t, s, "other") // different kind, never claimed below

	c1, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim 1: ok=%v err=%v", ok, err)
	}
	if c1.Job.ID != first.ID {
		t.Errorf("claim 1 got %s, want %s (FIFO)", c1.Job.ID, first.ID)
	}
	if c1.Job.State != StateRunning || c1.Job.LeaseOwner != "w1" || c1.Job.LeaseUntil == nil {
		t.Errorf("claim 1 lease state wrong: %+v", c1.Job)
	}
	if c1.Token != 1 {
		t.Errorf("fencing token = %d, want 1", c1.Token)
	}

	c2, ok, err := s.Claim(ctx, "w2", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim 2: ok=%v err=%v", ok, err)
	}
	if c2.Job.ID != second.ID {
		t.Errorf("claim 2 got %s, want %s (FIFO)", c2.Job.ID, second.ID)
	}
	if c2.Token != 1 {
		t.Errorf("fresh job fencing token = %d, want 1", c2.Token)
	}

	if _, ok, err := s.Claim(ctx, "w3", []string{"demo"}, time.Minute); ok || err != nil {
		t.Errorf("unexpected third claim: ok=%v err=%v", ok, err)
	}

	// The claim appended one event to each claimed job.
	if n := eventCount(t, s, first.ID); n != 2 {
		t.Errorf("first job has %d events, want 2 (create + claim)", n)
	}
}

func TestClaimTakesExpiredRunningLease(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()
	job := createJob(t, s, "demo")

	if _, ok, err := s.Claim(ctx, "w1", []string{"demo"}, 30*time.Second); !ok || err != nil {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}

	// Lease still valid: not claimable, not even by the same owner.
	if _, ok, _ := s.Claim(ctx, "w1", []string{"demo"}, 30*time.Second); ok {
		t.Fatal("claimed a job whose lease is still valid")
	}

	// After expiry the job is claimable again and the fencing token moves.
	clk.Advance(31 * time.Second)
	c2, ok, err := s.Claim(ctx, "w2", []string{"demo"}, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("reclaim: ok=%v err=%v", ok, err)
	}
	if c2.Job.ID != job.ID {
		t.Errorf("reclaimed %s, want %s", c2.Job.ID, job.ID)
	}
	if c2.Token != 2 {
		t.Errorf("fencing token = %d, want 2", c2.Token)
	}
	if c2.Job.LeaseOwner != "w2" {
		t.Errorf("lease owner = %q, want w2", c2.Job.LeaseOwner)
	}
}

// wantConflict asserts that err is a domain error carrying code.
func wantConflict(t *testing.T, err error) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodeResourceConflict {
		t.Fatalf("err = %v, want %s", err, domain.CodeResourceConflict)
	}
}

func TestRequestCancelMatrix(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// queued -> cancelled directly; lease fields are cleared.
	queued := createJob(t, s, "demo")
	cancelled, err := s.RequestCancel(ctx, queued.ID)
	if err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	if cancelled.State != StateCancelled || cancelled.LeaseOwner != "" || cancelled.LeaseUntil != nil {
		t.Errorf("cancelled queued job wrong: %+v", cancelled)
	}
	if n := eventCount(t, s, queued.ID); n != 2 {
		t.Errorf("queued cancel events = %d, want 2", n)
	}

	// running -> cancel_requested; repeating stays idempotent.
	running := createJob(t, s, "demo")
	if _, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute); !ok || err != nil {
		t.Fatalf("claim: %v", err)
	}
	requested, err := s.RequestCancel(ctx, running.ID)
	if err != nil {
		t.Fatalf("cancel running: %v", err)
	}
	if requested.State != StateCancelRequested {
		t.Errorf("state = %s, want cancel_requested", requested.State)
	}
	again, err := s.RequestCancel(ctx, running.ID)
	if err != nil {
		t.Fatalf("repeat cancel: %v", err)
	}
	if again.State != StateCancelRequested {
		t.Errorf("repeat cancel state = %s, want cancel_requested", again.State)
	}

	// terminal -> conflict.
	failed := createJob(t, s, "demo")
	if _, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute); !ok || err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := s.Fail(ctx, failed.ID, 1, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if _, err := s.RequestCancel(ctx, failed.ID); err == nil {
		t.Error("cancel of a failed job must conflict")
	} else {
		wantConflict(t, err)
	}

	// unknown job -> ErrNotFound.
	if _, err := s.RequestCancel(ctx, "job_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancel missing err = %v, want ErrNotFound", err)
	}
}

func TestRetryFreezesConfigAndLinksParent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	original := createJob(t, s, "demo")
	if _, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute); !ok || err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := s.Fail(ctx, original.ID, 1, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	retried, err := s.Retry(ctx, original.ID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.ID == original.ID {
		t.Fatal("retry must create a new job row")
	}
	if retried.State != StateQueued || retried.Kind != "demo" {
		t.Errorf("retried job wrong: %+v", retried)
	}
	if retried.ParentJobID != original.ID {
		t.Errorf("parent_job_id = %q, want %q", retried.ParentJobID, original.ID)
	}
	var config []byte
	if err := s.db.QueryRow(`SELECT immutable_config FROM runs WHERE id = ?`, retried.RunID).
		Scan(&config); err != nil {
		t.Fatalf("run row: %v", err)
	}
	if string(config) != `{"kind":demo}` {
		t.Errorf("frozen config = %s", config)
	}

	// The original row is untouched.
	prev, err := s.Get(ctx, original.ID)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if prev.State != StateFailed || prev.Error != "boom" {
		t.Errorf("original mutated: %+v", prev)
	}

	// Only failed or cancelled jobs can be retried.
	if _, err := s.Retry(ctx, retried.ID); err == nil {
		t.Error("retry of a queued job must conflict")
	} else {
		wantConflict(t, err)
	}
}

func TestRecoverOrphansOnStartup(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// Branch 1: a running orphan keeps its state but loses the lease, so the
	// next claim takes it immediately.
	expired := createJob(t, s, "demo")
	if _, ok, err := s.Claim(ctx, "dead-worker", []string{"demo"}, time.Minute); !ok || err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Branch 2: cancel_requested can never reach its safe point again; it is
	// closed as cancelled with an event.
	asked := createJob(t, s, "demo")
	if _, ok, err := s.Claim(ctx, "dead-worker", []string{"demo"}, time.Minute); !ok || err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.RequestCancel(ctx, asked.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	eventsBefore := eventCount(t, s, asked.ID)

	if err := s.RecoverOrphans(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	got, err := s.Get(ctx, expired.ID)
	if err != nil {
		t.Fatalf("get running orphan: %v", err)
	}
	if got.State != StateRunning || got.LeaseOwner != "" || got.LeaseUntil != nil {
		t.Errorf("running orphan not lease-cleared: %+v", got)
	}
	if _, ok, err := s.Claim(ctx, "w2", []string{"demo"}, time.Minute); !ok || err != nil {
		t.Errorf("reclaim after recovery: ok=%v err=%v", ok, err)
	}

	got, err = s.Get(ctx, asked.ID)
	if err != nil {
		t.Fatalf("get cancel_requested orphan: %v", err)
	}
	if got.State != StateCancelled {
		t.Errorf("cancel_requested orphan state = %s, want cancelled", got.State)
	}
	if n := eventCount(t, s, asked.ID); n != eventsBefore+1 {
		t.Errorf("cancelled orphan events = %d, want %d", n, eventsBefore+1)
	}

	// Terminal and queued jobs are untouched (no new events).
	queued := createJob(t, s, "demo")
	nQueued := eventCount(t, s, queued.ID)
	if err := s.RecoverOrphans(ctx); err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if n := eventCount(t, s, queued.ID); n != nQueued {
		t.Errorf("queued job gained events during recovery")
	}
}

func TestEventWindowTrimsAndReportsMinSequence(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetEventWindow(3)
	ctx := context.Background()
	job := createJob(t, s, "demo")
	claimed, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := s.Progress(ctx, job.ID, claimed.Token, "step", int64(i), nil); err != nil {
			t.Fatalf("progress %d: %v", i, err)
		}
	}

	// Sequences 1..5 were written (create, claim, 3 progress); a window of 3
	// trims sequence <= MAX(5)-3, keeping 3..5.
	min, err := s.MinSequence(ctx, job.ID)
	if err != nil {
		t.Fatalf("min sequence: %v", err)
	}
	if min != 3 {
		t.Fatalf("min sequence = %d, want 3", min)
	}
	events, err := s.EventsAfter(ctx, job.ID, 0, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 3 || events[0].Sequence != 3 || events[2].Sequence != 5 {
		t.Fatalf("retained events = %+v, want sequences 3..5", events)
	}
}

func TestEventWireShapeUsesStringSequence(t *testing.T) {
	s, _ := newTestStore(t)
	job := createJob(t, s, "demo")
	events, err := s.EventsAfter(context.Background(), job.ID, 0, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	raw, err := json.Marshal(events[0])
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	seq, ok := probe["sequence"].(string)
	if !ok || seq != "1" {
		t.Errorf("sequence = %#v, want the string \"1\"", probe["sequence"])
	}
	for _, key := range []string{"job_id", "at", "job"} {
		if _, ok := probe[key]; !ok {
			t.Errorf("event JSON missing %q: %s", key, raw)
		}
	}

	// Round trip through the stored payload shape.
	var back Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if back.Sequence != 1 || back.JobID != job.ID {
		t.Errorf("round trip mismatch: %+v", back)
	}
	if err := json.Unmarshal([]byte(`{"sequence":"x"}`), &back); err == nil {
		t.Error("non-numeric sequence must fail to unmarshal")
	}
}

func TestFencedWritesRejectStaleTokens(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	job := createJob(t, s, "demo")
	claimed, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	stale := claimed.Token + 99

	if err := s.RenewLease(ctx, job.ID, stale, time.Minute); !errors.Is(err, ErrStaleToken) {
		t.Errorf("RenewLease stale err = %v, want ErrStaleToken", err)
	}
	if _, err := s.Progress(ctx, job.ID, stale, "p", 1, nil); !errors.Is(err, ErrStaleToken) {
		t.Errorf("Progress stale err = %v, want ErrStaleToken", err)
	}
	if _, _, err := s.Complete(ctx, job.ID, stale, nil); err != nil {
		t.Errorf("Complete stale err = %v, want nil (won=false)", err)
	}
	if _, _, err := s.Fail(ctx, job.ID, stale, "boom"); err != nil {
		t.Errorf("Fail stale err = %v, want nil (won=false)", err)
	}

	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != StateRunning {
		t.Errorf("stale writes changed state to %s", got.State)
	}
	if n := eventCount(t, s, job.ID); n != 2 {
		t.Errorf("stale writes appended events: %d", n)
	}

	// The current token still works for every write path.
	if err := s.RenewLease(ctx, job.ID, claimed.Token, time.Minute); err != nil {
		t.Errorf("RenewLease: %v", err)
	}
	if _, err := s.Progress(ctx, job.ID, claimed.Token, "p", 1, nil); err != nil {
		t.Errorf("Progress: %v", err)
	}

	// RenewLease on a terminal or missing job is stale / not found.
	if _, _, err := s.Fail(ctx, job.ID, claimed.Token, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := s.RenewLease(ctx, job.ID, claimed.Token, time.Minute); !errors.Is(err, ErrStaleToken) {
		t.Errorf("RenewLease on failed job err = %v, want ErrStaleToken", err)
	}
	if err := s.RenewLease(ctx, "job_missing", 1, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Errorf("RenewLease missing err = %v, want ErrNotFound", err)
	}
}

func TestCompleteAndFailLoseRaceAgainstCancellation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// A late success on a cancel_requested job lands cancelled, and the
	// result refs of the aborted run are not persisted.
	job := createJob(t, s, "demo")
	claimed, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := s.RequestCancel(ctx, job.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	final, won, err := s.Complete(ctx, job.ID, claimed.Token, []ResultRef{{Kind: "snapshot", ID: "snap1"}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !won {
		t.Fatal("Complete must win the transition from cancel_requested to cancelled")
	}
	if final.State != StateCancelled {
		t.Errorf("state = %s, want cancelled (cancellation beats late success)", final.State)
	}
	if len(final.ResultRefs) != 0 {
		t.Errorf("cancelled job carries result refs: %+v", final.ResultRefs)
	}

	// A second terminal writer loses its race.
	again, won, err := s.Fail(ctx, job.ID, claimed.Token, "late failure")
	if err != nil {
		t.Fatalf("late fail: %v", err)
	}
	if won {
		t.Error("Fail on a cancelled job must lose the race")
	}
	if again.State != StateCancelled {
		t.Errorf("job state = %s, want cancelled", again.State)
	}

	// Fail from cancel_requested is legal for the writer that owns it.
	job2 := createJob(t, s, "demo")
	c2, ok, err := s.Claim(ctx, "w1", []string{"demo"}, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := s.RequestCancel(ctx, job2.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	final2, won, err := s.Fail(ctx, job2.ID, c2.Token, "failed while cancelling")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !won || final2.State != StateFailed || final2.Error != "failed while cancelling" {
		t.Errorf("cancel_requested -> failed lost: state=%s won=%v err=%v", final2.State, won, err)
	}
}

func TestHubFansOutPerJobWithoutBlocking(t *testing.T) {
	hub := NewHub()
	chA, cancelA := hub.Subscribe("job_a")
	chB, cancelB := hub.Subscribe("job_b")
	defer cancelB()

	// Matching subscriber receives; other jobs are filtered out.
	hub.Notify(Event{JobID: "job_a", Sequence: 1})
	select {
	case ev := <-chA:
		if ev.JobID != "job_a" || ev.Sequence != 1 {
			t.Errorf("got %+v", ev)
		}
	default:
		t.Fatal("matching subscriber missed the event")
	}
	select {
	case ev := <-chB:
		t.Fatalf("unrelated subscriber got %+v", ev)
	default:
	}

	// A full buffer drops instead of blocking the writer.
	for i := 0; i < 20; i++ {
		hub.Notify(Event{JobID: "job_a", Sequence: int64(i + 2)})
	}

	// Cancel releases the subscription.
	cancelA()
	hub.Notify(Event{JobID: "job_a", Sequence: 99}) // must not panic or block
}

func TestStoreSubscribeWithoutHubNeverDelivers(t *testing.T) {
	s, _ := newTestStore(t)
	ch, cancel := s.Subscribe("job_x")
	defer cancel()
	createJob(t, s, "demo")
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

func TestParseSequenceAcceptsOnlyNonNegativeIntegers(t *testing.T) {
	if seq, err := ParseSequence(""); err != nil || seq != 0 {
		t.Errorf("empty = %d, %v; want 0, nil", seq, err)
	}
	if seq, err := ParseSequence("42"); err != nil || seq != 42 {
		t.Errorf("42 = %d, %v", seq, err)
	}
	for _, bad := range []string{"abc", "-1", "1.5", "0x10"} {
		if _, err := ParseSequence(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// placeholders is asserted through the queries it builds; this test keeps the
// helper honest if its shape ever changes.
func TestPlaceholders(t *testing.T) {
	if got := placeholders(3); got != "?,?,?" {
		t.Errorf("placeholders(3) = %q", got)
	}
	if got := placeholders(1); got != "?" {
		t.Errorf("placeholders(1) = %q", got)
	}
	if !strings.Contains(placeholders(2), ",") {
		t.Error("placeholders(2) must separate placeholders")
	}
}
