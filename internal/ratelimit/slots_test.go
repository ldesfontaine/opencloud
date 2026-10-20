package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Les places du direct se prennent d'un geste : sous la concurrence,
// jamais une de plus que la borne.
func TestSlots_ConcurrentTakers_NeverExceedTheLimit(t *testing.T) {
	const limit = 64
	places := NewSlots(limit)
	var taken atomic.Int32
	var takers sync.WaitGroup
	for range 500 {
		takers.Add(1)
		go func() {
			defer takers.Done()
			if places.Take() {
				taken.Add(1)
			}
		}()
	}
	takers.Wait()
	if taken.Load() != limit || places.Taken() != limit {
		t.Fatalf("taken %d, counted %d", taken.Load(), places.Taken())
	}
}

func TestSourceSlots_TakeUpToTheLimitThenGiveBack(t *testing.T) {
	places := NewSourceSlots(2)
	for range 2 {
		if !places.Take("a") {
			t.Fatal("a source must get its share")
		}
	}
	if places.Take("a") {
		t.Fatal("a source must not get more than its share")
	}
	if !places.Take("b") {
		t.Fatal("another source has its own share")
	}
	places.Release("a")
	if !places.Take("a") {
		t.Fatal("a released place must be taken again")
	}
	places.Release("a")
	places.Release("a")
	places.Release("b")
	if places.Sources() != 0 {
		t.Fatalf("released sources must leave no entry: %d", places.Sources())
	}
}
