package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrUnknownCommand : la commande demandée n'existe pas.
var ErrUnknownCommand = errors.New("commande inconnue")

// Run exécute la commande nommée par args[0]. out reçoit ce que l'opérateur
// lit, errOut le journal. ctx s'annule sur SIGINT ou SIGTERM.
func Run(ctx context.Context, args []string, version string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return usage(out)
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], version, errOut)
	case "status":
		return runStatus(ctx, args[1:], out, errOut)
	case "enroll-local":
		return runEnrollLocal(ctx, args[1:], out, errOut)
	case "self-update":
		return runSelfUpdate(ctx, args[1:], version, out, errOut)
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
	fmt.Fprintln(out, "usage : opencloud <commande> [--config chemin]")
	fmt.Fprintln(out, "  serve     démarre l'interface")
	fmt.Fprintln(out, "  status    vérifie la configuration, l'état et le service")
	fmt.Fprintln(out, "  enroll-local  enrôle cette machine sur elle-même par SSH vers localhost (sudo)")
	fmt.Fprintln(out, "  self-update  installe la release suivante en place (sudo) ; --check, --version vX.Y.Z")
	fmt.Fprintln(out, "  version   affiche la version")
	return nil
}
