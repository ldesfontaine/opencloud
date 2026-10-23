package fsx

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Les bits que 0600 interdit : tout accès pour le groupe et les autres.
const privateForbiddenBits = 0o077

// ErrTooOpen : le fichier est lisible par d'autres comptes que le sien.
var ErrTooOpen = errors.New("file is readable by other accounts")

// ReadPrivate lit une clé ou un secret sous root : le mode se juge sur le
// descripteur ouvert, pas sur le chemin, et la lecture s'arrête à limit+1
// octets pour que l'appelant voie un fichier trop long. Un fichier absent
// rend une erreur qui satisfait errors.Is(err, fs.ErrNotExist).
func ReadPrivate(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", name, err)
	}
	if info.Mode().Perm()&privateForbiddenBits != 0 {
		return nil, fmt.Errorf("%w: %s has mode %04o", ErrTooOpen, name, info.Mode().Perm())
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return content, nil
}
