package enroll

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// prober : de quoi savoir si la machine répond. La machine openCloud et une
// machine distante se vérifient de la même façon.
type prober interface {
	Probe(ctx context.Context) error
}

// probeWithRetries insiste un peu : le rechargement de sshd ferme le port un
// instant, et on ne conclut pas à l'injoignable au premier essai.
func probeWithRetries(ctx context.Context, client prober, address string, wait time.Duration) error {
	var last error
	for attempt := 1; attempt <= probeAttempts; attempt++ {
		last = client.Probe(ctx)
		if last == nil {
			return nil
		}
		if attempt == probeAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return refusal.Refusal{
		Cause:  fmt.Sprintf("la machine ne répond pas en SSH sur %s après %d essais : %v", address, probeAttempts, last),
		Remedy: "vérifier que ssh.service écoute et que le compte opencloud accepte sa clé, puis rejouer l'enrôlement",
	}
}

// checkLauncherReply relit ce que le lanceur a répondu derrière sudo : appelé
// sans identifiant, il refuse, et c'est cette preuve-là qu'on veut.
func checkLauncherReply(reply transport.LauncherReply) error {
	if strings.Contains(reply.Output, "a password is required") {
		return refusal.Refusal{
			Cause:  "sudo demande un mot de passe au compte opencloud : la règle sudo n'a pas pris",
			Remedy: "vérifier /etc/sudoers.d/opencloud et l'ordre des règles de /etc/sudoers sur cette machine, puis rejouer l'enrôlement",
		}
	}
	if reply.ExitCode != catalog.ExitRefused {
		return refusal.Refusal{
			Cause: fmt.Sprintf("le lanceur appelé sans identifiant répond %d au lieu de %d : %s",
				reply.ExitCode, catalog.ExitRefused, reply.Output),
			Remedy: "vérifier /usr/local/sbin/oc-launch et la règle sudo sur cette machine, puis rejouer l'enrôlement",
		}
	}
	return nil
}
