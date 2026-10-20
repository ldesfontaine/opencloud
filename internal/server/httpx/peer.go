package httpx

import (
	"net"
	"net/http"
)

// PeerHost rend l'adresse du pair TCP, sans port ni mandataire. La règle des
// mandataires de confiance vit à la racine server.
func PeerHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
