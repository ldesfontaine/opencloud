package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"syscall"
)

// L'état de l'agent porte ses droits et la clé du serveur qu'il a épinglée :
// qui peut le remplacer décide de ce que l'agent, root, accepte d'exécuter.
// L'agent juge donc au démarrage le dossier d'état et chaque dossier
// au-dessus, sur le chemin donné comme sur le chemin résolu ; s'il en trouve
// un qui n'est pas à root ou à lui, ou que d'autres comptes peuvent
// modifier, il n'applique plus que observe (Options.UnsafeState), sans
// s'arrêter : un arrêt aurait aveuglé la supervision.

// UnsafeStateError dit quel dossier de la chaîne n'est pas sûr, qui le
// possède et avec quel mode.
type UnsafeStateError struct {
	Path string
	UID  uint32
	Mode fs.FileMode
}

func (e *UnsafeStateError) Error() string {
	return fmt.Sprintf("state dir is not safe: %s is owned by uid %d with mode %s", e.Path, e.UID, e.Mode)
}

// CheckStateDir rend nil quand l'état est bien gardé, sinon une erreur qui
// dit pourquoi (un *UnsafeStateError le plus souvent). Une erreur de lecture
// vaut un refus : ce qu'on ne peut pas juger n'est pas sûr.
func CheckStateDir(path string) error {
	euid := os.Geteuid()
	if euid < 0 || euid > math.MaxUint32 {
		return fmt.Errorf("state dir is not safe: effective uid %d is out of range", euid)
	}
	given, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("state dir is not safe: %w", err)
	}
	// #nosec G115 -- borné juste au-dessus.
	if err := checkChain(given, lstatOwner, uint32(euid)); err != nil {
		return err
	}
	// Le chemin donné peut passer par des liens : c'est le dossier réel, et
	// ceux au-dessus de lui, que d'autres comptes pourraient modifier.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("state dir is not safe: resolve %s: %w", path, err)
	}
	if resolved, err = filepath.Abs(resolved); err != nil {
		return fmt.Errorf("state dir is not safe: %w", err)
	}
	if resolved == given {
		return nil
	}
	// #nosec G115 -- borné plus haut.
	return checkChain(resolved, lstatOwner, uint32(euid))
}

// lstatOwner rend le propriétaire et le mode d'un chemin sans suivre un lien
// final. Linux seulement : un système de fichiers qui ne rend pas de
// propriétaire est une erreur, jamais un « sûr par défaut ».
func lstatOwner(path string) (uint32, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	raw, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("no owner reported for " + path)
	}
	return raw.Uid, info.Mode(), nil
}

// checkChain juge un chemin absolu et nettoyé, du dossier d'état jusqu'à « / ».
// Chacun doit appartenir à root ou à euid, et n'être inscriptible ni par le
// groupe ni par les autres. Seule exception, un dossier au-dessus de l'état
// à bit collant (/tmp) : personne n'y renomme le dossier d'un autre, et le
// suivant dans la chaîne est jugé à son tour. Le dossier d'état lui-même n'a
// pas cette exception : on y poserait des fichiers. Un lien symbolique sur
// la chaîne donnée est jugé par son propriétaire seulement (son mode vaut
// toujours 0777) ; sa cible est jugée par la chaîne résolue.
func checkChain(path string, stat func(string) (uid uint32, mode fs.FileMode, err error), euid uint32) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("state dir is not safe: %s is not an absolute clean path", path)
	}
	for current, isState := path, true; ; isState = false {
		uid, mode, err := stat(current)
		if err != nil {
			return fmt.Errorf("state dir is not safe: stat %s: %w", current, err)
		}
		ownedByUs := uid == 0 || uid == euid
		isLink := mode&fs.ModeSymlink != 0
		othersWrite := mode.Perm()&0o022 != 0 && !isLink
		if othersWrite && !isState && mode&fs.ModeSticky != 0 {
			othersWrite = false
		}
		if !ownedByUs || othersWrite {
			return &UnsafeStateError{Path: current, UID: uid, Mode: mode}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
