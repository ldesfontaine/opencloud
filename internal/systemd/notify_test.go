package systemd

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifyReady_WithoutSocket_DoesNothing(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	if err := NotifyReady(); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
}

func TestNotifyReady_WithSocket_SendsReady(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "notify")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", socketPath)

	if err := NotifyReady(); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	listener.SetReadDeadline(time.Now().Add(2 * time.Second))
	received := make([]byte, 64)
	length, err := listener.Read(received)
	if err != nil {
		t.Fatalf("rien reçu : %v", err)
	}
	if string(received[:length]) != "READY=1" {
		t.Fatalf("reçu %q, attendu READY=1", received[:length])
	}
}
