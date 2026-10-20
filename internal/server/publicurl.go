package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// resolvePublicURL donne l'adresse que les machines doivent appeler : la
// configuration d'abord, sinon ce que dit un mandataire de confiance, sinon
// l'hôte de la requête. Le booléen dit si elle paraît locale, auquel cas une
// machine distante ne la joindra pas.
func (s *Server) resolvePublicURL(r *http.Request) (string, bool) {
	if s.publicURL != "" {
		return strings.TrimRight(s.publicURL, "/"), looksLocal(hostOf(s.publicURL))
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if s.trustsPeer(r) {
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
		if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" || proto == "http" {
			scheme = proto
		}
	}
	return scheme + "://" + host, looksLocal(host)
}

// clientAddress est l'adresse de l'agent telle que le serveur la voit, ou
// celle que transmet un mandataire de confiance. X-Forwarded-For se lit de
// la droite : chaque mandataire ajoute à la fin l'adresse qu'il a vue, et
// seules les entrées ajoutées par les nôtres sont sûres. La plus à droite
// qui n'est pas l'un d'eux est le client ; ce qui est à sa gauche, le
// client l'a écrit lui-même.
func (s *Server) clientAddress(r *http.Request) string {
	peer := httpx.PeerHost(r)
	if !s.trustsPeer(r) {
		return peer
	}
	var entries []string
	for _, value := range r.Header.Values("X-Forwarded-For") {
		for _, entry := range strings.Split(value, ",") {
			if entry = strings.TrimSpace(entry); entry != "" {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		return peer
	}
	for i := len(entries) - 1; i >= 0; i-- {
		address, err := netip.ParseAddr(entries[i])
		if err != nil || !s.trustsAddress(address) {
			return entries[i]
		}
	}
	// Toute la chaîne est faite de nos mandataires : le premier a vu le client.
	return entries[0]
}

// Un en-tête X-Forwarded-* n'est cru que d'un mandataire déclaré.
func (s *Server) trustsPeer(r *http.Request) bool {
	peer, err := netip.ParseAddr(httpx.PeerHost(r))
	if err != nil {
		return false
	}
	return s.trustsAddress(peer)
}

func (s *Server) trustsAddress(address netip.Addr) bool {
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

// loopbackRanges : tout ce que le noyau donne comme adresse source à un
// processus de la même machine.
var loopbackRanges = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// TrustsLoopback dit si un préfixe de confiance couvre des adresses de
// boucle locale, par ce qu'il contient comme par ce qu'il enveloppe
// (0.0.0.0/0). Sur une machine, la boucle locale n'est pas celle d'un
// mandataire : c'est celle de tous les processus, et l'adresse du pair ne
// prouve plus rien.
func TrustsLoopback(prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		for _, loopback := range loopbackRanges {
			if prefix.Overlaps(loopback) {
				return true
			}
		}
	}
	return false
}

func hostOf(rawURL string) string {
	_, rest, ok := strings.Cut(rawURL, "://")
	if !ok {
		rest = rawURL
	}
	host, _, _ := strings.Cut(rest, "/")
	return host
}

func looksLocal(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	lower := strings.ToLower(host)
	return lower == "localhost" || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".localhost")
}
