// Point d'entrée du binaire. Il ne fait qu'aiguiller vers la commande demandée.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/cli"
)

// Renseignée à la compilation par -ldflags, depuis le tag git.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], version, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "opencloud:", err)
		os.Exit(1)
	}
}
