package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// requireKnownHost ferme la porte au DNS rebinding : une page d'un autre
// site dont le nom pointe soudain sur openCloud parle avec un Host qui
// n'est pas le nôtre. Sont reconnus l'hôte de public_url, localhost, toute
// adresse IP écrite telle quelle, et l'hôte de listen. Derrière un
// mandataire de confiance, l'hôte qu'il transmet est jugé aussi : Traefik
// garde le Host d'origine, ou le remplace par l'adresse de la boucle.
func (s *Server) requireKnownHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hosts := []string{r.Host}
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" && s.trustsPeer(r) {
			hosts = append(hosts, forwarded)
		}
		for _, host := range hosts {
			if !s.knownHost(host) {
				s.logger.Warn("request refused: unknown Host, set public_url to the address used to reach openCloud",
					"host", host, "path", r.URL.Path, "public_url", s.publicURL)
				s.writeAPIError(w, http.StatusForbidden, codeHost)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) knownHost(hostPort string) bool {
	host := hostName(hostPort)
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if host == "localhost" {
		return true
	}
	if s.publicURL != "" && host == hostName(hostOf(s.publicURL)) {
		return true
	}
	return s.listen != "" && host == hostName(s.listen)
}

// hostName retire le port et les crochets d'IPv6, en minuscules : un nom
// de domaine ne dépend pas de la casse.
func hostName(hostPort string) string {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	// Une zone IPv6 (fe80::1%eth0) n'a rien à faire dans un Host.
	if strings.Contains(host, "%") {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
