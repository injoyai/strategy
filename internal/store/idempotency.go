package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// SQLiteIdempotencyStore persists idempotency claims in the
// idempotency_records table. It mirrors the semantics of the in-memory store
// in internal/server: expired claims are reclaimed by Begin, and Commit
// refuses to resurrect a claim that no longer matches the request hash.
type SQLiteIdempotencyStore struct {
	db *sql.DB
}

var _ ports.IdempotencyStore = (*SQLiteIdempotencyStore)(nil)

// NewIdempotencyStore returns the durable IdempotencyStore backed by db.
func NewIdempotencyStore(db *sql.DB) *SQLiteIdempotencyStore {
	return &SQLiteIdempotencyStore{db: db}
}

// Begin claims (scope, key) atomically. The upsert either inserts a fresh
// claim or updates an expired one in a single statement, so concurrent
// callers get exactly one "claimed" outcome without a race window; the
// statement-level atomicity is backed by the busy timeout under contention.
func (s *SQLiteIdempotencyStore) Begin(scope, key, requestHash string, now time.Time) (*ports.IdempotencyRecord, bool, error) {
	nowNano := now.UnixNano()
	res, err := s.db.Exec(`
		INSERT INTO idempotency_records (scope, key, request_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (scope, key) DO UPDATE SET
			request_hash    = excluded.request_hash,
			response_status = 0,
			body            = NULL,
			location        = '',
			resource_id     = '',
			created_at      = excluded.created_at,
			expires_at      = excluded.expires_at
		WHERE expires_at <= ?`,
		scope, key, requestHash, nowNano, now.Add(ports.IdempotencyReplayWindow).UnixNano(), nowNano)
	if err != nil {
		return nil, false, fmt.Errorf("store: idempotency claim: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("store: idempotency claim result: %w", err)
	}
	if n > 0 {
		return nil, true, nil
	}
	// The statement matched an unexpired claim (0 rows changed): return it so
	// the caller can replay or conflict.
	rec, err := s.load(scope, key)
	if err != nil {
		return nil, false, err
	}
	return rec, false, nil
}

// Commit stores the response of a completed claim. Matching on the request
// hash ensures a handler that lost its claim (expired or reclaimed in the
// meantime) cannot overwrite another request's slot.
func (s *SQLiteIdempotencyStore) Commit(record ports.IdempotencyRecord) error {
	res, err := s.db.Exec(`
		UPDATE idempotency_records
		SET response_status = ?, body = ?, location = ?
		WHERE scope = ? AND key = ? AND request_hash = ?`,
		record.Status, orNullBytes(record.Body), record.Location,
		record.Scope, record.Key, record.RequestHash)
	if err != nil {
		return fmt.Errorf("store: idempotency commit: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: idempotency commit result: %w", err)
	}
	if n == 0 {
		return domain.NewError(domain.CodeIdempotencyConflict, "idempotency claim no longer active")
	}
	return nil
}

func (s *SQLiteIdempotencyStore) load(scope, key string) (*ports.IdempotencyRecord, error) {
	var (
		rec       ports.IdempotencyRecord
		createdAt int64
		expiresAt int64
		body      []byte
	)
	err := s.db.QueryRow(`
		SELECT scope, key, request_hash, response_status, body, location, created_at, expires_at
		FROM idempotency_records WHERE scope = ? AND key = ?`,
		scope, key).Scan(&rec.Scope, &rec.Key, &rec.RequestHash, &rec.Status,
		&body, &rec.Location, &createdAt, &expiresAt)
	if err != nil {
		return nil, fmt.Errorf("store: idempotency load: %w", err)
	}
	rec.Body = body
	rec.CreatedAt = time.Unix(0, createdAt).UTC()
	rec.ExpiresAt = time.Unix(0, expiresAt).UTC()
	return &rec, nil
}

// orNullBytes maps an empty body to SQL NULL so replays of empty responses
// stay distinguishable from absent ones.
func orNullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
