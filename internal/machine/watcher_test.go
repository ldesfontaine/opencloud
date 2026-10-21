package machine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

type recordingWatcher struct {
	changes []string
}

func (w *recordingWatcher) MachineChanged(id string) {
	w.changes = append(w.changes, id)
}

// Tout ce que l'interface montre d'une machine passe par le crochet : le
// jeton, l'enrôlement, la connexion, la déconnexion, le retrait. Le signal
// n'y passe pas : il ne change rien de ce que l'écran montre.
func TestWatcher_HearsEveryVisibleChange(t *testing.T) {
	h := newHarness(t)
	watcher := &recordingWatcher{}
	h.service.SetWatcher(watcher)
	ctx := context.Background()

	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	const id = "11111111-2222-4333-8444-555555555555"
	enrolled, err := h.service.Enroll(ctx, Enrollment{MachineID: id, PublicKey: public, Token: cleartext})
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Signal(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	h.service.Disconnect(session)
	if err := h.service.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	want := []string{"", id, id, id, id}
	if len(watcher.changes) != len(want) {
		t.Fatalf("changes %v, want %v", watcher.changes, want)
	}
	for i, got := range watcher.changes {
		if got != want[i] {
			t.Fatalf("change %d: %q, want %q", i, got, want[i])
		}
	}
}

func TestWatcher_NilIsTolerated(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.service.CreateToken(context.Background(), "vps-paris-1", nil); err != nil {
		t.Fatal(err)
	}
}

// Une machine en ligne qui signale ne publie rien : seul son dernier signal
// bouge. Signal après signal, le crochet reste muet.
func TestWatcher_SignalWithoutChangeDoesNotPublish(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(context.Background(), m, "192.0.2.1", "v0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	watcher := &recordingWatcher{}
	h.service.SetWatcher(watcher)
	for range 3 {
		h.now = h.now.Add(30 * time.Second)
		if _, err := h.service.Signal(context.Background(), session.Token); err != nil {
			t.Fatal(err)
		}
	}
	if len(watcher.changes) != 0 {
		t.Fatalf("signals published %v", watcher.changes)
	}
}

// Le passage hors ligne se voit : la fin du flux publie la machine.
func TestWatcher_GoingOfflinePublishes(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(context.Background(), m, "192.0.2.1", "v0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	watcher := &recordingWatcher{}
	h.service.SetWatcher(watcher)
	h.service.Disconnect(session)
	status, err := h.service.Get(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Online || len(watcher.changes) != 1 || watcher.changes[0] != m.ID {
		t.Fatalf("online %v, changes %v", status.Online, watcher.changes)
	}
}
