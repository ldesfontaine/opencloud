package action

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

// La clé du serveur signe les ordres. Elle vit dans server.key, à côté de
// secret.key et sous les mêmes règles : 0600, lue par descripteur, jamais
// en base. Sa moitié publique est livrée à l'agent à l'enrôlement, qui
// l'épingle ; un ordre qu'elle n'a pas signé est refusé par l'agent.
const (
	keyFileMode     = 0o600
	maxKeyFileBytes = 1024
	keyFileScheme   = "ed25519-v1"
)

var (
	// ErrNoKey : server.key n'existe pas.
	ErrNoKey = errors.New("action: no server key file")
	// ErrKeyExists : CreateKey a trouvé un fichier déjà posé.
	ErrKeyExists = errors.New("action: server key file already exists")
	// ErrKeyFileMode : le fichier est lisible par d'autres comptes.
	ErrKeyFileMode = errors.New("action: server key file is readable by other accounts")
	// ErrKeyFileInvalid : le fichier n'a pas la forme d'une clé.
	ErrKeyFileInvalid = errors.New("action: server key file is not a valid key")
)

// Key est la clé privée du serveur ; sûre pour plusieurs goroutines.
type Key struct {
	private     ed25519.PrivateKey
	fingerprint string
}

// GenerateKey tire une clé neuve. crypto/rand ne rend plus d'erreur depuis
// Go 1.24 : sans aléa, le programme s'arrête.
func GenerateKey() *Key {
	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed)
	return keyFromSeed(seed)
}

func keyFromSeed(seed []byte) *Key {
	private := ed25519.NewKeyFromSeed(seed)
	public, _ := private.Public().(ed25519.PublicKey)
	return &Key{private: private, fingerprint: agentwire.KeyFingerprint(public)}
}

// PublicKey rend la moitié que l'agent épingle.
func (k *Key) PublicKey() ed25519.PublicKey {
	public, _ := k.private.Public().(ed25519.PublicKey)
	return public
}

// Fingerprint est l'empreinte que l'opérateur compare (agentwire.KeyFingerprint).
func (k *Key) Fingerprint() string {
	return k.fingerprint
}

// LoadKey lit server.key sous root.
func LoadKey(root *os.Root) (*Key, error) {
	content, err := fsx.ReadPrivate(root, brand.ServerKeyFile, maxKeyFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoKey
	case errors.Is(err, fsx.ErrTooOpen):
		return nil, fmt.Errorf("%w: %v", ErrKeyFileMode, err)
	case err != nil:
		return nil, fmt.Errorf("read server key: %w", err)
	}
	return parseKeyFile(content)
}

// Le fichier garde une ligne « ed25519-v1:<graine en base64url> ».
func parseKeyFile(content []byte) (*Key, error) {
	if len(content) > maxKeyFileBytes {
		return nil, fmt.Errorf("%w: too long", ErrKeyFileInvalid)
	}
	line, _ := bytes.CutSuffix(content, []byte("\n"))
	if len(line) == 0 || bytes.ContainsAny(line, "\r\n") {
		return nil, fmt.Errorf("%w: want exactly one line", ErrKeyFileInvalid)
	}
	scheme, encoded, found := strings.Cut(string(line), ":")
	if !found || scheme != keyFileScheme {
		return nil, fmt.Errorf("%w: unknown format", ErrKeyFileInvalid)
	}
	seed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: bad key bytes", ErrKeyFileInvalid)
	}
	return keyFromSeed(seed), nil
}

// CreateKey écrit la clé en 0600 seulement si le fichier n'existe pas : deux
// serveurs lancés ensemble gardent la même, l'autre la relit.
func CreateKey(root *os.Root, key *Key) error {
	err := fsx.CreateExclusive(root, brand.ServerKeyFile, key.fileContent(), keyFileMode)
	if errors.Is(err, fs.ErrExist) {
		return ErrKeyExists
	}
	if err != nil {
		return fmt.Errorf("create server key file: %w", err)
	}
	return nil
}

func (k *Key) fileContent() []byte {
	return []byte(keyFileScheme + ":" + base64.RawURLEncoding.EncodeToString(k.private.Seed()) + "\n")
}

// LoadOrCreateKey lit la clé, ou la crée au premier démarrage.
func LoadOrCreateKey(root *os.Root) (key *Key, created bool, err error) {
	key, err = LoadKey(root)
	if !errors.Is(err, ErrNoKey) {
		return key, false, err
	}
	key = GenerateKey()
	err = CreateKey(root, key)
	if errors.Is(err, ErrKeyExists) {
		key, err = LoadKey(root)
		return key, false, err
	}
	return key, err == nil, err
}

// Sign signe le corps de l'ordre : les octets signés sont ceux qui voyagent.
func (k *Key) Sign(order Order) (agentwire.SignedOrder, error) {
	body, err := order.encode()
	if err != nil {
		return agentwire.SignedOrder{}, err
	}
	signature := ed25519.Sign(k.private, agentwire.OrderSigningBytes(body))
	return agentwire.SignedOrder{Body: body, Signature: signature, Key: k.fingerprint}, nil
}
