// Point d'entrée du binaire. Il ne fait qu'aiguiller vers la commande demandée.
package main

import (
	"fmt"
	"os"

	"github.com/ldesfontaine/opencloud/internal/cli"
)

// Renseignée à la compilation par -ldflags, depuis le tag git.
var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], version, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "opencloud:", err)
		os.Exit(1)
	}
}
