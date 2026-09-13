package ratelimit

import (
	"testing"
	"time"
)

func newTestLimiter(rate float64, burst int) (*Limiter, *time.Time) {
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	limiter := New(rate, burst)
	limiter.SetClock(func() time.Time { return now })
	return limiter, &now
}

func TestAllow_BurstThenRefillAtRate(t *testing.T) {
	limiter, now := newTestLimiter(1, 3)
	for i := range 3 {
		if !limiter.Allow("ip") {
			t.Fatalf("request %d of the burst refused", i+1)
		}
	}
	if limiter.Allow("ip") {
		t.Fatal("fourth request passed with an empty bucket")
	}
	*now = now.Add(500 * time.Millisecond)
	if limiter.Allow("ip") {
		t.Fatal("half a unit is not a unit")
	}
	*now = now.Add(500 * time.Millisecond)
	if !limiter.Allow("ip") {
		t.Fatal("one second later, one request must pass")
	}
	*now = now.Add(time.Hour)
	for i := range 3 {
		if !limiter.Allow("ip") {
			t.Fatalf("after a long rest the burst is back, request %d refused", i+1)
		}
	}
	if limiter.Allow("ip") {
		t.Fatal("the bucket never holds more than the burst")
	}
}

func TestAllow_KeysAreIndependent(t *testing.T) {
	limiter, _ := newTestLimiter(1, 1)
	if !limiter.Allow("a") || limiter.Allow("a") || !limiter.Allow("b") {
		t.Fatal("keys share a bucket")
	}
}

func TestSweep_ForgetsIdleKeysOnly(t *testing.T) {
	limiter, now := newTestLimiter(1, 1)
	limiter.Allow("old")
	*now = now.Add(10 * time.Minute)
	limiter.Allow("fresh")
	limiter.Sweep(5 * time.Minute)
	if limiter.Size() != 1 {
		t.Fatalf("size %d", limiter.Size())
	}
}
