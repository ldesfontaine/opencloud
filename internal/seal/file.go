package seal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const (
	keyFileMode = 0o600
	// Les bits que 0600 interdit : tout accès pour le groupe et les autres.
	keyFileForbiddenBits = 0o077
	// Une ligne de clé tient sous cent octets : au-delà, ce n'est pas elle.
	maxKeyFileBytes = 1024
	keyFileScheme   = "key-v1"
)

var (
	// ErrNoKey : le fichier de clé n'existe pas.
	ErrNoKey = errors.New("seal: no key file")
	// ErrKeyExists : Create a trouvé un fichier de clé déjà posé.
	ErrKeyExists = errors.New("seal: key file already exists")
	// ErrKeyFileMode : le fichier de clé est lisible par d'autres comptes.
	ErrKeyFileMode = errors.New("seal: key file is readable by other accounts")
	// ErrKeyFileInvalid : le fichier existe mais n'est pas un fichier de clé
	// intact (forme, longueur, empreinte, nombre de lignes).
	ErrKeyFileInvalid = errors.New("seal: key file is not a valid key")
)

// Load lit la clé sous root. Le fichier garde une ligne
// « key-v1:<id>:<clé> » ; le format laisse la place à plusieurs lignes pour
// une rotation future, qui n'existe pas encore : une seule est acceptée.
func Load(root *os.Root) (*Keyring, error) {
	file, err := root.Open(brand.SecretKeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, fmt.Errorf("open key file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat key file: %w", err)
	}
	if info.Mode().Perm()&keyFileForbiddenBits != 0 {
		return nil, fmt.Errorf("%w: mode %04o", ErrKeyFileMode, info.Mode().Perm())
	}
	content, err := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return parseKeyFile(content)
}

func parseKeyFile(content []byte) (*Keyring, error) {
	if len(content) > maxKeyFileBytes {
		return nil, fmt.Errorf("%w: too long", ErrKeyFileInvalid)
	}
	line, _ := bytes.CutSuffix(content, []byte("\n"))
	if len(line) == 0 || bytes.ContainsAny(line, "\r\n") {
		return nil, fmt.Errorf("%w: want exactly one line", ErrKeyFileInvalid)
	}
	parts := strings.Split(string(line), ":")
	if len(parts) != 3 || parts[0] != keyFileScheme {
		return nil, fmt.Errorf("%w: unknown format", ErrKeyFileInvalid)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(decoded) != keyBytes {
		return nil, fmt.Errorf("%w: bad key bytes", ErrKeyFileInvalid)
	}
	var key [keyBytes]byte
	copy(key[:], decoded)
	if keyIDOf(key) != parts[1] {
		return nil, fmt.Errorf("%w: id does not match the key", ErrKeyFileInvalid)
	}
	keyring, err := newKeyring(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyFileInvalid, err)
	}
	return keyring, nil
}

// Save écrit la clé sous root, en 0600, remplaçant celle qui y était. Seul
// `secrets forget` remplace une clé ; tout le reste passe par Create.
func Save(root *os.Root, keyring *Keyring) error {
	if err := fsx.WriteAtomic(root, brand.SecretKeyFile, keyring.fileContent(), keyFileMode); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}

// Create écrit la clé sous root en 0600 seulement si le fichier n'existe pas,
// sinon ErrKeyExists : deux serveurs lancés ensemble sur une base neuve
// tirent chacun une clé, une seule est gardée, et l'autre doit relire celle-là
// plutôt que chiffrer avec une clé qui n'existera plus.
func Create(root *os.Root, keyring *Keyring) error {
	err := fsx.CreateExclusive(root, brand.SecretKeyFile, keyring.fileContent(), keyFileMode)
	if errors.Is(err, fs.ErrExist) {
		return ErrKeyExists
	}
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	return nil
}

func (k *Keyring) fileContent() []byte {
	return []byte(keyFileScheme + ":" + k.id + ":" + base64.RawURLEncoding.EncodeToString(k.key[:]) + "\n")
}
