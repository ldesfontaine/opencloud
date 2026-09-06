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
	unitName = "opencloud.service"
	// systemctl restart attend la fin du job : lui laisser plus que le délai de
	// systemd, sinon self-update le tue à l'instant où systemd allait conclure,
	// et personne ne dit ce qui s'est passé.
	restartGrace   = 30 * time.Second
	restartTimeout = UnitStartTimeout + restartGrace
	maxOutputBytes = 2000
)

// UnitStartTimeout reprend TimeoutStartSec de packaging/opencloud.service : le
// temps que systemd laisse au service pour sauvegarder, migrer et répondre
// READY=1. C'est la seule référence des délais de self-update, et
// restart_test.go vérifie qu'elle ne dérive pas de l'unité.
const UnitStartTimeout = 120 * time.Second

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
