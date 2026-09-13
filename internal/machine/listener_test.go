package machine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

type recordingListener struct {
	changes []string
}

func (l *recordingListener) MachineChanged(id string) {
	l.changes = append(l.changes, id)
}

// Tout ce que l'interface montre d'une machine passe par le crochet : le
// jeton, l'enrôlement, la connexion, le signal, la déconnexion, le retrait.
func TestListener_HearsEveryVisibleChange(t *testing.T) {
	h := newHarness(t)
	listener := &recordingListener{}
	h.service.SetListener(listener)
	ctx := context.Background()

	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	const id = "11111111-2222-4333-8444-555555555555"
	if _, err := h.service.Enroll(ctx, Enrollment{MachineID: id, PublicKey: public, Token: cleartext}); err != nil {
		t.Fatal(err)
	}
	session, err := h.service.Connect(ctx, id, "192.0.2.1", "v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.service.Signal(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	h.service.Disconnect(session)
	if err := h.service.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	want := []string{"", id, id, id, id, id}
	if len(listener.changes) != len(want) {
		t.Fatalf("changes %v, want %v", listener.changes, want)
	}
	for i, got := range listener.changes {
		if got != want[i] {
			t.Fatalf("change %d: %q, want %q", i, got, want[i])
		}
	}
}

func TestListener_NilIsTolerated(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.service.CreateToken(context.Background(), "vps-paris-1"); err != nil {
		t.Fatal(err)
	}
}
