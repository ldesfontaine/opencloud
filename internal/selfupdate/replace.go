package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const (
	previousSuffix = ".prev"
	pendingSuffix  = ".new"
	executableMode = 0o755
)

var (
	// ErrUpdateInProgress : un .new existe déjà — une autre mise à jour est en
	// cours, ou une précédente a été interrompue.
	ErrUpdateInProgress = errors.New("an update is already in progress")
	// ErrReplacedButNotSynced : le nouveau binaire est en place, mais le fsync
	// du dossier a échoué — le rename pourrait se perdre à une coupure.
	ErrReplacedButNotSynced = errors.New("binary replaced but directory not synced")
)

// replaceExecutable met content à la place du binaire sans jamais écraser le
// fichier en cours d'exécution (« text file busy ») : écrit à côté, garde
// l'ancien joignable en .prev par un lien dur, puis un seul rename. À tout
// instant le chemin pointe sur un binaire entier, l'ancien ou le nouveau.
func replaceExecutable(executablePath string, content []byte) (previousPath string, err error) {
	directory, name := filepath.Split(executablePath)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", directory, err)
	}
	defer root.Close()

	pendingName := name + pendingSuffix
	previousName := name + previousSuffix

	// O_EXCL : le .new sert aussi de verrou contre deux mises à jour à la fois.
	pending, err := root.OpenFile(pendingName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, executableMode)
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%w: %s", ErrUpdateInProgress, filepath.Join(directory, pendingName))
	}
	if err != nil {
		return "", fmt.Errorf("create %s: %w", pendingName, err)
	}

	renamed := false
	defer func() {
		if renamed {
			return
		}
		// Nettoyage après échec : l'erreur d'origine prime sur celles-ci.
		_ = pending.Close()
		_ = root.Remove(pendingName)
	}()

	if err := writeExecutable(pending, content); err != nil {
		return "", err
	}
	// O_CREATE a subi le umask : on repose le mode voulu.
	if err := root.Chmod(pendingName, executableMode); err != nil {
		return "", fmt.Errorf("chmod %s: %w", pendingName, err)
	}

	// L'ancien binaire reste sous .prev : même inode, aucun octet copié.
	if err := root.Remove(previousName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("remove old %s: %w", previousName, err)
	}
	if err := root.Link(name, previousName); err != nil {
		return "", fmt.Errorf("keep previous binary as %s: %w", previousName, err)
	}

	if err := root.Rename(pendingName, name); err != nil {
		return "", fmt.Errorf("rename %s to %s: %w", pendingName, name, err)
	}
	renamed = true
	previousPath = filepath.Join(directory, previousName)

	// fsync du dossier, sinon le rename peut se perdre à la coupure. Le
	// remplacement est fait quoi qu'il arrive : l'appelant doit le savoir.
	if err := fsx.SyncDirectory(root, "."); err != nil {
		return previousPath, fmt.Errorf("%w: %w", ErrReplacedButNotSynced, err)
	}
	return previousPath, nil
}

func writeExecutable(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync new binary: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close new binary: %w", err)
	}
	return nil
}
