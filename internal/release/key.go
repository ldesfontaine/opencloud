package release

import "errors"

// TrustedPublicKey est la clé publique des versions, au format minisign.
// Vide tant que la paire n'est pas générée (docs/projet/12-methode.md) : le
// binaire refuse alors de vérifier quoi que ce soit. La changer, c'est ne
// plus reconnaître les versions signées par l'ancienne.
const TrustedPublicKey = ""

// ErrNoTrustedKey : ce binaire n'embarque aucune clé de version.
var ErrNoTrustedKey = errors.New("release: no trusted public key embedded in this binary")

// TrustedKey rend la clé publique embarquée dans ce binaire.
func TrustedKey() (PublicKey, error) {
	if TrustedPublicKey == "" {
		return PublicKey{}, ErrNoTrustedKey
	}
	return ParsePublicKey(TrustedPublicKey)
}
