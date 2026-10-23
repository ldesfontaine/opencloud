package release

import "errors"

// TrustedPublicKey est la clé publique des versions, au format minisign.
// Vide, le binaire refuse de vérifier quoi que ce soit
// (docs/projet/12-methode.md). La changer, c'est ne plus reconnaître les
// versions signées par l'ancienne.
const TrustedPublicKey = "RWR3td9Uebbdsc6fVgCOun/FNjBDAx5QCbN8hjrKBPtW47xwkbf1F+o3"

// ErrNoTrustedKey : ce binaire n'embarque aucune clé de version.
var ErrNoTrustedKey = errors.New("release: no trusted public key embedded in this binary")

// TrustedKey rend la clé publique embarquée dans ce binaire.
func TrustedKey() (PublicKey, error) {
	if TrustedPublicKey == "" {
		return PublicKey{}, ErrNoTrustedKey
	}
	return ParsePublicKey(TrustedPublicKey)
}
