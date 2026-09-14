package factor

import (
	"sync"

	"github.com/injoyai/strategy/internal/domain"
)

// Cache is the in-process staged result cache. A run stages its frame,
// publishes it once the full partition passed validation, and can abort a
// staged entry — staged entries are invisible to Get, so a cancelled or
// failed run never leaves a hit behind.
type Cache struct {
	mu        sync.Mutex
	staged    map[string]*Frame
	published map[string]*Frame
}

// NewCache builds an empty cache.
func NewCache() *Cache {
	return &Cache{
		staged:    make(map[string]*Frame),
		published: make(map[string]*Frame),
	}
}

// Get returns the published frame for key, if any.
func (c *Cache) Get(key string) (*Frame, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	frame, ok := c.published[key]
	return frame, ok
}

// Stage records frame under key as a candidate hit, replacing any earlier
// staged frame for the same key.
func (c *Cache) Stage(key string, frame *Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staged[key] = frame
}

// Publish promotes the staged frame for key to a visible hit.
func (c *Cache) Publish(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	frame, ok := c.staged[key]
	if !ok {
		return domain.NewError(codeCacheState, "factor: no staged frame for key %q", truncateKey(key))
	}
	delete(c.staged, key)
	c.published[key] = frame
	return nil
}

// Abort discards the staged frame for key.
func (c *Cache) Abort(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.staged, key)
}

// truncateKey bounds a key inside error messages.
func truncateKey(key string) string {
	if len(key) > 16 {
		return key[:16] + "..."
	}
	return key
}
