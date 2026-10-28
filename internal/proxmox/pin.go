package proxmox

import (
	"crypto/tls"
	"errors"
)

// ErrPinRequired : un appelant qui parle à un Proxmox sans magasin de
// certificats (l'agent, sur la boucle locale) n'accepte que l'empreinte.
var ErrPinRequired = errors.New("a certificate fingerprint is required")

// PinnedTLSConfig rend la configuration TLS qui juge le certificat par une
// empreinte SHA-256 et par elle seule. L'empreinte s'écrit comme l'opérateur la
// donne (openssl, avec ou sans deux-points) ; sans empreinte valide, rien ne
// s'ouvre : jamais de connexion non vérifiée.
func PinnedTLSConfig(rawPin string) (*tls.Config, error) {
	pin, err := normalizePin(rawPin)
	if err != nil {
		return nil, err
	}
	if pin == "" {
		return nil, ErrPinRequired
	}
	return tlsConfig(pin), nil
}

// ErrPinMismatch : le certificat présenté n'est pas celui de l'empreinte.
var ErrPinMismatch = errPinMismatch
