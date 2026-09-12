// Package jobs implements the durable job lifecycle: the state machine fixed
// by the implementation contract (M0-05), single-transaction job creation,
// lease-based claiming with fencing tokens, cancellation, retries and the
// per-job event stream that feeds SSE.
//
// State machine (the only legal transitions):
//
//	queued -> running -> succeeded
//	queued -> cancelled
//	running -> failed
//	running -> cancel_requested -> cancelled | failed
//
// Jobs are never deleted and retries create new rows, so there are no
// cascade semantics; see the 00002 migration note.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// State is a job lifecycle state; the wire values match the Job schema enum.
type State string

const (
	StateQueued          State = "queued"
	StateRunning         State = "running"
	StateCancelRequested State = "cancel_requested"
	StateSucceeded       State = "succeeded"
	StateFailed          State = "failed"
	StateCancelled       State = "cancelled"
)

// Terminal reports whether the state ends the job. Terminal states are
// never overwritten - late writers must lose their race (fencing).
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// transitions is the authoritative state machine; it backs ValidateTransition
// and the state machine tests. Worker-level writes enforce the same rules
// through conditional UPDATEs, so a bug here cannot corrupt stored state.
var transitions = map[State][]State{
	StateQueued:          {StateRunning, StateCancelled},
	StateRunning:         {StateSucceeded, StateFailed, StateCancelRequested},
	StateCancelRequested: {StateCancelled, StateFailed},
}

// ValidateTransition reports whether from may move to to.
func ValidateTransition(from, to State) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// ErrNotFound is returned when no job with the given id exists.
var ErrNotFound = errors.New("jobs: not found")

// ResultRef points at a resource produced by a job (Job.result_refs).
type ResultRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Job is the persistent job row. The JSON encoding matches the Job contract
// schema; internal fields (config hash, lease, fencing token) are not on the
// wire.
type Job struct {
	ID           string
	RunID        string
	ParentJobID  string
	Kind         string
	State        State
	Phase        string
	Done         int64
	Total        *int64
	ResultRefs   []ResultRef
	Error        string
	ConfigHash   string
	LeaseOwner   string
	LeaseUntil   *time.Time
	FencingToken int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// wireError mirrors the Error contract schema for Job.error. A failed job's
// message has no stable domain code, so it reports internal.error.
type wireError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Issues    []domain.Issue `json:"issues"`
}

