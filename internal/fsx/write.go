package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
)

// WriteAtomic remplace name sous root d'un seul coup : un lecteur voit
// l'ancien contenu ou le nouveau, jamais un fichier à moitié écrit.
func WriteAtomic(root *os.Root, name string, data []byte, perm os.FileMode) error {
	temp := name + ".tmp"
	if err := writeTemp(root, temp, data, perm, os.O_TRUNC); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		discard(root, nil, temp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	// fsync du dossier, sinon le rename peut se perdre à la coupure.
	return syncDir(root, path.Dir(name))
}

// CreateExclusive crée name sous root avec data, ou échoue en rendant une
// erreur qui vérifie errors.Is(err, fs.ErrExist) s'il existe déjà : deux
// processus qui créent ensemble le même fichier n'écrasent jamais l'autre,
// un seul gagne. Le contenu est écrit en entier avant que le nom n'apparaisse
// (lien dur d'un temporaire à nom unique), donc un lecteur ne voit jamais un
// fichier vide ou partiel.
func CreateExclusive(root *os.Root, name string, data []byte, perm os.FileMode) error {
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	temp := name + ".tmp-" + hex.EncodeToString(suffix)
	if err := writeTemp(root, temp, data, perm, os.O_EXCL); err != nil {
		return err
	}
	// Link échoue si name existe : c'est là que se joue l'exclusivité.
	linkErr := root.Link(temp, name)
	_ = root.Remove(temp)
	if linkErr != nil {
		return fmt.Errorf("create %s: %w", name, linkErr)
	}
	return syncDir(root, path.Dir(name))
}

// writeTemp écrit data dans temp, en mode perm exactement : un temporaire
// resté d'un échec avec un mode plus large ne le garderait pas.
func writeTemp(root *os.Root, temp string, data []byte, perm os.FileMode, extraFlags int) error {
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|extraFlags, perm)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if err := file.Chmod(perm); err != nil {
		discard(root, file, temp)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := writeAndSync(file, data); err != nil {
		discard(root, file, temp)
		return err
	}
	if err := file.Close(); err != nil {
		discard(root, nil, temp)
		return fmt.Errorf("close temp file: %w", err)
	}
	return nil
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
