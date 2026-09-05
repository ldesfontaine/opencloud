// Package cli aiguille les commandes du binaire : serve, enroll-command,
// status, version. Il ne contient aucune logique métier.
package cli

import (
	"errors"
	"fmt"
	"io"
)

// ErrUnknownCommand : la commande demandée n'existe pas.
var ErrUnknownCommand = errors.New("commande inconnue")

// Run exécute la commande nommée par args[0] et écrit sa sortie dans out.
func Run(args []string, version string, out io.Writer) error {
	if len(args) == 0 {
		return usage(out)
	}

	switch args[0] {
	case "version":
		fmt.Fprintln(out, "opencloud", version)
		return nil
	case "help", "-h", "--help":
		return usage(out)
	default:
		return fmt.Errorf("%w : %s", ErrUnknownCommand, args[0])
	}
}

func usage(out io.Writer) error {
	fmt.Fprintln(out, "usage : opencloud <commande>")
	fmt.Fprintln(out, "  version   affiche la version")
	return nil
}