// wireJob is the exact wire shape of the Job schema.
type wireJob struct {
	ID          string      `json:"id"`
	RunID       string      `json:"run_id"`
	ParentJobID *string     `json:"parent_job_id"`
	Kind        string      `json:"kind"`
	State       string      `json:"state"`
	Phase       string      `json:"phase"`
	Completed   int64       `json:"completed"`
	Total       *int64      `json:"total"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Error       *wireError  `json:"error"`
	ResultRefs  []ResultRef `json:"result_refs"`
}

// MarshalJSON encodes the contract shape: nullable parents and errors come
// out as null, result_refs as an array (empty, never null).
func (j Job) MarshalJSON() ([]byte, error) {
	w := wireJob{
		ID:         j.ID,
		RunID:      j.RunID,
		Kind:       j.Kind,
		State:      string(j.State),
		Phase:      j.Phase,
		Completed:  j.Done,
		Total:      j.Total,
		CreatedAt:  j.CreatedAt.UTC(),
		UpdatedAt:  j.UpdatedAt.UTC(),
		ResultRefs: j.ResultRefs,
	}
	if w.ResultRefs == nil {
		w.ResultRefs = []ResultRef{}
	}
	if j.ParentJobID != "" {
		w.ParentJobID = &j.ParentJobID
	}
	if j.Error != "" {
		w.Error = &wireError{
			Code:    domain.CodeInternalError,
			Message: j.Error,
			Issues:  []domain.Issue{},
		}
	}
	return json.Marshal(w)
}

// Event is the per-job event stream item (JobEvent schema). Payload is the
// exact JSON stored in job_events so SSE replays bytes as written. Sequence
// is a string on the wire to avoid browser integer precision loss.
type Event struct {
	JobID    string
	Sequence int64
	At       time.Time
	Job      Job
}

// wireEvent is the exact wire shape of the JobEvent schema.
type wireEvent struct {
	JobID    string    `json:"job_id"`
	Sequence string    `json:"sequence"`
	At       time.Time `json:"at"`
	Job      Job       `json:"job"`
}

// MarshalJSON encodes the contract shape with sequence as a decimal string.
func (e Event) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireEvent{
		JobID:    e.JobID,
		Sequence: strconv.FormatInt(e.Sequence, 10),
		At:       e.At.UTC(),
		Job:      e.Job,
	})
}

// UnmarshalJSON decodes stored payloads; the sequence string must be numeric.
func (e *Event) UnmarshalJSON(data []byte) error {
	var w wireEvent
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	seq, err := strconv.ParseInt(w.Sequence, 10, 64)
	if err != nil {
		return fmt.Errorf("jobs: event sequence %q is not numeric", w.Sequence)
	}
	e.JobID = w.JobID
	e.Sequence = seq
	e.At = w.At
	e.Job = w.Job
	return nil
}

// Subscribe exposes the live fan-out to SSE handlers. Without a hub the
// channel never delivers; clients still see every event through replay.
func (s *Store) Subscribe(jobID string) (<-chan Event, func()) {
	if s.hub == nil {
		ch := make(chan Event)
		return ch, func() {}
	}
	return s.hub.Subscribe(jobID)
}

// defaultEventWindow bounds how many events per job are retained; older
// events are trimmed so a Last-Event-ID below the window start yields 410.
const defaultEventWindow = 1000

// Store persists jobs on the shared SQLite metadata database.
type Store struct {
	db     *sql.DB
	clock  ports.Clock
	hub    *Hub
	window int
}

// NewStore builds a store. hub may be nil (then no live SSE fan-out happens,
// clients still see events through replay). clock defaults to the system
// clock.
func NewStore(db *sql.DB, clock ports.Clock, hub *Hub) *Store {
	if clock == nil {
		clock = ports.SystemClock{}
	}
	return &Store{db: db, clock: clock, hub: hub, window: defaultEventWindow}
}

// SetEventWindow overrides the retained-events-per-job bound (tests).
func (s *Store) SetEventWindow(n int) {
	if n > 0 {
		s.window = n
	}
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("jobs: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("jobs: commit: %w", err)
	}
	return nil
}

func (s *Store) newID(prefix string) string {
	id, err := ports.RandomIDGenerator{Prefix: prefix}.NewID()
	if err != nil {
		// RandomIDGenerator only fails when the OS entropy source fails;
		// that is unrecoverable for a single-process service.
		panic(fmt.Sprintf("jobs: generate id: %v", err))
	}
	return string(id)
}

func configHash(config []byte) string {
	return ports.SHA256Checksummer{}.Checksum(config)
}

// Create inserts a run, its queued job and the first event in one
// transaction (contract rule: submit, pre-allocated run and first event are
// indivisible, and the idempotent HTTP response is captured by the
// middleware around exactly this commit).
func (s *Store) Create(ctx context.Context, kind string, config []byte, parentJobID string) (Job, error) {
	now := s.clock.Now().UTC()
	job := Job{
		ID:          s.newID("job"),
		RunID:       s.newID("run"),
		ParentJobID: parentJobID,
		Kind:        kind,
		State:       StateQueued,
		ConfigHash:  configHash(config),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	var ev Event
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, workspace, kind, immutable_config, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			job.RunID, workspaceDefault, kind, config, now.UnixNano(), now.UnixNano())
		if err != nil {
			return fmt.Errorf("jobs: insert run: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO jobs (id, workspace, run_id, parent_job_id, kind, state, config_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			job.ID, workspaceDefault, job.RunID, job.ParentJobID, job.Kind,
			string(job.State), job.ConfigHash, now.UnixNano(), now.UnixNano())
		if err != nil {
			return fmt.Errorf("jobs: insert job: %w", err)
		}
		ev, err = s.appendEventTx(ctx, tx, job, now)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	s.notify(ev)
	return job, nil
}

// Get loads one job by id.
func (s *Store) Get(ctx context.Context, jobID string) (Job, error) {
	return loadJob(ctx, s.db, jobID)
}

// ListQuery filters the job list (GET /jobs).
type ListQuery struct {
	State  State  // empty = all
	Kind   string // empty = all
	Search string // q: substring of the job id, empty = all
	Limit  int
	Cursor string // opaque last id (already the cursor payload's LastID)
	Sort   string // "id" or "-id"
}

// ListResult is one page of jobs plus the opaque resume point.
type ListResult struct {
	Items      []Job
	NextCursor string
}

// List returns jobs ordered by id (asc or desc) with stable keyset paging.
func (s *Store) List(ctx context.Context, q ListQuery) (ListResult, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	where := "WHERE 1=1"
	var args []any
	if q.State != "" {
		where += " AND state = ?"
		args = append(args, string(q.State))
	}
	if q.Kind != "" {
		where += " AND kind = ?"
		args = append(args, q.Kind)
	}
	if q.Search != "" {
		where += " AND id LIKE '%' || ? || '%'"
		args = append(args, q.Search)
	}
	if q.Cursor != "" {
		if q.Sort == "-id" {
			where += " AND id < ?"
		} else {
			where += " AND id > ?"
		}
		args = append(args, q.Cursor)
	}
	order := "ORDER BY id ASC"
	if q.Sort == "-id" {
		order = "ORDER BY id DESC"
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, parent_job_id, kind, state, phase, progress_done, progress_total,
		       result_refs, error, config_hash, lease_owner, lease_until, fencing_token,
		       created_at, updated_at
		FROM jobs `+where+` `+order+` LIMIT ?`, append(args, q.Limit+1)...)
	if err != nil {
		return ListResult{}, fmt.Errorf("jobs: list: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return ListResult{}, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("jobs: list rows: %w", err)
	}
	if jobs == nil {
		jobs = []Job{}
	}
	res := ListResult{Items: jobs}
	if len(jobs) > q.Limit {
		res.Items = jobs[:q.Limit]
		last := res.Items[len(res.Items)-1]
		res.NextCursor = last.ID
	}
	return res, nil
}

