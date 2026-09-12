// Idempotency persistence port.
//
// The port lives here (not in server) so storage packages can implement it
// without depending on the HTTP layer. The scope layout encodes
// caller|workspace|method|path, so one workspace can never observe another
// caller's claims even when keys collide.
package ports

import "time"

// IdempotencyReplayWindow is the contract minimum ("24 hours or more")
// for claim and replay retention.
const IdempotencyReplayWindow = 24 * time.Hour

// IdempotencyRecord is the durable state of one idempotent request claim.
type IdempotencyRecord struct {
	Scope       string    `json:"scope"`
	Key         string    `json:"key"`
	RequestHash string    `json:"request_hash"`
	Status      int       `json:"status"` // 0 while the request is in flight
	Body        []byte    `json:"body,omitempty"`
	Location    string    `json:"location,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// InFlight reports whether the record has been claimed but not completed.
func (r IdempotencyRecord) InFlight() bool { return r.Status == 0 }

// IdempotencyStore persists idempotency claims. Begin must be atomic per
// (scope, key): exactly one concurrent caller claims; the others observe the
// existing record, including expired ones (claims are never reclaimed).
// Commit must refuse to overwrite a claim whose request hash differs.
type IdempotencyStore interface {
	Begin(scope, key, requestHash string, now time.Time) (existing *IdempotencyRecord, claimed bool, err error)
	Commit(record IdempotencyRecord) error
}
