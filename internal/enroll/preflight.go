package enroll

import (
	"errors"
	"os"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// preflight vérifie ce dont l'amorçage a besoin pour jouer le script : ce que
// la séquence elle-même exige — systemd, sudo, visudo, sshd, sshd_config.d —
// est vérifié par le script, une fois, pour les deux chemins. Chaque manque est
// un refus nommé : la cause, et le geste qui la lève.
func (e *enrollment) preflight() error {
	if e.deps.UID != 0 {
		return refusal.Refusal{
			Cause:  "l'amorçage écrit dans /etc et /usr/local/sbin, et il ne tourne pas en root",
			Remedy: "rejouer la commande avec sudo : sudo opencloud enroll-local",
		}
	}
	if _, err := os.Stat(e.systemPath(bashPath)); err != nil {
		return refusal.Refusal{
			Cause:  bashPath + " est absent : c'est lui qui joue le script de l'enrôlement",
			Remedy: "installer le paquet bash, puis rejouer l'amorçage",
		}
	}
	if _, err := os.Stat(e.systemPath(packagedLauncher)); err != nil {
		return refusal.Refusal{
			Cause:  "/" + packagedLauncher + " est absent : le lanceur vient du paquet openCloud",
			Remedy: "réinstaller le paquet openCloud, puis rejouer l'amorçage",
		}
	}

	found, err := lookupAccount(e.systemPath(passwdPath), AccountName)
	if errors.Is(err, errAccountNotFound) {
		return refusal.Refusal{
			Cause:  "le compte système « opencloud » n'existe pas sur cette machine",
			Remedy: "il est créé par le paquet openCloud : réinstaller le paquet, puis rejouer l'amorçage",
		}
	}
	if err != nil {
		return err
	}
	if found.Home == "" {
		return refusal.Refusal{
			Cause:  "le compte « opencloud » n'a pas de répertoire personnel : sa clé n'aurait nulle part où vivre",
			Remedy: "donner /var/lib/opencloud pour home au compte, puis rejouer l'amorçage",
		}
	}
	e.account = found
	return nil
}
