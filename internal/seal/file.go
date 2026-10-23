package seal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const (
	keyFileMode = 0o600
	// Une ligne de clé tient sous cent octets ; quelques clés le temps d'une
	// rotation : au-delà, ce n'est pas un fichier de clé.
	maxKeyFileBytes = 1024
	maxKeys         = 4
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

// Load lit les clés sous root. Le fichier garde une ligne
// « key-v1:<id>:<clé> » par clé : la première scelle, toutes ouvrent. Hors
// rotation, il n'y en a qu'une.
func Load(root *os.Root) (*Keyring, error) {
	content, err := fsx.ReadPrivate(root, brand.SecretKeyFile, maxKeyFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoKey
	case errors.Is(err, fsx.ErrTooOpen):
		return nil, fmt.Errorf("%w: %v", ErrKeyFileMode, err)
	case err != nil:
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return parseKeyFile(content)
}

func parseKeyFile(content []byte) (*Keyring, error) {
	if len(content) > maxKeyFileBytes {
		return nil, fmt.Errorf("%w: too long", ErrKeyFileInvalid)
	}
	text, _ := bytes.CutSuffix(content, []byte("\n"))
	if len(text) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrKeyFileInvalid)
	}
	lines := strings.Split(string(text), "\n")
	if len(lines) > maxKeys {
		return nil, fmt.Errorf("%w: more than %d keys", ErrKeyFileInvalid, maxKeys)
	}
	keyring := &Keyring{}
	for _, line := range lines {
		sealing, err := parseKeyLine(line)
		if err != nil {
			return nil, err
		}
		if keyring.keyOf(sealing.id) != nil {
			return nil, fmt.Errorf("%w: the same key twice", ErrKeyFileInvalid)
		}
		keyring.keys = append(keyring.keys, sealing)
	}
	return keyring, nil
}

func parseKeyLine(line string) (*sealingKey, error) {
	if line == "" || strings.ContainsRune(line, '\r') {
		return nil, fmt.Errorf("%w: a blank or broken line", ErrKeyFileInvalid)
	}
	parts := strings.Split(line, ":")
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
	sealing, err := newSealingKey(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyFileInvalid, err)
	}
	return sealing, nil
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

// Save remplace le fichier de clé d'un coup : la rotation y pose la neuve
// devant les anciennes, puis la neuve seule.
func Save(root *os.Root, keyring *Keyring) error {
	if err := fsx.WriteAtomic(root, brand.SecretKeyFile, keyring.fileContent(), keyFileMode); err != nil {
		return fmt.Errorf("save key file: %w", err)
	}
	return nil
}

// SaveAs écrit le trousseau sous un autre nom, sans rien remplacer : une
// clé mise de côté.
func SaveAs(root *os.Root, name string, keyring *Keyring) error {
	if err := fsx.CreateExclusive(root, name, keyring.fileContent(), keyFileMode); err != nil {
		return fmt.Errorf("set aside keys: %w", err)
	}
	return nil
}

func (k *Keyring) fileContent() []byte {
	var content strings.Builder
	for _, sealing := range k.keys {
		content.WriteString(keyFileScheme + ":" + sealing.id + ":" + base64.RawURLEncoding.EncodeToString(sealing.key[:]) + "\n")
	}
	return []byte(content.String())
}
