package cache

import (
	"sync"
	"time"
)

type Entry[T any] struct {
	value     T
	expiresAt time.Time
	hasValue  bool
}

type TTL[T any] struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]Entry[T]
	now     func() time.Time
}

func NewTTL[T any](ttl time.Duration) *TTL[T] {
	return &TTL[T]{
		ttl:     ttl,
		entries: make(map[string]Entry[T]),
		now:     time.Now,
	}
}

func (c *TTL[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	now := c.now()
	c.mu.RUnlock()

	var zero T
	if !ok || !entry.hasValue || !now.Before(entry.expiresAt) {
		return zero, false
	}
	return entry.value, true
}

func (c *TTL[T]) Set(key string, value T) {
	c.mu.Lock()
	c.entries[key] = Entry[T]{
		value:     value,
		expiresAt: c.now().Add(c.ttl),
		hasValue:  true,
	}
	c.mu.Unlock()
}

func (c *TTL[T]) Delete(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *TTL[T]) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]Entry[T])
	c.mu.Unlock()
}
