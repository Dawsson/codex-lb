package proxy

import (
	"testing"
	"time"
)

func TestAccountSelectionCacheGetSetMiss(t *testing.T) {
	cache := NewAccountSelectionCache[string](time.Minute)
	key := AccountCacheKey{Model: "gpt-5.5"}

	if _, ok := cache.Get(key); ok {
		t.Fatalf("expected cache miss before set")
	}

	cache.Set("selection", key, nil)

	got, ok := cache.Get(key)
	if !ok || got != "selection" {
		t.Fatalf("expected cache hit with %q, got %q (ok=%v)", "selection", got, ok)
	}

	other := AccountCacheKey{Model: "gpt-5.4"}
	if _, ok := cache.Get(other); ok {
		t.Fatalf("expected cache miss for different key")
	}
}

func TestAccountSelectionCacheZeroTTLDisablesCaching(t *testing.T) {
	cache := NewAccountSelectionCache[string](0)
	key := AccountCacheKey{Model: "gpt-5.5"}

	cache.Set("selection", key, nil)

	if _, ok := cache.Get(key); ok {
		t.Fatalf("expected zero-TTL cache to never hit")
	}
}

func TestAccountSelectionCacheExpiry(t *testing.T) {
	cache := NewAccountSelectionCache[string](time.Millisecond)
	key := AccountCacheKey{Model: "gpt-5.5"}

	cache.Set("selection", key, nil)
	time.Sleep(5 * time.Millisecond)

	if _, ok := cache.Get(key); ok {
		t.Fatalf("expected expired entry to miss")
	}
}

func TestAccountSelectionCacheInvalidate(t *testing.T) {
	cache := NewAccountSelectionCache[string](time.Minute)
	key := AccountCacheKey{Model: "gpt-5.5"}

	gen := cache.Generation()
	cache.Set("selection", key, &gen)

	if _, ok := cache.Get(key); !ok {
		t.Fatalf("expected cache hit before invalidate")
	}

	cache.Invalidate()

	if _, ok := cache.Get(key); ok {
		t.Fatalf("expected invalidate to clear cache")
	}
	if cache.Generation() != gen+1 {
		t.Fatalf("expected generation to increment, got %d", cache.Generation())
	}
}

func TestAccountSelectionCacheStaleGenerationSetIsDropped(t *testing.T) {
	cache := NewAccountSelectionCache[string](time.Minute)
	key := AccountCacheKey{Model: "gpt-5.5"}

	staleGen := cache.Generation()
	cache.Invalidate()

	cache.Set("selection", key, &staleGen)

	if _, ok := cache.Get(key); ok {
		t.Fatalf("expected stale-generation set to be dropped")
	}
}
