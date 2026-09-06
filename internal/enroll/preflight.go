package enroll

import (
	"errors"
	"fmt"
	"os"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// preflight vérifie tout avant le premier effet. Chaque manque est un refus
// nommé : la cause, et le geste qui la lève (15-catalogue-actions.md §1).
func (e *enrollment) preflight() error {
	if e.deps.UID != 0 {
		return refusal.Refusal{
			Cause:  "l'amorçage écrit dans /etc et /usr/local/sbin, et il ne tourne pas en root",
			Remedy: "rejouer la commande avec sudo : sudo opencloud enroll-local",
		}
	}
	if _, err := os.Stat(e.systemPath(systemdMarkerPath)); err != nil {
		return refusal.Refusal{
			Cause:  "systemd ne tourne pas sur cette machine : /run/systemd/system est absent",
			Remedy: "openCloud lance les actions par systemd-run — installer openCloud sur une machine gérée par systemd",
		}
	}

	binaries := []struct {
		path   string
		reason string
	}{
		{sudoPath, "sudo élève le compte opencloud vers le lanceur"},
		{visudoPath, "visudo valide la règle sudo avant de la poser"},
		{sshdPath, "sshd sert les actions, y compris vers localhost"},
		{usermodPath, "usermod donne son shell au compte de service"},
		{passwdCommandPath, "passwd verrouille le mot de passe du compte de service"},
		{systemctlPath, "systemctl recharge sshd après le drop-in"},
	}
	for _, binary := range binaries {
		if _, err := os.Stat(e.systemPath(binary.path)); err != nil {
			return refusal.Refusal{
				Cause:  fmt.Sprintf("/%s est absent : %s", binary.path, binary.reason),
				Remedy: fmt.Sprintf("installer le paquet qui fournit /%s, puis rejouer l'amorçage", binary.path),
			}
		}
	}

	if info, err := os.Stat(e.systemPath(sshdDropInDir)); err != nil || !info.IsDir() {
		return refusal.Refusal{
			Cause:  "/etc/ssh/sshd_config.d est absent : cette version d'OpenSSH ne lit pas de drop-in",
			Remedy: "utiliser une Debian ou une Ubuntu dont sshd_config porte « Include /etc/ssh/sshd_config.d/*.conf »",
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
