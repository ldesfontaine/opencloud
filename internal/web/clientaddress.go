package web

import (
	"net/http"
	"net/netip"
	"strings"
)

const forwardedForHeader = "X-Forwarded-For"

// clientAddress rend l'adresse d'origine de la requête : celle de la
// connexion, sauf quand elle appartient à un proxy de confiance déclaré — et
// alors le dernier saut non fiable de X-Forwarded-For. Sans proxy déclaré
// l'en-tête est ignoré, toujours : n'importe qui peut l'écrire.
// L'adresse rendue est invalide quand la connexion n'en a pas de lisible.
func clientAddress(r *http.Request, trustedProxies []netip.Prefix) netip.Addr {
	connection := connectionAddress(r.RemoteAddr)
	if !connection.IsValid() || !isTrustedProxy(connection, trustedProxies) {
		return connection
	}
	return lastUntrustedHop(forwardedHops(r.Header), trustedProxies, connection)
}

// connectionAddress lit l'adresse de RemoteAddr. Unmap : ::ffff:192.0.2.1 et
// 192.0.2.1 sont la même adresse, donc le même compteur.
func connectionAddress(remoteAddress string) netip.Addr {
	addressPort, err := netip.ParseAddrPort(remoteAddress)
	if err != nil {
		return netip.Addr{}
	}
	return addressPort.Addr().Unmap().WithZone("")
}

// forwardedHops rend les sauts de X-Forwarded-For dans l'ordre où ils ont été
// écrits : le client d'abord, chaque proxy traversé ensuite.
func forwardedHops(header http.Header) []string {
	var hops []string
	for _, value := range header.Values(forwardedForHeader) {
		for _, hop := range strings.Split(value, ",") {
			hops = append(hops, strings.TrimSpace(hop))
		}
	}
	return hops
}

// lastUntrustedHop remonte la chaîne depuis la fin — le saut le plus proche de
// nous, écrit par notre propre proxy — et rend le premier qui n'est pas un
// proxy de confiance : c'est le client. Une chaîne entièrement de confiance ou
// un saut illisible rendent l'adresse de la connexion, qui elle ne se forge pas.
func lastUntrustedHop(hops []string, trustedProxies []netip.Prefix, connection netip.Addr) netip.Addr {
	for index := len(hops) - 1; index >= 0; index-- {
		hop, err := netip.ParseAddr(hops[index])
		if err != nil {
			return connection
		}
		hop = hop.Unmap().WithZone("")
		if !isTrustedProxy(hop, trustedProxies) {
			return hop
		}
	}
	return connection
}

func isTrustedProxy(address netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, prefix := range trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
