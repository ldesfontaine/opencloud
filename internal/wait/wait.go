package wait

import (
	"context"
	"time"
)

// For attend la durée et dit vrai, ou dit faux dès que le contexte est
// annulé. Une durée nulle ou négative n'attend pas.
func For(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
