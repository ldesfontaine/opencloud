package hostinfo

import (
	"bufio"
	"net"
	"os"
	"runtime"
	"strings"
)

const osReleasePath = "/etc/os-release"

type Info struct {
	Hostname string
	OS       string
	Arch     string
	Address  string
}

// Collect ne renvoie jamais d'erreur : ce qu'on ne sait pas reste vide.
func Collect() Info {
	hostname, _ := os.Hostname()
	return Info{
		Hostname: hostname,
		OS:       readOSName(osReleasePath),
		Arch:     runtime.GOARCH,
		Address:  primaryAddress(),
	}
}

// readOSName compose « Debian 12 » depuis NAME et VERSION_ID d'os-release,
// et retombe sur le nom du système de Go.
func readOSName(path string) string {
	file, err := os.Open(path) // #nosec G304 -- chemin constant.
	if err != nil {
		return runtime.GOOS
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = strings.Trim(value, `"`)
		}
	}
	name := values["NAME"]
	if name == "" {
		return runtime.GOOS
	}
	if version := values["VERSION_ID"]; version != "" {
		return name + " " + version
	}
	return name
}

// primaryAddress prend la première adresse IPv4 routable d'une interface
// active, sinon une IPv6, sinon rien.
func primaryAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || !ipNet.IP.IsGlobalUnicast() {
				continue
			}
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
			if fallback == "" {
				fallback = ipNet.IP.String()
			}
		}
	}
	return fallback
}
