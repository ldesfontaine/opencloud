package ratelimit

import (
	"testing"
	"time"
)

const testIdle = 10 * time.Minute

func newTestLimiter(rate float64, burst int) (*Limiter, *time.Time) {
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	limiter := New(rate, burst, testIdle)
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

// Le balayage passe avec une requête, au plus une fois par période, et
// n'oublie que les seaux silencieux depuis plus que l'oisiveté.
func TestAllow_SweepsIdleKeysOnlyOncePerPeriod(t *testing.T) {
	limiter, now := newTestLimiter(1, 1)
	limiter.Allow("old")
	*now = now.Add(testIdle + time.Minute)
	limiter.Allow("fresh")
	if limiter.Size() != 1 {
		t.Fatalf("after the sweep: size %d, want 1", limiter.Size())
	}
	limiter.Allow("other")
	*now = now.Add(testIdle + time.Minute)
	limiter.Allow("again")
	*now = now.Add(sweepEvery - time.Second)
	limiter.Allow("late")
	// « fresh » et « other » sont partis au balayage d'« again » ; celui de
	// « late » n'est pas encore dû.
	if limiter.Size() != 2 {
		t.Fatalf("before the next period: size %d, want 2", limiter.Size())
	}
}

// Deux adresses d'un même /64 partagent un seau ; un autre /64 a le sien.
func TestBySource_SharesOneBucketPerIPv6SixtyFour(t *testing.T) {
	limiter := NewBySource(1, 2, testIdle)
	if !limiter.Allow("2001:db8:9:9::1") || !limiter.Allow("2001:db8:9:9::2") {
		t.Fatal("the burst must pass")
	}
	if limiter.Allow("2001:db8:9:9::3") {
		t.Fatal("a third address of the same /64 got its own bucket")
	}
	if !limiter.Allow("2001:db8:9:a::1") || !limiter.Allow("203.0.113.9") {
		t.Fatal("another source must have its own bucket")
	}
}
