// Package ports defines small replaceable interfaces for side-effecting
// concerns (time, ids, checksums). Production wires real implementations;
// tests inject deterministic fakes so no test depends on the wall clock or
// process-global randomness.
package ports

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// Clock supplies current time. All system time flows through this port so
// tests can freeze or advance it deterministically.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production clock; it always reports UTC.
type SystemClock struct{}

// Now returns the current wall time normalized to UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FixedClock is a deterministic clock for tests.
type FixedClock struct {
	current time.Time
}

// NewFixedClock returns a clock pinned at current (normalized to UTC).
func NewFixedClock(current time.Time) *FixedClock {
	return &FixedClock{current: current.UTC()}
}

// Now returns the frozen time.
func (c *FixedClock) Now() time.Time { return c.current }

// Advance moves the frozen time forward by d.
func (c *FixedClock) Advance(d time.Duration) { c.current = c.current.Add(d) }

// Set pins the frozen time at t (normalized to UTC).
func (c *FixedClock) Set(t time.Time) { c.current = t.UTC() }

// IDGenerator produces opaque identifiers.
type IDGenerator interface {
	NewID() (domain.ID, error)
}

// randomIDBytes is the entropy per generated id: 128 bits of randomness.
const randomIDBytes = 16

// RandomIDGenerator is the production id source: 16 random bytes hex-encoded,
// optionally prefixed ("prefix_hex") for operator readability.
type RandomIDGenerator struct {
	Prefix string
}

// NewID returns a fresh validated id. Read failures are wrapped as
// internal.error rather than silently reused or zero-filled.
func (g RandomIDGenerator) NewID() (domain.ID, error) {
	buf := make([]byte, randomIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", domain.Wrap(err, domain.CodeInternalError, "id: random source failed")
	}
	s := hex.EncodeToString(buf)
	if g.Prefix != "" {
		s = g.Prefix + "_" + s
	}
	return domain.ParseID(s)
}

// Checksummer computes a stable content digest.
type Checksummer interface {
	Checksum([]byte) string
}

// SHA256Checksummer digests content with SHA-256, hex-encoded.
type SHA256Checksummer struct{}

// Checksum returns the hex-encoded SHA-256 of data.
func (SHA256Checksummer) Checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
