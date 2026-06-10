package proxy

import (
	"sync"
	"time"
)

// AccountCacheKey mirrors the _CacheKey tuple used by
// app.modules.proxy.account_cache.AccountSelectionCache: a tuple of
// (model, traffic class, routing key, assigned account IDs).
type AccountCacheKey struct {
	Model              string
	TrafficClass       string
	RoutingKey         string
	AssignedAccountIDs string
}

// AccountSelectionCache mirrors
// app.modules.proxy.account_cache.AccountSelectionCache: a short-lived,
// generation-stamped cache of account selection inputs keyed by request
// shape. T is the cached payload type (the Go equivalent of
// load_balancer.SelectionInputs, ported separately).
type AccountSelectionCache[T any] struct {
	ttl   time.Duration
	mu    sync.Mutex
	cache map[AccountCacheKey]cachedSelectionInputs[T]
	gen   int
}

type cachedSelectionInputs[T any] struct {
	data      T
	expiresAt time.Time
}

// NewAccountSelectionCache constructs a cache with the given TTL. A TTL of
// zero disables caching entirely, matching the Python implementation's
// pytest behavior.
func NewAccountSelectionCache[T any](ttl time.Duration) *AccountSelectionCache[T] {
	return &AccountSelectionCache[T]{
		ttl:   ttl,
		cache: make(map[AccountCacheKey]cachedSelectionInputs[T]),
	}
}

// Generation returns the current invalidation generation.
func (c *AccountSelectionCache[T]) Generation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// Get ports AccountSelectionCache.get.
func (c *AccountSelectionCache[T]) Get(key AccountCacheKey) (T, bool) {
	var zero T
	if c.ttl <= 0 {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok {
		return zero, false
	}
	if time.Now().After(entry.expiresAt) {
		return zero, false
	}
	return entry.data, true
}

// Set ports AccountSelectionCache.set. If generation is non-nil and does not
// match the cache's current generation, the write is dropped (it raced with
// an Invalidate call).
func (c *AccountSelectionCache[T]) Set(data T, key AccountCacheKey, generation *int) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != nil && *generation != c.gen {
		return
	}
	c.cache[key] = cachedSelectionInputs[T]{
		data:      data,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate ports AccountSelectionCache.invalidate: bumps the generation
// counter and clears all cached entries.
func (c *AccountSelectionCache[T]) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.cache = make(map[AccountCacheKey]cachedSelectionInputs[T])
}
