package heartbeat

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

const (
	// Le préfixe rend un jeton de ping reconnaissable dans un cron ou un journal.
	tokenScheme = "hb_" // #nosec G101 -- un préfixe, pas un secret.
	// 128 bits : assez pour un secret d'URL, court dans une ligne de cron.
	tokenBytes = 16
	idBytes    = 8
)

// NewToken tire le secret de l'URL de ping. La base le garde chiffré, car la
// page du moniteur doit pouvoir le réafficher ; HashToken sert à le chercher.
func NewToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return tokenScheme + strings.ToLower(encoded), nil
}

// HashToken est la forme du jeton qu'on cherche en base : le chiffré change à
// chaque écriture, l'empreinte non. Un SHA-256 simple suffit, le jeton tire
// 128 bits et l'empreinte ne dépend d'aucune clé.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewID tire l'identifiant du moniteur, celui des URL de l'interface ; il
// n'est pas le jeton, pour que la page ne porte pas le secret.
func NewID() (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
