package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	unitName       = "opencloud.service"
	restartTimeout = 90 * time.Second
	maxOutputBytes = 2000
)

// ErrNoSystemd : pas de systemd sur cette machine — le service se relance à la main.
var ErrNoSystemd = errors.New("systemd is not running")

// RestartUnit relance l'unité par systemctl : sans shell, chemin absolu,
// environnement remplacé, délai borné.
func RestartUnit(ctx context.Context) error {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return ErrNoSystemd
	}
	systemctl, err := findSystemctl()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, restartTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, systemctl, "restart", unitName) // #nosec G204 -- bounded: chemins et arguments constants
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}

	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %w: %s", unitName, err, firstBytes(output, maxOutputBytes))
	}
	return nil
}

// Debian récent le pose dans /usr/bin, un système non fusionné dans /bin.
func findSystemctl() (string, error) {
	for _, candidate := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("systemctl not found")
}

func firstBytes(output []byte, count int) string {
	text := strings.TrimSpace(string(output))
	if len(text) > count {
		return text[:count]
	}
	return text
}
