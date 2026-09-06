package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	lockSuffix   = ".lock"
	lockFileMode = 0o600
)

// ErrUpdateInProgress : le verrou est déjà pris — une autre mise à jour est en
// cours, ou une précédente a été interrompue.
var ErrUpdateInProgress = errors.New("an update is already in progress")

// updateLock est un fichier posé à côté du binaire, pris avant le premier
// appel réseau et rendu une fois l'unité redémarrée. Le .new ne peut pas tenir
// ce rôle : il disparaît au rename, et un second self-update arrivé juste
// après remplaçait le .prev par le binaire déjà mis à jour.
type updateLock struct {
	root *os.Root
	name string
}

func lockUpdate(executablePath string) (*updateLock, error) {
	directory, name := filepath.Split(executablePath)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", directory, err)
	}

	// O_EXCL : c'est l'existence du fichier qui verrouille, son contenu ne dit rien.
	lockName := name + lockSuffix
	file, err := root.OpenFile(lockName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, lockFileMode)
	if err != nil {
		_ = root.Close()
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrUpdateInProgress, filepath.Join(directory, lockName))
		}
		return nil, fmt.Errorf("create %s: %w", lockName, err)
	}
	_ = file.Close()
	return &updateLock{root: root, name: lockName}, nil
}

// release retire le verrou. Une erreur ici ne change rien à la mise à jour,
// qui est finie : le prochain self-update dira que le fichier est resté.
func (l *updateLock) release() {
	_ = l.root.Remove(l.name)
	_ = l.root.Close()
}
