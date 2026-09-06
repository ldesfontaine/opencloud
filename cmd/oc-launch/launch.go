package main

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// Le compte qui dépose les actions : le lanceur n'exécute que ce qui lui
	// appartient, et sudoers n'autorise que le lanceur.
	ownerAccount = "opencloud"

	// Aucune écriture pour le groupe ni pour les autres : sinon un tiers
	// changerait le script entre la vérification et l'exécution.
	writeForGroupOrOthers = 0o022

	// L'environnement du script est remplacé, jamais hérité.
	fixedPath   = "PATH=/usr/sbin:/usr/bin:/sbin:/bin"
	fixedLocale = "LC_ALL=C"
)

// vector est l'appel que le lanceur remplace par execve. Seuls l'identifiant
// de l'action et son délai y varient : aucune option ne vient d'ailleurs.
type vector struct {
	Path string
	Args []string
	Env  []string
}

// launcher tient les deux choses qui changent entre une machine et un test :
// la racine des actions et l'uid attendu.
type launcher struct {
	actionsRoot string
	ownerUID    uint32
}

// prepare relit tout ce que sudoers ne sait pas valider, puis rend l'appel
// exact. Une seule condition manquante et rien n'est lancé.
func (l launcher) prepare(arguments []string) (vector, error) {
	if len(arguments) != 1 {
		return vector{}, refusal.Refusal{
			Cause:  fmt.Sprintf("le lanceur prend un seul argument, l'identifiant de l'action ; il en a reçu %d", len(arguments)),
			Remedy: "appeler « oc-launch <id> », sans option, sans chemin, sans rien d'autre",
		}
	}
	id := arguments[0]
	if !actiondir.ValidID(id) {
		return vector{}, refusal.Refusal{
			Cause:  "cet identifiant d'action n'a pas la forme attendue",
			Remedy: "n'employer que des minuscules, des chiffres et des tirets, au plus 40 caractères",
		}
	}

	directory := path.Join(l.actionsRoot, id)
	if err := l.checkDirectory(directory); err != nil {
		return vector{}, err
	}
	if err := l.checkFile(path.Join(directory, actiondir.ScriptName), true); err != nil {
		return vector{}, err
	}
	if err := l.checkFile(path.Join(directory, actiondir.ParamsName), false); err != nil {
		return vector{}, err
	}
	seconds, err := l.readTimeout(path.Join(directory, actiondir.TimeoutName))
	if err != nil {
		return vector{}, err
	}
	return launchVector(directory, id, seconds), nil
}

func (l launcher) checkDirectory(directory string) error {
	file, err := openWithoutFollowing(directory, syscall.O_DIRECTORY)
	if err != nil {
		return refusal.Refusal{
			Cause:  "le dossier de cette action n'est pas un dossier ouvrable sur cette machine",
			Remedy: "déposer l'action avant de la lancer ; un lien symbolique n'est jamais suivi",
		}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return refusal.Refusal{
			Cause:  "le dossier de cette action n'est pas un dossier",
			Remedy: "retirer ce qui porte ce nom, puis redéposer l'action",
		}
	}
	if !l.isOwner(info) {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("le dossier de cette action n'appartient pas au compte %s", ownerAccount),
			Remedy: "redéposer l'action depuis openCloud, qui écrit sous ce compte",
		}
	}
	if info.Mode().Perm()&writeForGroupOrOthers != 0 {
		return refusal.Refusal{
			Cause:  "le dossier de cette action est modifiable par le groupe ou par les autres",
			Remedy: "poser le mode 0700 sur ce dossier, puis relancer",
		}
	}
	return nil
}

func (l launcher) checkFile(name string, mustBeExecutable bool) error {
	file, err := openWithoutFollowing(name, 0)
	if err != nil {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » n'est pas lisible dans le dossier de cette action", path.Base(name)),
			Remedy: "redéposer l'action ; un lien symbolique n'est jamais suivi",
		}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » n'est pas un fichier régulier", path.Base(name)),
			Remedy: "redéposer l'action : le lanceur n'exécute que des fichiers ordinaires",
		}
	}
	if !l.isOwner(info) {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » n'appartient pas au compte %s", path.Base(name), ownerAccount),
			Remedy: "redéposer l'action depuis openCloud, qui écrit sous ce compte",
		}
	}
	if info.Mode().Perm()&writeForGroupOrOthers != 0 {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » est modifiable par le groupe ou par les autres", path.Base(name)),
			Remedy: "retirer le droit d'écriture au groupe et aux autres, puis relancer",
		}
	}
	if mustBeExecutable && info.Mode().Perm()&0o100 == 0 {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » n'est pas exécutable par son propriétaire", path.Base(name)),
			Remedy: "poser le mode 0700 sur le script, puis relancer",
		}
	}
	return nil
}

func (l launcher) readTimeout(name string) (int, error) {
	file, err := openWithoutFollowing(name, 0)
	if err != nil {
		return 0, refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » n'est pas lisible dans le dossier de cette action", path.Base(name)),
			Remedy: "redéposer l'action : son délai maximum en fait partie",
		}
	}
	defer file.Close()

	// Le fichier tient quelques chiffres : ce qui dépasse est déjà hors bornes.
	content := make([]byte, 16)
	read, err := file.Read(content)
	if err != nil && read == 0 {
		return 0, refusal.Refusal{
			Cause:  "le délai maximum de cette action est illisible",
			Remedy: "redéposer l'action",
		}
	}
	seconds, ok := actiondir.ParseTimeout(string(content[:read]))
	if !ok {
		return 0, refusal.Refusal{
			Cause: fmt.Sprintf("le délai maximum de cette action n'est pas un nombre de secondes entre %d et %d",
				actiondir.MinTimeoutSeconds, actiondir.MaxTimeoutSeconds),
			Remedy: "redéposer l'action depuis openCloud, qui écrit ce délai",
		}
	}
	return seconds, nil
}

func (l launcher) isOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == l.ownerUID
}

// openWithoutFollowing refuse le lien symbolique en dernier composant, et
// n'attend jamais : un tube nommé bloquerait le lanceur à l'ouverture.
func openWithoutFollowing(name string, extraFlags int) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | extraFlags
	// #nosec G703 G304 -- le chemin est dérivé de actiondir.Root et d'un
	// identifiant que actiondir.ValidID vient d'accepter : il ne tient ni
	// barre oblique, ni point, ni rien qui remonte d'un dossier.
	return os.OpenFile(name, flags, 0)
}

func launchVector(directory, id string, timeoutSeconds int) vector {
	return vector{
		Path: actiondir.SystemdRunPath,
		Args: []string{
			"systemd-run",
			"--unit=" + actiondir.UnitName(id),
			"--description=openCloud action " + id,
			"--property=EnvironmentFile=" + path.Join(directory, actiondir.ParamsName),
			"--property=RuntimeMaxSec=" + strconv.Itoa(timeoutSeconds),
			"--property=WorkingDirectory=" + directory,
			// --collect : l'unité est ramassée à la fin, journald garde tout.
			"--collect",
			"--quiet",
			path.Join(directory, actiondir.ScriptName),
		},
		Env: []string{fixedPath, fixedLocale},
	}
}
