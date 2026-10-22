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

// ErrPrivateAddress : la destination résout seulement vers le réseau local
// et l'appelant n'a pas le droit d'y composer (DialWith). Distinct de
// ErrForbiddenAddress : celle-là ne s'ouvre jamais, celle-ci s'ouvre par un droit.
var ErrPrivateAddress = errors.New("private egress address")

// Forbidden dit ce qu'on ne joint jamais : le lien-local, qui contient
// l'adresse de métadonnées des hébergeurs (169.254.169.254), laquelle livre
// les identifiants de l'instance à qui sait la demander. La boucle locale
// et les adresses privées restent ouvertes au serveur : prévenir un ntfy
// sur le LAN est l'usage voulu. Pour les sondes de l'agent, elles dépendent
// du droit probe.lan (DialWith, Private).
// Deux hébergeurs servent leurs métadonnées hors du lien-local, et sont
// nommés un par un : AWS en IPv6 sur fd00:ec2::254, Alibaba Cloud sur
// 100.100.100.200 ; le reste de leurs plages reste ouvert.
func Forbidden(ip net.IP) bool {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	return ip.Equal(net.ParseIP(awsMetadataIPv6)) || ip.Equal(net.ParseIP(alibabaMetadata))
}

const (
	awsMetadataIPv6 = "fd00:ec2::254"
	alibabaMetadata = "100.100.100.200"
)

// Private dit ce qui est « le réseau local » pour une sonde : ce que seul le
// droit probe.lan ouvre. Le lien-local n'en fait pas partie, il reste
// interdit (Forbidden).
func Private(ip net.IP) bool {
	// Les formes IPv4 écrites en IPv6 (::ffff:10.0.0.1) se jugent comme
	// l'IPv4 : sinon elles passeraient pour une IPv6 publique.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	// IsPrivate : RFC 1918 et fc00::/7. IsUnspecified : 0.0.0.0 et ::, que
	// Linux compose vers la boucle locale.
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
		return true
	}
	return sharedAddressSpace.Contains(ip) || siteLocalIPv6.Contains(ip)
}

var (
	// 100.64.0.0/10 (RFC 6598, Tailscale et les opérateurs) : ni IsPrivate
	// ni IsLoopback ne le couvrent.
	sharedAddressSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	// fec0::/10, l'ancien « site-local » IPv6 : déprécié, mais un noyau
	// l'achemine encore s'il est configuré, et c'est du réseau local.
	siteLocalIPv6 = &net.IPNet{IP: net.ParseIP("fec0::"), Mask: net.CIDRMask(10, 128)}
)

// Dial compose en gardant le réseau local ouvert : c'est ce que fait le
// serveur (alertes, e-mail, registres). Voir DialWith.
func Dial(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	return DialWith(ctx, network, address, timeout, true)
}

// DialWith résout le nom lui-même, écarte ce qu'on ne doit pas joindre, puis
// compose vers l'adresse retenue. Composer sur l'adresse déjà résolue ferme
// la porte à un nom qui répondrait autre chose au deuxième appel ; et posé
// sur le Transport d'un client HTTP, il est rappelé à chaque saut, donc à
// chaque redirection. Sans allowPrivate, une adresse du réseau local est
// sautée comme le lien-local, mais l'échec le dit (ErrPrivateAddress).
func DialWith(ctx context.Context, network, address string, timeout time.Duration, allowPrivate bool) (net.Conn, error) {
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
		if !allowPrivate && Private(candidate.IP) {
			// Une adresse interdite plus loin dans la liste ne doit pas
			// masquer ce refus-ci : last ne bouge que s'il était encore le défaut.
			if errors.Is(last, ErrForbiddenAddress) {
				last = ErrPrivateAddress
			}
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
