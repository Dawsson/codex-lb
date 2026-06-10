package cache

import (
	"testing"
	"time"
)

func TestTTLReturnsValueBeforeExpiry(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	cache := NewTTL[string](time.Second)
	cache.now = func() time.Time { return now }

	cache.Set("key", "value")

	got, ok := cache.Get("key")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got != "value" {
		t.Fatalf("expected value, got %q", got)
	}
}

func TestTTLMissesAfterExpiry(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	cache := NewTTL[string](time.Second)
	cache.now = func() time.Time { return now }
	cache.Set("key", "value")

	now = now.Add(time.Second)

	_, ok := cache.Get("key")
	if ok {
		t.Fatal("expected cache miss after expiry")
	}
}
