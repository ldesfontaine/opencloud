package waittest

import (
	"testing"
	"time"
)

const (
	limit        = 30 * time.Second
	pollInterval = 2 * time.Millisecond
)

// reporter est ce que poll demande à un test : de quoi échouer.
type reporter interface {
	Helper()
	Fatalf(format string, args ...any)
}

// Until relit la condition jusqu'à ce qu'elle soit vraie ; échoue au bout de
// 30 s en nommant ce qu'on attendait.
func Until(t testing.TB, what string, condition func() bool) {
	t.Helper()
	poll(t, what, condition, limit)
}

func poll(t reporter, what string, condition func() bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
			return
		}
		time.Sleep(pollInterval)
	}
}
