package trading212

import (
	"context"
	"strings"
	"sync"
	"time"
)

// CacheObservation describes how one upstream call was served.
type CacheObservation struct {
	CacheHit    bool      `json:"cacheHit"`
	RetrievedAt time.Time `json:"retrievedAt"`
	TTLSeconds  float64   `json:"ttlSeconds"`
}

// Observer collects cache observations for the calls made with a context.
type Observer struct {
	mu   sync.Mutex
	list []CacheObservation
}

// Observations returns a copy of the recorded observations.
func (o *Observer) Observations() []CacheObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]CacheObservation(nil), o.list...)
}

func (o *Observer) record(obs CacheObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.list = append(o.list, obs)
}

type observerKey struct{}

// WithObserver returns a context whose client calls are recorded by the
// returned Observer.
func WithObserver(ctx context.Context) (context.Context, *Observer) {
	o := &Observer{}
	return context.WithValue(ctx, observerKey{}, o), o
}

func observe(ctx context.Context, obs CacheObservation) {
	if o, ok := ctx.Value(observerKey{}).(*Observer); ok {
		o.record(obs)
	}
}

// TTLs configures cache lifetimes per endpoint family. A zero TTL disables
// caching for that family.
type TTLs struct {
	Account  time.Duration
	History  time.Duration
	Metadata time.Duration
}

func (t TTLs) forPath(path string) time.Duration {
	switch {
	case strings.HasPrefix(path, "/equity/metadata/"):
		return t.Metadata
	case strings.HasPrefix(path, "/equity/history/") && !strings.HasPrefix(path, "/equity/history/exports"):
		return t.History
	default:
		return t.Account
	}
}

type cacheEntry struct {
	body        []byte
	retrievedAt time.Time
	generation  uint64
}

// responseCache is an in-memory GET response cache. Any mutation bumps the
// generation, making every account-scoped entry stale; instrument and exchange
// metadata is unaffected by account mutations and survives.
type responseCache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	generation uint64
	now        func() time.Time
}

func newResponseCache(now func() time.Time) *responseCache {
	return &responseCache{entries: map[string]cacheEntry{}, now: now}
}

func (c *responseCache) get(key string, ttl time.Duration, metadata bool) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	if (!metadata && e.generation != c.generation) || c.now().Sub(e.retrievedAt) >= ttl {
		delete(c.entries, key)
		return cacheEntry{}, false
	}
	return e, true
}

// currentGeneration must be read before a request is sent so that a response
// racing with a concurrent mutation is stored as already stale.
func (c *responseCache) currentGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *responseCache) put(key string, body []byte, retrievedAt time.Time, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{body: body, retrievedAt: retrievedAt, generation: generation}
}

func (c *responseCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for k := range c.entries {
		if !strings.Contains(k, "/equity/metadata/") {
			delete(c.entries, k)
		}
	}
}
