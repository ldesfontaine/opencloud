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
  agent     tourne sur une machine gérée (options -server -lang -pin -allow-plain -state ; jeton d'enrôlement sur l'entrée standard)
  guard     lanceur stable de l'agent : lance bin/current et revient à la version
            précédente si une mise à jour n'est pas confirmée (option -state)
  auth      status | disable : état de l'authentification de l'interface,
            ou la couper en cas de mot de passe oublié (option -config)
  export    écrit l'annuaire dans un fichier YAML, par le serveur qui tourne
            (options -config -o -propose)
  apply     applique un fichier YAML à l'annuaire, après avoir montré le plan et
            demandé confirmation (options -config -prune -yes -dry-run)
  secrets   forget : clé de chiffrement perdue, repart d'une clé neuve et oublie
            les secrets qu'elle chiffrait (option -config)
  release   verify <fichier> : vérifie qu'un fichier appartient à une version signée ;
            import <dossier> : copie une version vérifiée, pour mettre à jour les agents (option -config) ;
            sign, keygen : pour publier une version
  backup    restore : restaure une sauvegarde chiffrée de la base, hors ligne
            (options -key -state ; phrase de passe sur l'entrée standard)
  version   affiche la version
`

// exitStatus est le code de sortie qu'une commande demande, sans message : le
// code 3 de l'agent dit à systemd « mis à jour, relance-moi ».
type exitStatus struct {
	code int
}

func (e *exitStatus) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

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
	case "guard":
		err = runGuard(os.Args[2:])
	case "auth":
		err = runAuth(os.Args[2:])
	case "export":
		err = runExport(os.Args[2:])
	case "apply":
		err = runApply(os.Args[2:])
	case "secrets":
		err = runSecrets(os.Args[2:])
	case "release":
		err = runRelease(os.Args[2:])
	case "backup":
		err = runBackup(os.Args[2:])
	case "version":
		err = runVersion()
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue : %s\n\n%s", os.Args[1], strings.ReplaceAll(usage, "{command}", brand.Command))
		os.Exit(2)
	}
	if err != nil {
		// Un code de sortie voulu par la commande (l'agent mis à jour demande
		// à systemd de le relancer) n'est pas une erreur à afficher.
		var status *exitStatus
		if errors.As(err, &status) {
			os.Exit(status.code)
		}
		fmt.Fprintln(os.Stderr, brand.Command+" :", err)
		var refused *refusal
		if errors.As(err, &refused) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
