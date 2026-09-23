package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Chaque sorte de secret porte son préfixe : reconnaissable dans un
// journal ou un presse-papier, jamais confondu avec un jeton d'enrôlement
// (oc_) ni de ping (hb_).
const (
	secretScheme  = "ocs_" // #nosec G101 -- un préfixe, pas un secret.
	codeScheme    = "occ_" // #nosec G101
	accessScheme  = "oca_" // #nosec G101
	refreshScheme = "ocr_" // #nosec G101
	apiScheme     = "ock_" // #nosec G101

	secretBytes = 32
	// Le préfixe affiché : le schéma plus six caractères, 30 bits sur 256.
	shownLength = 6
	idLength    = 16
	familyBytes = 8
)

// generated est un secret tiré : le clair, à rendre une seule fois, et ce
// que la base garde.
type generated struct {
	cleartext string
	hash      string
	prefix    string
	id        string
}

func newSecret(scheme string) (generated, error) {
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

// HashSecret est la seule forme d'un secret, d'un code ou d'un jeton que
// la base voit. SHA-256 sans dérivation lente : l'entrée est un aléa de
// 256 bits, pas un mot de passe.
func HashSecret(cleartext string) string {
	sum := sha256.Sum256([]byte(cleartext))
	return hex.EncodeToString(sum[:])
}

// secretMatches compare en temps constant le secret présenté à l'empreinte
// stockée.
func secretMatches(presented, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashSecret(presented)), []byte(hash)) == 1
}

// verifierMatches vérifie PKCE S256 : base64url(SHA-256(verifier)) doit
// être le défi présenté à l'autorisation.
func verifierMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func newFamilyID() (string, error) {
	raw := make([]byte, familyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
