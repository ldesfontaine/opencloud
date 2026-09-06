// Point d'entrée du binaire. Il ne fait qu'aiguiller vers la commande demandée.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/cli"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Un refus n'est pas une erreur : cause et remède tels quels, code 2.
const exitCodeRefused = 2

// Renseignée à la compilation par -ldflags, depuis le tag git.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := cli.Run(ctx, os.Args[1:], version, os.Stdout, os.Stderr)
	if err == nil {
		return
	}

	var refused refusal.Refusal
	if errors.As(err, &refused) {
		fmt.Fprintln(os.Stderr, refused.Error())
		os.Exit(exitCodeRefused)
	}
	fmt.Fprintln(os.Stderr, "opencloud:", err)
	os.Exit(1)
}
