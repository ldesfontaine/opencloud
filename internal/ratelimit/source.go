package ratelimit

import "net/netip"

// ipv6PrefixBits : un abonné IPv6 reçoit au moins un /64 et choisit à
// l'intérieur autant d'adresses qu'il veut. Une clé par adresse complète
// donnerait à une seule source autant de seaux pleins qu'elle a d'adresses.
const ipv6PrefixBits = 64

// sourceKey est la clé de limitation d'une adresse cliente : l'adresse
// elle-même en IPv4 (y compris une IPv4 mappée en IPv6), le /64 en IPv6.
// Une valeur qui n'est pas une adresse reste telle quelle. Elle ne sert
// qu'à limiter : le journal garde l'adresse complète.
func sourceKey(address string) string {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	if parsed.Is4In6() {
		return parsed.Unmap().String()
	}
	if parsed.Is4() {
		return parsed.String()
	}
	// WithZone("") : deux zones du même lien sont la même source.
	prefix, err := parsed.WithZone("").Prefix(ipv6PrefixBits)
	if err != nil {
		return address
	}
	return prefix.String()
}
