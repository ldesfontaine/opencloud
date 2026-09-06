package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// MigrationLockFileName est le nom du verrou de migration dans le répertoire
// d'état.
const MigrationLockFileName = "migration.lock"

const (
	migrationLockFileMode = 0o600
	unknownLockPid        = "inconnu"
	unknownLockDate       = "date inconnue"
)

// migrationLock est le fichier posé dans le répertoire d'état pendant la
// sauvegarde et les migrations. Chaque migration est déjà une transaction :
// ce que le verrou ajoute, c'est de dire à une seconde instance — ou au
// démarrage qui suit une coupure — qu'une migration était en cours.
type migrationLock struct {
	root *os.Root
}

// migrationLockHolder : qui a posé le verrou, tel que le fichier le dit.
type migrationLockHolder struct {
	pid  string
	date string
}

func acquireMigrationLock(root *os.Root) (*migrationLock, error) {
	// O_EXCL : c'est la création du fichier qui verrouille ; son contenu ne
	// sert qu'à nommer qui l'a posé quand il reste.
	file, err := root.OpenFile(MigrationLockFileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, migrationLockFileMode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, migrationLockRefusal(root)
		}
		return nil, fmt.Errorf("create migration lock: %w", err)
	}

	holder := migrationLockHolder{
		pid:  strconv.Itoa(os.Getpid()),
		date: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeAndSyncLock(root, file, holder); err != nil {
		// Le verrou n'a rien gardé : rien n'a encore été tenté sur la base.
		_ = root.Remove(MigrationLockFileName)
		return nil, err
	}
	return &migrationLock{root: root}, nil
}

func writeAndSyncLock(root *os.Root, file *os.File, holder migrationLockHolder) error {
	defer file.Close()

	if _, err := file.WriteString(holder.pid + "\n" + holder.date + "\n"); err != nil {
		return fmt.Errorf("write migration lock: %w", err)
	}
	// Un verrou qui ne survit pas à la coupure ne verrouille rien : c'est
	// justement la coupure qu'il doit raconter au démarrage suivant.
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync migration lock: %w", err)
	}
	return fsx.SyncDirectory(root, ".")
}

// release retire le verrou. Il n'est appelé qu'après la dernière migration
// appliquée : une sauvegarde ou une migration qui échoue laisse le verrou en
// place, et c'est ce qu'on veut dire au démarrage suivant.
func (l *migrationLock) release() error {
	if err := l.root.Remove(MigrationLockFileName); err != nil {
		return fmt.Errorf("remove migration lock: %w", err)
	}
	return fsx.SyncDirectory(l.root, ".")
}

// CheckMigrationLock rend un refus nommé quand le verrou de migration est
// présent dans le répertoire d'état. `opencloud status` le lit sans ouvrir la
// base, pour dire la même chose que le démarrage.
func CheckMigrationLock(stateDir string) error {
	root, err := os.OpenRoot(stateDir) // bounded: config.validateStateDir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open state directory: %w", err)
	}
	defer root.Close()
	return refuseIfMigrationLocked(root)
}

func refuseIfMigrationLocked(root *os.Root) error {
	locked, err := fileExists(root, MigrationLockFileName)
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	return migrationLockRefusal(root)
}

func migrationLockRefusal(root *os.Root) refusal.Refusal {
	holder := readMigrationLock(root)
	// Le chemin sort de l'os.Root parce que l'opérateur doit pouvoir le taper.
	lockPath := filepath.Join(root.Name(), MigrationLockFileName)

	return refusal.Refusal{
		Cause: fmt.Sprintf("une migration est en cours ou a été interrompue, posée le %s par le processus %s",
			holder.date, holder.pid),
		Remedy: fmt.Sprintf("vérifier qu'aucun autre opencloud ne tourne (`systemctl status opencloud`, `pgrep opencloud`), puis retirer %s ; la base n'a pas été touchée par cette migration : chaque migration est une transaction",
			lockPath),
	}
}

// readMigrationLock ne rend jamais d'erreur : un verrou illisible reste un
// verrou, et le refus doit se lire même quand son contenu est perdu.
func readMigrationLock(root *os.Root) migrationLockHolder {
	holder := migrationLockHolder{pid: unknownLockPid, date: unknownLockDate}

	content, err := root.ReadFile(MigrationLockFileName)
	if err != nil {
		return holder
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) > 0 && lines[0] != "" {
		holder.pid = lines[0]
	}
	if len(lines) > 1 && lines[1] != "" {
		holder.date = lines[1]
	}
	return holder
}
