package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
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
// celle que transmet un mandataire de confiance.
func (s *Server) clientAddress(r *http.Request) string {
	if s.trustsPeer(r) {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Un en-tête X-Forwarded-* n'est cru que d'un mandataire déclaré.
func (s *Server) trustsPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(peer.Unmap()) {
			return true
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
