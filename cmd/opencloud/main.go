// Le seul main : il aiguille vers la commande demandée.
package main

import (
	"errors"
	"fmt"
	"os"
)

const usage = `Usage : opencloud <commande> [options]

Commandes :
  serve     démarre le serveur web (option -config)
  agent     tourne sur une machine gérée (options -server -token -lang -pin -allow-plain -state)
  version   affiche la version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "agent":
		err = runAgent(os.Args[2:])
	case "version":
		err = runVersion()
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue : %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "opencloud :", err)
		var refused *refusal
		if errors.As(err, &refused) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
