package heartbeat_test

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

type recordingWatcher struct {
	changes int
}

func (w *recordingWatcher) HeartbeatChanged(string) {
	w.changes++
}

// Le direct entend chaque changement d'un moniteur : création, ping,
// échéance dépassée, pause, reprise, suppression.
func TestWatcher_HearsEveryChange(t *testing.T) {
	f := newFixture(t)
	watcher := &recordingWatcher{}
	f.SetWatcher(watcher)
	ctx := context.Background()

	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(10 * time.Minute)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Pause(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if watcher.changes != 6 {
		t.Fatalf("%d changes, want 6", watcher.changes)
	}
	// Une échéance déjà dépassée ne rechange rien.
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if watcher.changes != 6 {
		t.Fatalf("%d changes after a quiet check, want 6", watcher.changes)
	}
}
