package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claimed is a job handed to a worker by Claim.
type Claimed struct {
	Job   Job
	Token int64 // fencing token; every subsequent write must carry it
}

// ErrStaleToken is returned when a write references a fencing token that is
// no longer current: the job moved on (another claim or a terminal state).
// The caller must stop executing immediately and must not write again.
var ErrStaleToken = errors.New("jobs: fencing token is stale")

// Claim hands the oldest claimable job of one of kinds to this worker:
// queued jobs first (FIFO by creation time), then running jobs whose lease
// expired or is missing (the previous owner is presumed dead; startup
// recovery clears leases of surviving running jobs). The claim marks the job
// running, sets the lease and bumps the fencing token. The write transaction
// serializes concurrent claimers (SQLite _txlock=immediate). ok is false
// when nothing is claimable.
func (s *Store) Claim(ctx context.Context, owner string, kinds []string, lease time.Duration) (Claimed, bool, error) {
	if len(kinds) == 0 {
		return Claimed{}, false, nil
	}
	now := s.clock.Now().UTC()
	var claimed Claimed
	var ev Event
	ok := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		query := `SELECT ` + jobColumns + ` FROM jobs
		WHERE kind IN (` + placeholders(len(kinds)) + `)
		  AND (state = 'queued'
		       OR (state = 'running' AND (lease_until IS NULL OR lease_until <= ?)))
		ORDER BY created_at ASC, id ASC
		LIMIT 1`
		args := make([]any, 0, len(kinds)+1)
		for _, k := range kinds {
			args = append(args, k)
		}
		args = append(args, now.UnixNano())

		job, err := scanJob(tx.QueryRowContext(ctx, query, args...))
		if errors.Is(err, ErrNotFound) {
			return nil // nothing claimable
		}
		if err != nil {
			return err
		}

		token := job.FencingToken + 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE jobs SET state = 'running', lease_owner = ?, lease_until = ?,
				fencing_token = ?, updated_at = ?
			WHERE id = ?`,
			owner, now.Add(lease).UnixNano(), token, now.UnixNano(), job.ID); err != nil {
			return fmt.Errorf("jobs: claim %s: %w", job.ID, err)
		}

		until := now.Add(lease)
		job.State = StateRunning
		job.LeaseOwner = owner
		job.LeaseUntil = &until
		job.FencingToken = token
		job.UpdatedAt = now

		ev, err = s.appendEventTx(ctx, tx, job, now)
		if err != nil {
			return err
		}
		claimed = Claimed{Job: job, Token: token}
		ok = true
		return nil
	})
	if err != nil {
		return Claimed{}, false, err
	}
	if ok {
		s.notify(ev)
	}
	return claimed, ok, nil
}

// RenewLease extends the lease of a job the caller still owns. It writes no
// event: lease heartbeats must not flood the bounded event window. A stale
// token or terminal state returns ErrStaleToken.
func (s *Store) RenewLease(ctx context.Context, jobID string, token int64, lease time.Duration) error {
	now := s.clock.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET lease_until = ?, updated_at = ?
		WHERE id = ? AND fencing_token = ? AND state IN ('running','cancel_requested')`,
		now.Add(lease).UnixNano(), now.UnixNano(), jobID, token)
	if err != nil {
		return fmt.Errorf("jobs: renew lease %s: %w", jobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("jobs: renew lease rows %s: %w", jobID, err)
	}
	if n == 0 {
		if _, err := s.Get(ctx, jobID); err != nil {
			return err // ErrNotFound or a real failure
		}
		return ErrStaleToken
	}
	return nil
}

// Progress records handler progress (phase may change, total may be unknown).
// The write matches the fencing token and only applies while the job is
// running or cancelling; anything else returns ErrStaleToken.
func (s *Store) Progress(ctx context.Context, jobID string, token int64, phase string, done int64, total *int64) (Job, error) {
	now := s.clock.Now().UTC()
	var job Job
	var ev Event
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE jobs SET phase = ?, progress_done = ?, progress_total = ?, updated_at = ?
			WHERE id = ? AND fencing_token = ? AND state IN ('running','cancel_requested')`,
			phase, done, nullInt64(total), now.UnixNano(), jobID, token)
		if err != nil {
			return fmt.Errorf("jobs: progress %s: %w", jobID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("jobs: progress rows %s: %w", jobID, err)
		}
		if n == 0 {
			job, err = loadJob(ctx, tx, jobID)
			if err != nil {
				return err
			}
			return ErrStaleToken
		}
		job, err = loadJob(ctx, tx, jobID)
		if err != nil {
			return err
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

// Complete performs the winning terminal transition for a successful
// handler. The CASE makes cancellation beat a late success: a job that
// reached cancel_requested lands on cancelled even though the handler
// finished its work (contract: exactly one terminal transition wins).
// won reports whether this call performed the transition; when false, job is
// the state that beat us.
func (s *Store) Complete(ctx context.Context, jobID string, token int64, refs []ResultRef) (Job, bool, error) {
	now := s.clock.Now().UTC()
	if refs == nil {
		refs = []ResultRef{}
	}
	refsJSON, err := json.Marshal(refs)
	if err != nil {
		return Job{}, false, fmt.Errorf("jobs: encode result refs %s: %w", jobID, err)
	}
	var job Job
	won := false
	var ev Event
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE jobs SET
				state = CASE state WHEN 'cancel_requested' THEN 'cancelled' ELSE 'succeeded' END,
				result_refs = CASE state WHEN 'running' THEN ? ELSE result_refs END,
				lease_owner = '', lease_until = NULL, updated_at = ?
			WHERE id = ? AND fencing_token = ? AND state IN ('running','cancel_requested')`,
			string(refsJSON), now.UnixNano(), jobID, token)
		if err != nil {
			return fmt.Errorf("jobs: complete %s: %w", jobID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("jobs: complete rows %s: %w", jobID, err)
		}
		if n == 0 {
			job, err = loadJob(ctx, tx, jobID)
			return err // won stays false; job carries the winning state
		}
		won = true
		job, err = loadJob(ctx, tx, jobID)
		if err != nil {
			return err
		}
		ev, err = s.appendEventTx(ctx, tx, job, now)
		return err
	})
	if err != nil {
		return Job{}, false, err
	}
	if won {
		s.notify(ev)
	}
	return job, won, nil
}

// Fail performs the winning terminal transition for a failed handler. Both
// running and cancel_requested may move to failed; a job that was already
// closed by another writer loses the race and won is false.
func (s *Store) Fail(ctx context.Context, jobID string, token int64, message string) (Job, bool, error) {
	now := s.clock.Now().UTC()
	var job Job
	won := false
	var ev Event
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE jobs SET state = 'failed', error = ?,
				lease_owner = '', lease_until = NULL, updated_at = ?
			WHERE id = ? AND fencing_token = ? AND state IN ('running','cancel_requested')`,
			message, now.UnixNano(), jobID, token)
		if err != nil {
			return fmt.Errorf("jobs: fail %s: %w", jobID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("jobs: fail rows %s: %w", jobID, err)
		}
		if n == 0 {
			job, err = loadJob(ctx, tx, jobID)
			return err
		}
		won = true
		job, err = loadJob(ctx, tx, jobID)
		if err != nil {
			return err
		}
		ev, err = s.appendEventTx(ctx, tx, job, now)
		return err
	})
	if err != nil {
		return Job{}, false, err
	}
	if won {
		s.notify(ev)
	}
	return job, won, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
