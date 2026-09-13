package machine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

const (
	// Le préfixe rend un jeton reconnaissable dans un journal ou un presse-papier.
	tokenScheme = "oc_" // #nosec G101 -- un préfixe, pas un secret.
	tokenBytes  = 32
	// Le préfixe affiché : le schéma plus six caractères, 30 bits sur 256.
	tokenPrefixLength = len(tokenScheme) + 6
	tokenIDLength     = 16
)

// NewToken tire un jeton et renvoie son clair, à montrer une seule fois, et
// la ligne à stocker, qui n'en garde que l'empreinte et le préfixe.
func NewToken(name, machineID string) (string, Token, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	cleartext := tokenScheme + strings.ToLower(encoded)
	hash := HashToken(cleartext)
	token := Token{
		ID:        hash[:tokenIDLength],
		Hash:      hash,
		Prefix:    cleartext[:tokenPrefixLength],
		Name:      name,
		MachineID: machineID,
	}
	return cleartext, token, nil
}

// HashToken est la seule forme du jeton que la base voit.
func HashToken(cleartext string) string {
	sum := sha256.Sum256([]byte(cleartext))
	return hex.EncodeToString(sum[:])
}

// Masked est tout ce qu'une page peut montrer d'un jeton déjà émis.
func (t Token) Masked() string {
	return t.Prefix + "…"
}
