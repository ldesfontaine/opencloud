// Package systemd prévient systemd de l'état du service par sd_notify, pour
// une unité Type=notify. Sans NOTIFY_SOCKET, chaque appel est un non-événement :
// le binaire se lance aussi à la main.
package systemd

import (
	"fmt"
	"net"
	"os"
)

// NotifyReady dit à systemd que le service écoute. À appeler une fois le port ouvert.
func NotifyReady() error {
	return notify("READY=1")
}

// NotifyStopping dit à systemd que l'arrêt a commencé.
func NotifyStopping() error {
	return notify("STOPPING=1")
}

func notify(state string) error {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return nil
	}

	address := &net.UnixAddr{Name: socketPath, Net: "unixgram"}
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
