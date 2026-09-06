package enroll

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Aucune commande de l'amorçage ne travaille : elles écrivent une ligne ou
// rechargent un service.
const commandTimeout = 30 * time.Second

// Ces binaires sont à nous et ne parlent pas ; on garde de quoi lire un refus,
// pas de quoi remplir la mémoire.
const maxCommandOutputBytes = 4 << 10

// Result est ce qu'une commande système a répondu. Un code non nul n'est pas
// une erreur : c'est une réponse, que l'étape interprète.
type Result struct {
	Output   string
	ExitCode int
}

// CommandRunner joue un binaire système par chemin absolu. Les tests en posent
// un faux : rien de l'amorçage ne s'exécute sur le poste de développement.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

// SystemCommands est la seule mise en œuvre réelle : pas de shell, chemin
// absolu, environnement remplacé, délai borné.
type SystemCommands struct{}

func (SystemCommands) Run(ctx context.Context, name string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, name, args...) // #nosec G204 -- bounded: binaires nommés par des constantes, arguments constants ou chemins dérivés
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.Dir = "/"

	output, err := command.CombinedOutput()
	text := truncate(strings.TrimSpace(string(output)))

	var exitError *exec.ExitError
	switch {
	case err == nil:
		return Result{Output: text}, nil
	case ctx.Err() != nil:
		return Result{}, fmt.Errorf("%s : %w", name, ctx.Err())
	case errors.As(err, &exitError):
		return Result{Output: text, ExitCode: exitError.ExitCode()}, nil
	default:
		return Result{}, fmt.Errorf("jouer %s : %w", name, err)
	}
}

func truncate(text string) string {
	if len(text) <= maxCommandOutputBytes {
		return text
	}
	return text[:maxCommandOutputBytes] + "… (sortie tronquée)"
}
