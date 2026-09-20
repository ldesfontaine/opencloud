package probe

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Address est où une sonde compose : l'hôte à joindre et à vérifier, et le
// port. Pour HTTP, le port vient de l'URL ou du protocole.
type Address struct {
	Host string
	Port string
}

func (a Address) String() string {
	return net.JoinHostPort(a.Host, a.Port)
}

// ParseTarget lit la cible d'une sonde : une URL http ou https, ou un
// « hôte:port » pour TCP.
func ParseTarget(kind Kind, target string) (Address, error) {
	if kind == KindTCP {
		return parseHostPort(target)
	}
	return parseURL(target)
}

func parseURL(target string) (Address, error) {
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Address{}, ErrTargetInvalid
	}
	host := parsed.Hostname()
	if host == "" {
		return Address{}, ErrTargetInvalid
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
		if parsed.Scheme == "https" {
			port = "443"
		}
	}
	if !isPort(port) {
		return Address{}, ErrTargetInvalid
	}
	return Address{Host: host, Port: port}, nil
}

func parseHostPort(target string) (Address, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || !isPort(port) {
		return Address{}, ErrTargetInvalid
	}
	return Address{Host: host, Port: port}, nil
}

func isPort(port string) bool {
	number, err := strconv.Atoi(port)
	return err == nil && number > 0 && number <= 65535
}

// validateTarget refuse une cible qu'openCloud ne sait pas sonder, et
// celle qui pointe déjà, en clair, vers une adresse interdite. Ce que le
// nom résout se revérifie au moment de composer : voir guardedDial.
func validateTarget(kind Kind, target string) error {
	if target == "" || len(target) > MaxTargetLength || strings.ContainsAny(target, " \t\r\n") {
		return ErrTargetInvalid
	}
	address, err := ParseTarget(kind, target)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(address.Host); ip != nil && forbiddenIP(ip) {
		return ErrTargetForbidden
	}
	return nil
}

// forbiddenIP dit ce qu'une sonde ne doit jamais joindre : le lien-local,
// qui contient l'adresse de métadonnées des hébergeurs (169.254.169.254),
// laquelle livre les identifiants de l'instance à qui sait la demander.
// La boucle locale et les adresses privées restent ouvertes : sonder un
// service par l'agent de sa propre machine est l'usage voulu.
func forbiddenIP(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast()
}