// RequestCancel moves a queued job straight to cancelled (no worker will
// ever observe it) and a running job to cancel_requested, which the worker
// honors at its next safe point. Repeating a cancel returns the current
// state unchanged; cancelling a succeeded/failed job is a conflict.
func (s *Store) RequestCancel(ctx context.Context, jobID string) (Job, error) {
	now := s.clock.Now().UTC()
	changed := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE jobs SET
				state = CASE state WHEN 'queued' THEN 'cancelled' ELSE 'cancel_requested' END,
				lease_owner = CASE state WHEN 'queued' THEN '' ELSE lease_owner END,
				lease_until = CASE state WHEN 'queued' THEN NULL ELSE lease_until END,
				updated_at = ?
			WHERE id = ? AND state IN ('queued','running')`,
			now.UnixNano(), jobID)
		if err != nil {
			return fmt.Errorf("jobs: request cancel: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("jobs: request cancel rows: %w", err)
		}
		if n == 0 {
			return nil
		}
		changed = true
		job, err := loadJob(ctx, tx, jobID)
		if err != nil {
			return err
		}
		_, err = s.appendEventTx(ctx, tx, job, now)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	if changed {
		job, err := loadJob(ctx, s.db, jobID)
		if err != nil {
			return Job{}, err
		}
		s.notify(eventFor(job, now))
		return job, nil
	}
	job, err := loadJob(ctx, s.db, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.State == StateCancelRequested || job.State == StateCancelled {
		return job, nil // already cancelling/cancelled: idempotent success
	}
	return Job{}, domain.NewError(domain.CodeResourceConflict,
		"job is already in terminal state %s", string(job.State))
}

// Retry creates a new job from a failed or cancelled one with frozen inputs:
// the original run's immutable_config is copied verbatim and the new job
// records parent_job_id. The original row is never modified.
func (s *Store) Retry(ctx context.Context, jobID string) (Job, error) {
	previous, err := s.Get(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	if previous.State != StateFailed && previous.State != StateCancelled {
		return Job{}, domain.NewError(domain.CodeResourceConflict,
			"only failed or cancelled jobs can be retried, job is %s", string(previous.State))
	}
	var config []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT immutable_config FROM runs WHERE id = ?`, previous.RunID).
		Scan(&config)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("jobs: run %s of job %s is missing", previous.RunID, jobID)
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: load run config: %w", err)
	}
	return s.Create(ctx, previous.Kind, config, jobID)
}

