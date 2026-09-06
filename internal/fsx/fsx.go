// Package fsx écrit les fichiers de façon atomique : temporaire, fsync, rename,
// fsync du dossier. Le seul endroit qui le fait. Tout chemin est relatif à un
// os.Root, jamais absolu : le répertoire d'état est ouvert une fois et tout
// passe par lui.
package fsx

import (
	"errors"
	"fmt"
	"os"
	"path"
)

// Suffixe du fichier temporaire, dans le même dossier que la cible : un rename
// n'est atomique qu'au sein d'un même système de fichiers.
const temporarySuffix = ".tmp"

// WriteFile remplace name par content, ou le crée. Une coupure à n'importe quel
// instant laisse soit l'ancien fichier entier, soit le nouveau — jamais un
// fichier tronqué.
func WriteFile(root *os.Root, name string, content []byte, mode os.FileMode) error {
	temporaryName := name + temporarySuffix

	// Un temporaire laissé par une coupure précédente bloquerait O_EXCL.
	if err := root.Remove(temporaryName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale temporary file: %w", err)
	}

	// O_EXCL : os.Root ne suit alors jamais un lien symbolique, même dans le root.
	temporary, err := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	renamed := false
	defer func() {
		if renamed {
			return
		}
		// Nettoyage après échec : l'erreur d'origine prime sur celles-ci.
		_ = temporary.Close()
		_ = root.Remove(temporaryName)
	}()

	if err := writeAndSync(temporary, content); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := root.Rename(temporaryName, name); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	renamed = true

	// fsync du dossier, sinon le rename peut se perdre à la coupure.
	return SyncDirectory(root, path.Dir(name))
}

func writeAndSync(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	return nil
}

// SyncFile force l'écriture sur disque d'un fichier déjà écrit par un tiers,
// par exemple une sauvegarde produite par SQLite.
func SyncFile(root *os.Root, name string) error {
	file, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open file to sync: %w", err)
	}
	defer file.Close()

	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	return nil
}

// SyncDirectory force l'écriture sur disque des entrées d'un dossier : c'est ce
// qui rend un rename durable. "." désigne le root lui-même.
func SyncDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("open directory to sync: %w", err)
	}
	defer directory.Close()

	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
