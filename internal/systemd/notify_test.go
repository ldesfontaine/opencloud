package systemd

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifier_WithoutSocket_DoesNothing(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	if err := NewNotifier().Ready(); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
}

func TestNotifier_WithSocket_SendsReadyThenStoppingAndHidesTheSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "notify")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", socketPath)

	notifier := NewNotifier()
	if _, stillSet := os.LookupEnv("NOTIFY_SOCKET"); stillSet {
		t.Fatal("NOTIFY_SOCKET doit sortir de l'environnement une fois lu")
	}
	if err := notifier.Ready(); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if err := notifier.Stopping(); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	for _, expected := range []string{"READY=1", "STOPPING=1"} {
		listener.SetReadDeadline(time.Now().Add(2 * time.Second))
		received := make([]byte, 64)
		length, err := listener.Read(received)
		if err != nil {
			t.Fatalf("rien reçu pour %s : %v", expected, err)
		}
		if string(received[:length]) != expected {
			t.Fatalf("reçu %q, attendu %s", received[:length], expected)
		}
	}
}
