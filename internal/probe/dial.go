package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"
)

// errForbiddenAddress : la cible résout vers une adresse qu'une sonde ne
// doit pas joindre.
var errForbiddenAddress = errors.New("forbidden probe address")

// guardedDial résout le nom lui-même, écarte ce qu'une sonde ne doit pas
// joindre, puis compose vers l'adresse retenue. Composer sur l'adresse
// déjà résolue ferme la porte à un nom qui répondrait autre chose au
// deuxième appel.
func guardedDial(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: timeout}
	var last error = errForbiddenAddress
	for _, candidate := range resolved {
		if forbiddenIP(candidate.IP) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// inspectTLS ouvre une poignée de main nue pour savoir si l'hôte répond et
// lire la chaîne qu'il présente. La requête n'est jamais rejouée : rien de
// ce qu'elle portait ne part vers un pair dont la chaîne est refusée.
func inspectTLS(ctx context.Context, address Address, timeout time.Duration) (tls.ConnectionState, error) {
	raw, err := guardedDial(ctx, "tcp", address.String(), timeout)
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
