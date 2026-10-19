package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Un temporaire s'appelle « <nom>.tmp-<16 hexadécimaux> » : assez long pour
// ne pas se deviner, assez précis pour que le ménage ne touche qu'aux siens.
const (
	tempInfix       = ".tmp-"
	tempSuffixBytes = 8
)

// WriteAtomic remplace name sous root d'un seul coup : un lecteur voit
// l'ancien contenu ou le nouveau, jamais un fichier à moitié écrit.
//
// Le temporaire porte un nom aléatoire et s'ouvre en O_EXCL : un fichier
// déjà posé à cet endroit par un autre compte (son propriétaire, ses
// descripteurs ouverts) n'est jamais réutilisé, même quand l'appelant est root.
func WriteAtomic(root *os.Root, name string, data []byte, perm os.FileMode) error {
	temp := uniqueTempName(name)
	if err := writeTemp(root, temp, data, perm, os.O_EXCL); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		discard(root, nil, temp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	removeLeftoverTemps(root, name)
	// fsync du dossier, sinon le rename peut se perdre à la coupure.
	return syncDir(root, path.Dir(name))
}

// removeLeftoverTemps supprime les temporaires qu'une coupure a laissés à
// côté de name. Chacun garde une copie de ce qui s'écrivait (une clé, une
// identité, un export), et un nom aléatoire n'est jamais repris : sans ce
// ménage, une clé remplacée par « secrets forget » survivrait dans l'un
// d'eux. Un écrivain concurrent du même fichier peut y perdre son
// temporaire : son rename échoue, le fichier reste entier. Le ménage ne
// fait jamais échouer l'écriture, déjà faite.
func removeLeftoverTemps(root *os.Root, name string) {
	dir, prefix := path.Dir(name), path.Base(name)+tempInfix
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		suffix, found := strings.CutPrefix(entry.Name(), prefix)
		if !found || !isTempSuffix(suffix) || !entry.Type().IsRegular() {
			continue
		}
		_ = root.Remove(path.Join(dir, entry.Name()))
	}
}

func isTempSuffix(suffix string) bool {
	decoded, err := hex.DecodeString(suffix)
	return err == nil && len(decoded) == tempSuffixBytes
}

// CreateExclusive crée name sous root avec data, ou échoue en rendant une
// erreur qui vérifie errors.Is(err, fs.ErrExist) s'il existe déjà : deux
// processus qui créent ensemble le même fichier n'écrasent jamais l'autre,
// un seul gagne. Le contenu est écrit en entier avant que le nom n'apparaisse
// (lien dur d'un temporaire à nom unique), donc un lecteur ne voit jamais un
// fichier vide ou partiel.
func CreateExclusive(root *os.Root, name string, data []byte, perm os.FileMode) error {
	temp := uniqueTempName(name)
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

// uniqueTempName rend un nom de temporaire imprévisible, dans le même dossier
// que name : le rename final doit rester sur le même système de fichiers.
func uniqueTempName(name string) string {
	suffix := make([]byte, tempSuffixBytes)
	_, _ = rand.Read(suffix)
	return name + tempInfix + hex.EncodeToString(suffix)
}

// writeTemp écrit data dans temp, en mode perm exactement : le umask de
// l'appelant ne doit pas le modifier.
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
