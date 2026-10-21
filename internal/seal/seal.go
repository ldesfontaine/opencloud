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

// Keyring tient les clés du serveur : la première scelle, chacune ouvre ce
// qu'elle a scellé, reconnue par son identifiant. Plusieurs clés ne vivent
// ensemble que le temps d'une rotation. Il est sûr pour plusieurs goroutines.
type Keyring struct {
	keys []*sealingKey
}

type sealingKey struct {
	key  [keyBytes]byte
	id   string
	aead cipher.AEAD
}

// Generate tire une clé neuve. crypto/rand.Read ne rend plus d'erreur depuis
// Go 1.24 : si le système n'a plus d'aléa, le programme s'arrête.
func Generate() *Keyring {
	return &Keyring{keys: []*sealingKey{generateKey()}}
}

func generateKey() *sealingKey {
	var key [keyBytes]byte
	_, _ = rand.Read(key[:])
	sealing, err := newSealingKey(key)
	if err != nil {
		// AES accepte toute clé de 32 octets : ce n'est pas atteignable.
		panic("seal: build cipher: " + err.Error())
	}
	return sealing
}

func newSealingKey(key [keyBytes]byte) (*sealingKey, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealingKey{key: key, id: keyIDOf(key), aead: aead}, nil
}

// WithNewKey rend un trousseau qui scelle avec une clé neuve et ouvre
// encore avec les précédentes : le premier temps d'une rotation.
func (k *Keyring) WithNewKey() *Keyring {
	return &Keyring{keys: append([]*sealingKey{generateKey()}, k.keys...)}
}

// Current rend le trousseau de la seule clé qui scelle : le dernier temps
// d'une rotation, une fois chaque secret rescellé.
func (k *Keyring) Current() *Keyring {
	return &Keyring{keys: k.keys[:1]}
}

// Retired rend le trousseau des clés qui ne scellent plus, vide hors
// rotation : ce qui relit une sauvegarde plus ancienne.
func (k *Keyring) Retired() *Keyring {
	return &Keyring{keys: k.keys[1:]}
}

// Len rend le nombre de clés.
func (k *Keyring) Len() int {
	return len(k.keys)
}

// keyIDOf est l'empreinte courte inscrite dans chaque chiffré et dans le
// fichier de clé.
func keyIDOf(key [keyBytes]byte) string {
	sum := sha256.Sum256(append([]byte(brand.SecretKeyIDDomain), key[:]...))
	return hex.EncodeToString(sum[:keyIDBytes])
}

// ID est l'identifiant court de la clé qui scelle, sans rien qui la révèle.
func (k *Keyring) ID() string {
	return k.keys[0].id
}

// Seal chiffre plaintext pour la ligne que dit scope, avec la première clé.
// Le nonce est tiré à chaque appel : le même clair ne donne jamais deux fois
// le même chiffré.
func (k *Keyring) Seal(plaintext string, scope Scope) string {
	sealing := k.keys[0]
	nonce := make([]byte, nonceBytes)
	_, _ = rand.Read(nonce)
	body := sealing.aead.Seal(nonce, nonce, []byte(plaintext), sealing.associatedData(scope))
	return sealedVersion + ":" + sealing.id + ":" + base64.RawURLEncoding.EncodeToString(body)
}

// Open déchiffre sealed pour la ligne que dit scope, avec la clé dont il
// porte l'identifiant.
func (k *Keyring) Open(sealed string, scope Scope) (string, error) {
	id, body, err := parse(sealed)
	if err != nil {
		return "", err
	}
	sealing := k.keyOf(id)
	if sealing == nil {
		return "", ErrUnknownKey
	}
	nonce, encrypted := body[:nonceBytes], body[nonceBytes:]
	plaintext, err := sealing.aead.Open(nil, nonce, encrypted, sealing.associatedData(scope))
	if err != nil {
		return "", ErrTampered
	}
	return string(plaintext), nil
}

func (k *Keyring) keyOf(id string) *sealingKey {
	for _, sealing := range k.keys {
		if sealing.id == id {
			return sealing
		}
	}
	return nil
}

// IsSealed dit si value a la forme d'un chiffré, sans clé : c'est ce qui
// distingue, à la migration, un secret déjà chiffré d'un clair.
func IsSealed(value string) bool {
	_, _, err := parse(value)
	return err == nil
}

// associatedData lie le chiffré à la clé et à sa ligne. Le séparateur NUL
// ne peut figurer dans un nom de table ni de colonne.
func (k *sealingKey) associatedData(scope Scope) []byte {
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
