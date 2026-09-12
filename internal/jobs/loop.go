package jobs

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// workspaceDefault is the single-workspace placeholder carried by runs, jobs
// and job_events until multi-workspace support lands (M0 has one workspace).
const workspaceDefault = "default"

// Defaults for a Loop with unset timing fields.
const (
	DefaultLease   = 30 * time.Second
	DefaultPoll    = 500 * time.Millisecond
	DefaultAdvance = time.Second // minimum spacing between progress writes
)

// Hub fans committed events out to live SSE subscribers. Delivery is
// best-effort: a slow subscriber's buffered channel fills up and events are
// dropped - the client recovers by replaying from the store on reconnect,
// so a dropped push costs latency, never correctness.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]string // subscriber channel -> job id filter
}

// NewHub builds an empty fan-out hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan Event]string)}
}

// Subscribe registers a live listener for one job's events. The returned
// cancel function must be called (e.g. on SSE disconnect) to release it.
func (h *Hub) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	h.subs[ch] = jobID
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Notify delivers an event to matching subscribers without blocking.
func (h *Hub) Notify(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, jobID := range h.subs {
		if jobID != ev.JobID {
			continue
		}
		select {
		case ch <- ev:
		default: // subscriber too slow; it replays from the store on reconnect
		}
	}
}

// HandlerFunc executes one claimed job. It must observe ctx cancellation at
// safe points (task.Cancelled), report progress through task and return an
// error to fail the job. Handlers may be re-executed: M0 promises
// at-least-once, never exactly-once.
type HandlerFunc func(ctx context.Context, task *Task) error

// Task is the handler-facing view of a running job: throttled progress,
// cancellation checks and result registration.
type Task struct {
	store     *Store
	ctx       context.Context
	jobID     string
	token     int64
	interval  time.Duration
	last      time.Time
	lastPhase string
	refs      []ResultRef
}

// Progress records handler progress. Writes are throttled to one per
// interval unless the phase changed, so phase transitions are never
// swallowed; cancellation and terminal states are written by the loop, not
// here.
func (t *Task) Progress(phase string, done int64, total *int64) error {
	now := t.store.clock.Now()
	if phase == t.lastPhase && now.Sub(t.last) < t.interval {
		return nil
	}
	if _, err := t.store.Progress(t.ctx, t.jobID, t.token, phase, done, total); err != nil {
		return err
	}
	t.last = now
	t.lastPhase = phase
	return nil
}

// Cancelled reports whether the job should stop at the next safe point: the
// context is done (shutdown or lost lease) or a cancel request was accepted.
func (t *Task) Cancelled() bool {
	if t.ctx.Err() != nil {
		return true
	}
	job, err := t.store.Get(t.ctx, t.jobID)
	if err != nil {
		return false // transient read failure: keep going, the lease watchdog decides
	}
	return job.State == StateCancelRequested || job.State.Terminal()
}

// SetResultRefs registers the job's produced artifacts; they are persisted
// only when the job lands on succeeded.
func (t *Task) SetResultRefs(refs []ResultRef) { t.refs = refs }

// JobID returns the claimed job's ID.
func (t *Task) JobID() string { return t.jobID }

// Loop claims and executes jobs of its registered kinds. M0 runs exactly one
// Loop in-process; leases and fencing tokens keep the code honest for a
// future second worker without changing any path.
type Loop struct {
	Store            *Store
	Owner            string
	Handlers         map[string]HandlerFunc
	Lease            time.Duration // lease duration, renewed every Lease/2
	Poll             time.Duration // idle wait between claim attempts
	ProgressInterval time.Duration // minimum spacing between progress writes
	Log              *slog.Logger
}

// Run drives the loop until ctx is done. Shutdown leaves claimed jobs with
// their lease; the expired lease is re-claimed on the next start (running
// jobs with expired leases are claimable) or closed by startup recovery.
func (l *Loop) Run(ctx context.Context) {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	lease := l.Lease
	if lease <= 0 {
		lease = DefaultLease
	}
	poll := l.Poll
	if poll <= 0 {
		poll = DefaultPoll
	}
	progressInterval := l.ProgressInterval
	if progressInterval <= 0 {
		progressInterval = DefaultAdvance
	}

	for {
		if ctx.Err() != nil {
			return
		}
		claimed, ok, err := l.Store.Claim(ctx, l.Owner, l.kinds(), lease)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("jobs: claim failed", "err", err)
			if !sleep(ctx, poll) {
				return
			}
			continue
		}
		if !ok {
			if !sleep(ctx, poll) {
				return
			}
			continue
		}
		l.runOne(ctx, claimed, lease, progressInterval, log)
	}
}

// kinds returns the handler keys in stable order (Claim builds an IN clause).
func (l *Loop) kinds() []string {
	kinds := make([]string, 0, len(l.Handlers))
	for kind := range l.Handlers {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func (l *Loop) runOne(ctx context.Context, claimed Claimed, lease, progressInterval time.Duration, log *slog.Logger) {
	job := claimed.Job
	handler, ok := l.Handlers[job.Kind]
	if !ok {
		// Claim filters on registered kinds, so this cannot happen; guard
		// against a nil-map panic anyway.
		log.Error("jobs: no handler for claimed job", "kind", job.Kind, "job", job.ID)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	task := &Task{
		store:    l.Store,
		ctx:      runCtx,
		jobID:    job.ID,
		token:    claimed.Token,
		interval: progressInterval,
	}

	// Renew the lease at half its lifetime. Losing it (stale token) or any
	// renewal error cancels the handler context: the worker's writes would
	// be fenced off anyway, so continuing only wastes work.
	renewCtx, renewCancel := context.WithCancel(ctx)
	defer renewCancel()
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(lease / 2)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				if err := l.Store.RenewLease(renewCtx, job.ID, claimed.Token, lease); err != nil {
					if renewCtx.Err() == nil {
						log.Warn("jobs: lease renewal failed, stopping handler",
							"job", job.ID, "err", err)
					}
					cancel()
					return
				}
			}
		}
	}()

	err := handler(runCtx, task)
	renewCancel() // stop renewals before the terminal write
	<-renewDone   // no renewal can interleave with the terminal update
	if ctx.Err() != nil {
		// Shutting down: keep the lease; the expired lease is re-claimed or
		// recovered on the next start (default at-least-once).
		return
	}

	var final Job
	var won bool
	if err != nil {
		final, won, err = l.Store.Fail(ctx, job.ID, claimed.Token, err.Error())
	} else {
		final, won, err = l.Store.Complete(ctx, job.ID, claimed.Token, task.refs)
	}
	if err != nil {
		log.Error("jobs: terminal write failed", "job", job.ID, "err", err)
		return
	}
	if !won {
		// A concurrent cancel request (or a renewed claim) won the terminal
		// race; final carries the state that beat us.
		log.Info("jobs: terminal write lost the race", "job", job.ID,
			"state", string(final.State))
	}
}

// sleep waits for d or until ctx is done; reports whether to keep running.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
