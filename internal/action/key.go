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
//
// Le temps d'une rotation, le fichier porte une seconde ligne : la clé
// précédente. Le serveur signe alors chaque ordre avec la clé que son agent
// a épinglée, et livre la neuve à un agent resté sur la précédente par un
// ordre que celle-ci signe (agent.server_key).
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
	// ErrRotationPending : une rotation est déjà en cours, la clé
	// précédente est encore là.
	ErrRotationPending = errors.New("action: a server key rotation is already in progress")
	// ErrNoRotation : aucune rotation à finir.
	ErrNoRotation = errors.New("action: no server key rotation in progress")
)

// Key est la clé du serveur, et la précédente le temps d'une rotation ;
// sûre pour plusieurs goroutines.
type Key struct {
	current  *signingKey
	previous *signingKey
}

type signingKey struct {
	private     ed25519.PrivateKey
	fingerprint string
}

// GenerateKey tire une clé neuve. crypto/rand ne rend plus d'erreur depuis
// Go 1.24 : sans aléa, le programme s'arrête.
func GenerateKey() *Key {
	return &Key{current: generateSigningKey()}
}

func generateSigningKey() *signingKey {
	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed)
	return signingKeyFromSeed(seed)
}

func signingKeyFromSeed(seed []byte) *signingKey {
	private := ed25519.NewKeyFromSeed(seed)
	public, _ := private.Public().(ed25519.PublicKey)
	return &signingKey{private: private, fingerprint: agentwire.KeyFingerprint(public)}
}

func (k *signingKey) public() ed25519.PublicKey {
	public, _ := k.private.Public().(ed25519.PublicKey)
	return public
}

// PublicKey rend la moitié que l'agent épingle : celle de la clé courante.
func (k *Key) PublicKey() ed25519.PublicKey {
	return k.current.public()
}

// Fingerprint est l'empreinte que l'opérateur compare (agentwire.KeyFingerprint).
func (k *Key) Fingerprint() string {
	return k.current.fingerprint
}

// PreviousFingerprint est celle de la clé précédente, vide hors rotation.
func (k *Key) PreviousFingerprint() string {
	if k.previous == nil {
		return ""
	}
	return k.previous.fingerprint
}

// Rotated rend la clé d'une rotation qui commence : une neuve devant, la
// courante en précédente. Une seule rotation à la fois.
func (k *Key) Rotated() (*Key, error) {
	if k.previous != nil {
		return nil, ErrRotationPending
	}
	return &Key{current: generateSigningKey(), previous: k.current}, nil
}

// Finished rend la clé d'une rotation finie : la courante seule.
func (k *Key) Finished() (*Key, error) {
	if k.previous == nil {
		return nil, ErrNoRotation
	}
	return &Key{current: k.current}, nil
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

// Le fichier garde une ligne « ed25519-v1:<graine en base64url> » : la clé
// courante, puis la précédente le temps d'une rotation.
func parseKeyFile(content []byte) (*Key, error) {
	if len(content) > maxKeyFileBytes {
		return nil, fmt.Errorf("%w: too long", ErrKeyFileInvalid)
	}
	text, _ := bytes.CutSuffix(content, []byte("\n"))
	lines := strings.Split(string(text), "\n")
	if len(lines) > 2 {
		return nil, fmt.Errorf("%w: want one or two lines", ErrKeyFileInvalid)
	}
	current, err := parseKeyLine(lines[0])
	if err != nil {
		return nil, err
	}
	key := &Key{current: current}
	if len(lines) == 2 {
		if key.previous, err = parseKeyLine(lines[1]); err != nil {
			return nil, err
		}
	}
	return key, nil
}

func parseKeyLine(line string) (*signingKey, error) {
	if line == "" || strings.ContainsRune(line, '\r') {
		return nil, fmt.Errorf("%w: a blank or broken line", ErrKeyFileInvalid)
	}
	scheme, encoded, found := strings.Cut(line, ":")
	if !found || scheme != keyFileScheme {
		return nil, fmt.Errorf("%w: unknown format", ErrKeyFileInvalid)
	}
	seed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: bad key bytes", ErrKeyFileInvalid)
	}
	return signingKeyFromSeed(seed), nil
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

// SaveKey remplace server.key d'un coup : au début et à la fin d'une
// rotation.
func SaveKey(root *os.Root, key *Key) error {
	if err := fsx.WriteAtomic(root, brand.ServerKeyFile, key.fileContent(), keyFileMode); err != nil {
		return fmt.Errorf("save server key file: %w", err)
	}
	return nil
}

func (k *Key) fileContent() []byte {
	content := keyLine(k.current)
	if k.previous != nil {
		content += keyLine(k.previous)
	}
	return []byte(content)
}

func keyLine(key *signingKey) string {
	return keyFileScheme + ":" + base64.RawURLEncoding.EncodeToString(key.private.Seed()) + "\n"
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

// Sign signe le corps de l'ordre avec la clé que l'agent a épinglée : la
// précédente pour un agent resté sur elle pendant une rotation, la courante
// sinon. Les octets signés sont ceux qui voyagent.
func (k *Key) Sign(order Order, pinned string) (agentwire.SignedOrder, error) {
	signer := k.current
	if k.previous != nil && pinned == k.previous.fingerprint {
		signer = k.previous
	}
	body, err := order.encode()
	if err != nil {
		return agentwire.SignedOrder{}, err
	}
	signature := ed25519.Sign(signer.private, agentwire.OrderSigningBytes(body))
	return agentwire.SignedOrder{Body: body, Signature: signature, Key: signer.fingerprint}, nil
}
