package egress

import (
	"context"
	"errors"
	"net"
	"time"
)

// ErrForbiddenAddress : la destination résout vers une adresse qu'openCloud
// ne doit pas joindre.
var ErrForbiddenAddress = errors.New("forbidden egress address")

// Forbidden dit ce qu'on ne joint jamais : le lien-local, qui contient
// l'adresse de métadonnées des hébergeurs (169.254.169.254), laquelle livre
// les identifiants de l'instance à qui sait la demander. La boucle locale
// et les adresses privées restent ouvertes : sonder un service par l'agent
// de sa propre machine, ou prévenir un ntfy sur le LAN, est l'usage voulu.
func Forbidden(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast()
}

// Dial résout le nom lui-même, écarte ce qu'on ne doit pas joindre, puis
// compose vers l'adresse retenue. Composer sur l'adresse déjà résolue ferme
// la porte à un nom qui répondrait autre chose au deuxième appel ; et posé
// sur le Transport d'un client HTTP, il est rappelé à chaque saut, donc à
// chaque redirection.
func Dial(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: timeout}
	var last error = ErrForbiddenAddress
	for _, candidate := range resolved {
		if Forbidden(candidate.IP) {
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
