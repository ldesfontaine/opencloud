package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// L'état de l'agent porte ses droits et la clé du serveur qu'il a épinglée :
// qui peut le remplacer décide de ce que l'agent, root, accepte d'exécuter.
// L'agent juge donc au démarrage chaque dossier que le chemin de son état
// traverse, liens compris ; s'il en trouve un qui n'est pas à root ou à lui,
// ou que d'autres comptes peuvent modifier, il n'applique plus que observe
// (Options.UnsafeState), sans s'arrêter : un arrêt aurait aveuglé la
// supervision.

// maxStateLinks borne les liens suivis, comme le noyau (ELOOP) : une boucle
// n'est pas un état.
const maxStateLinks = 40

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

// CheckStateDir rend nil quand l'état ouvert (opened, sur path) est bien
// gardé, sinon une erreur qui dit pourquoi (un *UnsafeStateError le plus
// souvent). Une erreur de lecture vaut un refus : ce qu'on ne peut pas juger
// n'est pas sûr. Le chemin est jugé après l'ouverture : le dossier ouvert
// doit être celui que le chemin désigne au moment du jugement, sans quoi un
// lien ou un dossier a changé entre les deux.
func CheckStateDir(opened *os.Root, path string) error {
	euid := os.Geteuid()
	if euid < 0 || euid > math.MaxUint32 {
		return fmt.Errorf("state dir is not safe: effective uid %d is out of range", euid)
	}
	// Pas de filepath.Abs : il nettoie « lien/.. » à la lettre, le noyau
	// remonte depuis la cible. Le chemin se suit comme le noyau le suit.
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("state dir is not safe: %w", err)
		}
		path = wd + string(filepath.Separator) + path
	}
	// #nosec G115 -- borné juste au-dessus.
	owner := uint32(euid)
	resolved, err := checkChain(path, lstatOwner, os.Readlink, owner)
	if err != nil {
		return err
	}
	openedInfo, err := opened.Stat(".")
	if err != nil {
		return fmt.Errorf("state dir is not safe: stat opened %s: %w", path, err)
	}
	// Plus personne d'autre ne peut changer ce que resolved désigne : chaque
	// dossier au-dessus vient d'être jugé.
	judgedInfo, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("state dir is not safe: stat %s: %w", resolved, err)
	}
	if !os.SameFile(openedInfo, judgedInfo) {
		return fmt.Errorf("state dir is not safe: %s changed between its opening and its check", path)
	}
	return checkStateFile(opened, filepath.Join(resolved, identityFile), identityFile, owner)
}

// checkStateFile juge le fichier qui porte les droits : à root ou à l'agent,
// non inscriptible par d'autres. Un état déplacé à la main garde les fichiers
// de qui l'avait remplacé ; un dossier à root ne suffit pas. Absent : rien à
// juger (premier lancement).
func checkStateFile(opened *os.Root, shown, name string, euid uint32) error {
	info, err := opened.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state dir is not safe: stat %s: %w", shown, err)
	}
	uid, err := ownerOf(info, shown)
	if err != nil {
		return fmt.Errorf("state dir is not safe: %w", err)
	}
	if !info.Mode().IsRegular() || !ownedBy(uid, euid) || info.Mode().Perm()&0o022 != 0 {
		return &UnsafeStateError{Path: shown, UID: uid, Mode: info.Mode()}
	}
	return nil
}

// lstatOwner rend le propriétaire et le mode d'un chemin sans suivre un lien
// final.
func lstatOwner(path string) (uint32, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	uid, err := ownerOf(info, path)
	return uid, info.Mode(), err
}

// ownerOf : Linux seulement ; un système de fichiers qui ne rend pas de
// propriétaire est une erreur, jamais un « sûr par défaut ».
func ownerOf(info fs.FileInfo, path string) (uint32, error) {
	raw, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no owner reported for " + path)
	}
	return raw.Uid, nil
}

func ownedBy(uid, euid uint32) bool {
	return uid == 0 || uid == euid
}

// checkChain suit un chemin absolu comme le noyau, un composant après
// l'autre, depuis « / » : chaque dossier traversé, ceux où mène un lien
// compris, doit appartenir à root ou à euid et n'être inscriptible ni par le
// groupe ni par les autres. Seule exception, un dossier traversé à bit
// collant (/tmp) : personne n'y renomme le dossier d'un autre, et le suivant
// est jugé à son tour. Un lien n'est jugé que par son propriétaire (son mode
// vaut toujours 0777, et sous un bit collant son propriétaire peut le
// remplacer) ; sa cible est suivie, « .. » remonte depuis elle. Le dossier
// d'arrivée, l'état, n'a pas l'exception du bit collant : on y poserait des
// fichiers. Rend le chemin réel de l'état, sans lien.
//
// Juger seulement le chemin donné puis le chemin résolu laisserait passer un
// dossier qu'on ne traverse qu'entre deux liens : son propriétaire choisirait
// où mène le second.
func checkChain(path string, stat func(string) (uid uint32, mode fs.FileMode, err error), readlink func(string) (string, error), euid uint32) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("state dir is not safe: %s is not an absolute path", path)
	}
	judge := func(current string, isState bool) (fs.FileMode, error) {
		uid, mode, err := stat(current)
		if err != nil {
			return 0, fmt.Errorf("state dir is not safe: stat %s: %w", current, err)
		}
		if mode&fs.ModeSymlink != 0 {
			if !ownedBy(uid, euid) {
				return 0, &UnsafeStateError{Path: current, UID: uid, Mode: mode}
			}
			return mode, nil
		}
		othersWrite := mode.Perm()&0o022 != 0 && (isState || mode&fs.ModeSticky == 0)
		if !ownedBy(uid, euid) || othersWrite {
			return 0, &UnsafeStateError{Path: current, UID: uid, Mode: mode}
		}
		return mode, nil
	}
	current := string(filepath.Separator)
	if _, err := judge(current, false); err != nil {
		return "", err
	}
	rest, links := components(path), 0
	for len(rest) > 0 {
		name := rest[0]
		rest = rest[1:]
		if name == ".." {
			current = filepath.Dir(current)
			continue
		}
		next := filepath.Join(current, name)
		mode, err := judge(next, false)
		if err != nil {
			return "", err
		}
		if mode&fs.ModeSymlink == 0 {
			current = next
			continue
		}
		if links++; links > maxStateLinks {
			return "", fmt.Errorf("state dir is not safe: too many links from %s", path)
		}
		target, err := readlink(next)
		if err != nil {
			return "", fmt.Errorf("state dir is not safe: read link %s: %w", next, err)
		}
		if filepath.IsAbs(target) {
			current = string(filepath.Separator)
		}
		rest = append(components(target), rest...)
	}
	// L'arrivée, déjà jugée comme dossier traversé, l'est de nouveau sans
	// l'exception du bit collant.
	if _, err := judge(current, true); err != nil {
		return "", err
	}
	return current, nil
}

// components coupe un chemin en ses noms, sans les vides ni « . » ; les
// « .. » restent, checkChain les suit.
func components(path string) []string {
	var names []string
	for _, name := range strings.Split(path, string(filepath.Separator)) {
		if name != "" && name != "." {
			names = append(names, name)
		}
	}
	return names
}
