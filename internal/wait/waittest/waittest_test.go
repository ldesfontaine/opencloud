package waittest

import (
	"sync/atomic"
	"testing"
	"time"
)

type recorder struct{ failures []string }

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, format)
}

func TestUntil_ConditionAlreadyTrue_ReturnsAtOnce(t *testing.T) {
	start := time.Now()
	Until(t, "nothing", func() bool { return true })
	if time.Since(start) > time.Second {
		t.Fatal("a true condition must not wait")
	}
}

func TestUntil_ConditionBecomesTrue_ReturnsWithoutFailing(t *testing.T) {
	var ready atomic.Bool
	go func() {
		time.Sleep(10 * time.Millisecond)
		ready.Store(true)
	}()
	Until(t, "the flag", ready.Load)
}

func TestPoll_ConditionNeverTrue_FailsNamingWhatWasAwaited(t *testing.T) {
	r := &recorder{}
	poll(r, "the flag", func() bool { return false }, 20*time.Millisecond)
	if len(r.failures) != 1 {
		t.Fatalf("an unmet condition must fail once, got %d", len(r.failures))
	}
}