// Config loads the frozen immutable_config of a job's run, so a handler can
// recover the exact request the job was created with. A retried job replays
// its original inputs even if upstream state moved on in the meantime.
func (s *Store) Config(ctx context.Context, jobID string) ([]byte, error) {
	job, err := s.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	var config []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT immutable_config FROM runs WHERE id = ?`, job.RunID).
		Scan(&config)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("jobs: run %s of job %s is missing", job.RunID, jobID)
	}
	if err != nil {
		return nil, fmt.Errorf("jobs: load run config: %w", err)
	}
	return config, nil
}

// RecoverOrphans runs at startup. The process model is single-owner (M0 runs
// one worker loop in-process), so any lease surviving startup belongs to a
// dead worker: cancel_requested jobs can never reach their safe point again
// and are closed as cancelled; running jobs keep their state but lose the
// lease so the next claim takes them immediately.
func (s *Store) RecoverOrphans(ctx context.Context) error {
	now := s.clock.Now().UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM jobs WHERE state IN ('running','cancel_requested')`)
	if err != nil {
		return fmt.Errorf("jobs: find orphans: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("jobs: scan orphans: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("jobs: scan orphans: %w", err)
	}

	for _, id := range ids {
		err := s.withTx(ctx, func(tx *sql.Tx) error {
			job, err := loadJob(ctx, tx, id)
			if err != nil {
				return err
			}
			if job.State == StateCancelRequested {
				job.State = StateCancelled
			}
			job.LeaseOwner = ""
			job.LeaseUntil = nil
			job.UpdatedAt = now
			_, err = tx.ExecContext(ctx, `
				UPDATE jobs SET state = ?, lease_owner = '', lease_until = NULL, updated_at = ? WHERE id = ?`,
				string(job.State), now.UnixNano(), id)
			if err != nil {
				return fmt.Errorf("jobs: recover %s: %w", id, err)
			}
			if job.State == StateCancelled {
				_, err = s.appendEventTx(ctx, tx, job, now)
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// --- events ---

// appendEventTx appends the next sequence with a full job snapshot payload
// and trims the window. Returns the stored event so callers can fan out
// after commit.
func (s *Store) appendEventTx(ctx context.Context, tx *sql.Tx, job Job, at time.Time) (Event, error) {
	var seq int64
	err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sequence), 0) + 1 FROM job_events WHERE job_id = ?`, job.ID).
		Scan(&seq)
	if err != nil {
		return Event{}, fmt.Errorf("jobs: next sequence: %w", err)
	}
	ev := eventFor(job, at)
	ev.Sequence = seq
	payload, err := json.Marshal(ev)
	if err != nil {
		return Event{}, fmt.Errorf("jobs: encode event: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO job_events (job_id, sequence, workspace, state, phase, progress_total, progress_done, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, seq, workspaceDefault, string(job.State), job.Phase,
		nullInt64(job.Total), job.Done, payload, at.UnixNano())
	if err != nil {
		return Event{}, fmt.Errorf("jobs: insert event: %w", err)
	}
	// Keep only the newest s.window events per job; older ones are deleted so
	// Last-Event-ID replay below the window start can answer 410.
	if s.window > 0 {
		_, err = tx.ExecContext(ctx, `
			DELETE FROM job_events
			WHERE job_id = ? AND sequence <= (SELECT MAX(sequence) - ? FROM job_events WHERE job_id = ?)`,
			job.ID, s.window, job.ID)
		if err != nil {
			return Event{}, fmt.Errorf("jobs: trim events: %w", err)
		}
	}
	return ev, nil
}

func eventFor(job Job, at time.Time) Event {
	return Event{JobID: job.ID, At: at, Job: job}
}

// EventsAfter returns up to limit events with sequence > after, oldest
// first. payload carries the stored JSON.
func (s *Store) EventsAfter(ctx context.Context, jobID string, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload FROM job_events
		WHERE job_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`,
		jobID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("jobs: events: %w", err)
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("jobs: scan event: %w", err)
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("jobs: decode event: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// MinSequence returns the lowest retained sequence for a job (0 when none).
// A client's Last-Event-ID below min-1 means its replay window is gone (410).
func (s *Store) MinSequence(ctx context.Context, jobID string) (int64, error) {
	var min sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(sequence) FROM job_events WHERE job_id = ?`, jobID).Scan(&min)
	if err != nil {
		return 0, fmt.Errorf("jobs: min sequence: %w", err)
	}
	return min.Int64, nil
}

// notify fans an event out to live SSE subscribers after commit. Failures
// are dropped silently: subscribers replay from the database on reconnect,
// so a missed push is a latency blip, never data loss.
func (s *Store) notify(ev Event) {
	if s.hub != nil {
		s.hub.Notify(ev)
	}
}

// --- row loading ---

const jobColumns = `id, run_id, parent_job_id, kind, state, phase, progress_done, progress_total,
	result_refs, error, config_hash, lease_owner, lease_until, fencing_token, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(row rowScanner) (Job, error) {
	var j Job
	var parent, state, leaseOwner string
	var leaseUntil, total sql.NullInt64
	var refs []byte
	var created, updated int64
	err := row.Scan(&j.ID, &j.RunID, &parent, &j.Kind, &state, &j.Phase, &j.Done, &total,
		&refs, &j.Error, &j.ConfigHash, &leaseOwner, &leaseUntil, &j.FencingToken,
		&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: scan: %w", err)
	}
	j.ParentJobID = parent
	j.State = State(state)
	j.LeaseOwner = leaseOwner
	if total.Valid {
		t := total.Int64
		j.Total = &t
	}
	if leaseUntil.Valid {
		t := time.Unix(0, leaseUntil.Int64).UTC()
		j.LeaseUntil = &t
	}
	j.CreatedAt = time.Unix(0, created).UTC()
	j.UpdatedAt = time.Unix(0, updated).UTC()
	if err := json.Unmarshal(refs, &j.ResultRefs); err != nil {
		return Job{}, fmt.Errorf("jobs: decode result_refs: %w", err)
	}
	return j, nil
}

func loadJob(ctx context.Context, q rowQuerier, jobID string) (Job, error) {
	j, err := scanJob(q.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, jobID))
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// ParseSequence parses a Last-Event-ID header value.
func ParseSequence(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("jobs: invalid Last-Event-ID %q", raw)
	}
	return seq, nil
}
