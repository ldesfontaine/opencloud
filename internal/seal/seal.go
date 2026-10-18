package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	// 256 bits : AES-256.
	keyBytes = 32
	// 96 bits, la taille que GCM attend ; tiré au hasard à chaque chiffrement.
	nonceBytes = 12
	// Quatre octets d'empreinte : de quoi reconnaître une clé qui n'est pas
	// la bonne, pas de quoi la retrouver.
	keyIDBytes = 4
	keyIDChars = keyIDBytes * 2

	sealedVersion = "v1"
	// Le plus petit chiffré possible : un nonce et l'étiquette GCM d'un
	// texte vide.
	minSealedBytes = nonceBytes + 16
)

var (
	// ErrMalformed : la valeur n'a pas la forme d'un chiffré.
	ErrMalformed = errors.New("seal: value is not a sealed secret")
	// ErrUnknownKey : le chiffré vient d'une autre clé que celle-ci.
	ErrUnknownKey = errors.New("seal: secret was sealed with another key")
	// ErrTampered : la clé est la bonne mais le chiffré a changé, ou il a
	// été copié depuis une autre ligne ou une autre colonne.
	ErrTampered = errors.New("seal: secret was altered or moved")
)

// Scope dit d'où vient un secret. Il entre dans les données associées de
// GCM : un chiffré recopié dans une autre ligne ne s'ouvre plus.
type Scope struct {
	Table  string
	Column string
	Row    string
}

// Keyring tient la clé du serveur. Il est sûr pour plusieurs goroutines.
type Keyring struct {
	key  [keyBytes]byte
	id   string
	aead cipher.AEAD
}

// Generate tire une clé neuve. crypto/rand.Read ne rend plus d'erreur depuis
// Go 1.24 : si le système n'a plus d'aléa, le programme s'arrête.
func Generate() *Keyring {
	var key [keyBytes]byte
	_, _ = rand.Read(key[:])
	keyring, err := newKeyring(key)
	if err != nil {
		// AES accepte toute clé de 32 octets : ce n'est pas atteignable.
		panic("seal: build cipher: " + err.Error())
	}
	return keyring
}

func newKeyring(key [keyBytes]byte) (*Keyring, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keyring{key: key, id: keyIDOf(key), aead: aead}, nil
}

// keyIDOf est l'empreinte courte inscrite dans chaque chiffré et dans le
// fichier de clé.
func keyIDOf(key [keyBytes]byte) string {
	sum := sha256.Sum256(append([]byte(brand.SecretKeyIDDomain), key[:]...))
	return hex.EncodeToString(sum[:keyIDBytes])
}

// ID est l'identifiant court de la clé, sans rien qui la révèle.
func (k *Keyring) ID() string {
	return k.id
}

// Seal chiffre plaintext pour la ligne que dit scope. Le nonce est tiré à
// chaque appel : le même clair ne donne jamais deux fois le même chiffré.
func (k *Keyring) Seal(plaintext string, scope Scope) string {
	nonce := make([]byte, nonceBytes)
	_, _ = rand.Read(nonce)
	body := k.aead.Seal(nonce, nonce, []byte(plaintext), k.associatedData(scope))
	return sealedVersion + ":" + k.id + ":" + base64.RawURLEncoding.EncodeToString(body)
}

// Open déchiffre sealed pour la ligne que dit scope.
func (k *Keyring) Open(sealed string, scope Scope) (string, error) {
	id, body, err := parse(sealed)
	if err != nil {
		return "", err
	}
	if id != k.id {
		return "", ErrUnknownKey
	}
	nonce, encrypted := body[:nonceBytes], body[nonceBytes:]
	plaintext, err := k.aead.Open(nil, nonce, encrypted, k.associatedData(scope))
	if err != nil {
		return "", ErrTampered
	}
	return string(plaintext), nil
}

// IsSealed dit si value a la forme d'un chiffré, sans clé : c'est ce qui
// distingue, à la migration, un secret déjà chiffré d'un clair.
func IsSealed(value string) bool {
	_, _, err := parse(value)
	return err == nil
}

// associatedData lie le chiffré à la clé et à sa ligne. Le séparateur NUL
// ne peut figurer dans un nom de table ni de colonne.
func (k *Keyring) associatedData(scope Scope) []byte {
	return []byte(sealedVersion + ":" + k.id + "\x00" + scope.Table + "\x00" + scope.Column + "\x00" + scope.Row)
}

// parse rend l'identifiant de clé et le corps (nonce puis chiffré). Le
// décodage strict refuse les formes que Seal n'écrit pas.
func parse(sealed string) (id string, body []byte, err error) {
	parts := strings.Split(sealed, ":")
	if len(parts) != 3 || parts[0] != sealedVersion || !isKeyID(parts[1]) {
		return "", nil, ErrMalformed
	}
	body, err = base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(body) < minSealedBytes {
		return "", nil, ErrMalformed
	}
	return parts[1], body, nil
}

func isKeyID(value string) bool {
	if len(value) != keyIDChars {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == keyIDBytes && value == strings.ToLower(value)
}
