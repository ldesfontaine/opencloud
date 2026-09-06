package main

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Le lanceur ne connaît qu'un code : il refuse, ou il n'est plus là. Ce qui
// échoue ensuite est l'affaire de l'unité et de son journal.
const exitRefused = 2

func main() {
	uid, err := accountUID(ownerAccount)
	if err != nil {
		refuse(err)
	}

	// L'environnement n'est jamais lu : rien d'hérité ne change ce qui suit.
	launch := launcher{actionsRoot: actiondir.Root, ownerUID: uid}
	toRun, err := launch.prepare(os.Args[1:])
	if err != nil {
		refuse(err)
	}

	// execve : pas de processus intermédiaire, pas de shell, un environnement
	// fixe. Si l'appel revient, c'est qu'il a échoué.
	// #nosec G204 -- le vecteur est une constante de launchVector : seuls
	// l'identifiant validé et le délai borné y entrent, et il n'y a pas de
	// shell pour les relire.
	if err := syscall.Exec(toRun.Path, toRun.Args, toRun.Env); err != nil {
		refuse(refusal.Refusal{
			Cause:  fmt.Sprintf("%s n'a pas pu être exécuté : %v", toRun.Path, err),
			Remedy: "vérifier que systemd-run est installé à ce chemin",
		})
	}
}

func refuse(reason error) {
	fmt.Fprintln(os.Stderr, reason)
	os.Exit(exitRefused)
}

func accountUID(account string) (uint32, error) {
	found, err := user.Lookup(account)
	if err != nil {
		return 0, refusal.Refusal{
			Cause:  fmt.Sprintf("le compte %s n'existe pas sur cette machine", account),
			Remedy: "enrôler la machine : c'est l'enrôlement qui crée ce compte",
		}
	}
	// ParseUint borne déjà à 32 bits : un uid plus grand n'existe pas.
	number, err := strconv.ParseUint(found.Uid, 10, 32)
	if err != nil {
		return 0, refusal.Refusal{
			Cause:  fmt.Sprintf("l'uid du compte %s n'est pas un nombre", account),
			Remedy: "corriger /etc/passwd sur cette machine",
		}
	}
	return uint32(number), nil
}
