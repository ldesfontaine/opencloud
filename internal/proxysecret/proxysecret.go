package proxysecret

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// 32 caractères d'un secret tiré au hasard (openssl rand -hex 32 en
	// donne 64) : assez pour qu'on ne le devine pas.
	MinLength = 32
	// Une ligne de secret tient très en dessous : au-delà, ce n'est pas lui.
	maxFileBytes = 1024
	// Les bits que 0640 laisse fermés : tout accès pour les autres comptes.
	// Le groupe peut lire, pour que le compte du service lise un fichier
	// que root a posé.
	forbiddenBits = 0o007
)

var (
	ErrFileMode = errors.New("proxysecret: file is readable by other accounts")
	ErrInvalid  = errors.New("proxysecret: file does not hold a valid secret")
)

// Load rend le secret du fichier. Un chemin vide rend un secret vide : pas de
// secret configuré. Rien n'est avalé : un fichier illisible, trop ouvert ou
// trop court arrête le démarrage, jamais un mode forward sans preuve que
// l'opérateur croit protégé. Les erreurs ne citent jamais le contenu.
func Load(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("open forward secret directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("open forward secret file %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	// Le mode se lit sur le fichier ouvert, pas sur le chemin : rien ne peut
	// le remplacer entre le contrôle et la lecture.
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat forward secret file %s: %w", path, err)
	}
	if info.Mode().Perm()&forbiddenBits != 0 {
		return "", fmt.Errorf("%w: %s has mode %04o, want 0640 or stricter", ErrFileMode, path, info.Mode().Perm())
	}
	content, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read forward secret file %s: %w", path, err)
	}
	secret, err := parse(content)
	if err != nil {
		return "", fmt.Errorf("forward secret file %s: %w", path, err)
	}
	return secret, nil
}

// parse veut une seule ligne de caractères ASCII visibles : le secret
// voyage dans un en-tête, et un octet de contrôle ou une espace ne s'y
// retrouveraient pas à l'identique.
func parse(content []byte) (string, error) {
	if len(content) > maxFileBytes {
		return "", fmt.Errorf("%w: too long", ErrInvalid)
	}
	line, _ := bytes.CutSuffix(content, []byte("\n"))
	line, _ = bytes.CutSuffix(line, []byte("\r"))
	if bytes.ContainsAny(line, "\r\n") {
		return "", fmt.Errorf("%w: want exactly one line", ErrInvalid)
	}
	if len(line) < MinLength {
		return "", fmt.Errorf("%w: shorter than %d characters", ErrInvalid, MinLength)
	}
	for _, char := range line {
		if char < '!' || char > '~' {
			return "", fmt.Errorf("%w: only visible ASCII characters, without spaces", ErrInvalid)
		}
	}
	return string(line), nil
}
