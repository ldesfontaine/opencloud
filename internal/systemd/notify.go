package systemd

import (
	"fmt"
	"net"
	"os"
)

// Notifier parle à systemd par le socket que NOTIFY_SOCKET désigne. Sans
// lui, chaque appel est un non-événement : le binaire se lance aussi à la
// main.
type Notifier struct {
	socketPath string
}

// NewNotifier lit NOTIFY_SOCKET et le retire de l'environnement, comme le
// recommande sd_notify(3) : ce que le service lancera plus tard n'héritera
// pas du socket (NotifyAccess=main dans l'unité est le vrai rempart ; ceci
// est la ceinture). À appeler une seule fois : un second appel ne verrait
// plus le socket.
func NewNotifier() *Notifier {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	// L'échec de Unsetenv ne change rien : le socket est déjà lu.
	_ = os.Unsetenv("NOTIFY_SOCKET")
	return &Notifier{socketPath: socketPath}
}

// Ready dit à systemd que le service écoute. À appeler une fois le port ouvert.
func (n *Notifier) Ready() error {
	return n.notify("READY=1")
}

// Stopping dit à systemd que l'arrêt a commencé.
func (n *Notifier) Stopping() error {
	return n.notify("STOPPING=1")
}

func (n *Notifier) notify(state string) error {
	if n.socketPath == "" {
		return nil
	}

	address := &net.UnixAddr{Name: n.socketPath, Net: "unixgram"}
	connection, err := net.DialUnix("unixgram", nil, address)
	if err != nil {
		return fmt.Errorf("dial notify socket: %w", err)
	}
	defer connection.Close()

	if _, err := connection.Write([]byte(state)); err != nil {
		return fmt.Errorf("write notify state: %w", err)
	}
	return nil
}
