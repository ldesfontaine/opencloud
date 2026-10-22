package apitoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Le préfixe des jetons : reconnaissable dans un journal ou un
// presse-papier, jamais confondu avec un jeton d'enrôlement (oc_) ni de
// ping (hb_).
const scheme = brand.APITokenScheme

const (
	secretBytes = 32
	// Le préfixe affiché : le schéma plus six caractères, 30 bits sur 256.
	shownLength = 6
	idLength    = 16
)

// generated est un jeton tiré : le clair, à rendre une seule fois, et ce
// que la base garde.
type generated struct {
	cleartext string
	hash      string
	prefix    string
	id        string
}

func newSecret() (generated, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return generated{}, err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	cleartext := scheme + strings.ToLower(encoded)
	hash := HashSecret(cleartext)
	return generated{
		cleartext: cleartext,
		hash:      hash,
		prefix:    cleartext[:len(scheme)+shownLength],
		id:        hash[:idLength],
	}, nil
}

// HashSecret est la seule forme d'un jeton que la base voit. SHA-256 sans
// dérivation lente : l'entrée est un aléa de 256 bits, pas un mot de passe.
func HashSecret(cleartext string) string {
	sum := sha256.Sum256([]byte(cleartext))
	return hex.EncodeToString(sum[:])
}
