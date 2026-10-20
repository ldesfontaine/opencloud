package wait

import (
	"context"
	"testing"
	"time"
)

func TestFor_ElapsedDelayIsTrue(t *testing.T) {
	if !For(context.Background(), time.Millisecond) {
		t.Fatal("a delay that elapsed must say true")
	}
}

func TestFor_ZeroDelayDoesNotWait(t *testing.T) {
	if !For(context.Background(), 0) {
		t.Fatal("a zero delay on a live context must say true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if For(ctx, 0) {
		t.Fatal("a zero delay on a cancelled context must say false")
	}
}

// Annulé pendant l'attente : la réponse vient tout de suite, pas au bout
// de l'heure.
func TestFor_CancelStopsTheWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() { done <- For(ctx, time.Hour) }()
	cancel()
	select {
	case elapsed := <-done:
		if elapsed {
			t.Fatal("a cancelled wait must say false")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the wait did not stop on cancel")
	}
}
