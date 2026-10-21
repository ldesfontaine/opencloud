package ident

import (
	"crypto/rand"
	"encoding/hex"
)

// randomBytes : 64 bits, seize hexadécimaux. Le format est figé : les
// adresses de l'interface et les préfixes de l'annuaire en dépendent.
const randomBytes = 8

// New tire un identifiant de seize hexadécimaux en minuscules.
func New() (string, error) {
	raw := make([]byte, randomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
