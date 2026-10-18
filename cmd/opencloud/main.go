// Le seul main : il aiguille vers la commande demandée.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const usage = `Usage : {command} <commande> [options]

Commandes :
  serve     démarre le serveur web (option -config)
  agent     tourne sur une machine gérée (options -server -token -lang -pin -allow-plain -state)
  mcp       parle MCP sur stdin/stdout à un client IA local, en lecture seule (option -config)
  auth      status | disable : état de l'authentification de l'interface,
            ou la couper en cas de mot de passe oublié (option -config)
  export    écrit l'annuaire dans un fichier YAML, par le serveur qui tourne
            (options -config -o)
  secrets   forget : clé de chiffrement perdue, repart d'une clé neuve et oublie
            les secrets qu'elle chiffrait (option -config)
  version   affiche la version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(usage, "{command}", brand.Command))
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "agent":
		err = runAgent(os.Args[2:])
	case "mcp":
		err = runMCP(os.Args[2:])
	case "auth":
		err = runAuth(os.Args[2:])
	case "export":
		err = runExport(os.Args[2:])
	case "secrets":
		err = runSecrets(os.Args[2:])
	case "version":
		err = runVersion()
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue : %s\n\n%s", os.Args[1], strings.ReplaceAll(usage, "{command}", brand.Command))
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, brand.Command+" :", err)
		var refused *refusal
		if errors.As(err, &refused) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
