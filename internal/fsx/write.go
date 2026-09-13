package fsx

import (
	"fmt"
	"os"
	"path"
)

// WriteAtomic remplace name sous root d'un seul coup : un lecteur voit
// l'ancien contenu ou le nouveau, jamais un fichier à moitié écrit.
func WriteAtomic(root *os.Root, name string, data []byte, perm os.FileMode) error {
	temp := name + ".tmp"
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if err := writeAndSync(file, data); err != nil {
		discard(root, file, temp)
		return err
	}
	if err := file.Close(); err != nil {
		discard(root, nil, temp)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := root.Rename(temp, name); err != nil {
		discard(root, nil, temp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	// fsync du dossier, sinon le rename peut se perdre à la coupure.
	return syncDir(root, path.Dir(name))
}

// Nettoyage après échec : l'erreur d'origine compte, pas celle du ménage.
func discard(root *os.Root, file *os.File, temp string) {
	if file != nil {
		_ = file.Close()
	}
	_ = root.Remove(temp)
}

func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	return nil
}

func syncDir(root *os.Root, dir string) error {
	handle, err := root.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir: %w", err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync dir: %w", err)
	}
	return nil
}
