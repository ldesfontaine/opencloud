package probe

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/ldesfontaine/opencloud/internal/egress"
)

// La sonde compose par la garde de sortie : elle résout elle-même, écarte
// le lien-local, et compose sur l'adresse retenue.

// inspectTLS ouvre une poignée de main nue pour savoir si l'hôte répond et
// lire la chaîne qu'il présente. La requête n'est jamais rejouée : rien de
// ce qu'elle portait ne part vers un pair dont la chaîne est refusée.
func inspectTLS(ctx context.Context, address Address, timeout time.Duration) (tls.ConnectionState, error) {
	raw, err := egress.Dial(ctx, "tcp", address.String(), timeout)
	if err != nil {
		return tls.ConnectionState{}, err
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName: address.Host,
		// #nosec G402 -- poignée de main d'inspection : elle n'envoie aucun
		// octet applicatif et sert justement à lire une chaîne qu'on a déjà
		// refusée ; la validation est faite ensuite par l'appelant. Le
		// minimum descend à TLS 1.0 pour que la pile périmée d'un hôte se
		// voie sur sa fiche, au lieu de le rendre « injoignable ». La
		// requête, elle, reste à TLS 1.2 : voir newClient.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS10,
	})
	defer func() { _ = conn.Close() }()
	if err := conn.HandshakeContext(ctx); err != nil {
		return tls.ConnectionState{}, err
	}
	return conn.ConnectionState(), nil
}
