package hostinfo

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"runtime"
	"strings"
)

const osReleasePath = "/etc/os-release"

// L'identifiant de machine de systemd, puis celui de dbus sur un système
// sans systemd.
var machineIDPaths = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// L'empreinte est préfixée : elle ne se compare pas à une autre empreinte
// du même identifiant faite par un autre logiciel.
const fingerprintPrefix = "opencloud-host:"

type Info struct {
	Hostname string
	OS       string
	Arch     string
	Address  string
	// Empreinte de l'identifiant de machine, jamais l'identifiant brut ;
	// vide si la machine n'en a pas.
	HostFingerprint string
}

// Collect ne renvoie jamais d'erreur : ce qu'on ne sait pas reste vide.
func Collect() Info {
	hostname, _ := os.Hostname()
	return Info{
		Hostname: hostname,
		OS:       readOSName(osReleasePath),
		Arch:     runtime.GOARCH,
		Address:  primaryAddress(),

		HostFingerprint: HostFingerprint(),
	}
}

// HostFingerprint rend l'empreinte de l'identifiant de cette machine.
func HostFingerprint() string {
	return readHostFingerprint(machineIDPaths)
}

// readHostFingerprint lit le premier identifiant de machine présent et en
// rend l'empreinte ; aucun, rien.
func readHostFingerprint(paths []string) string {
	for _, path := range paths {
		content, err := os.ReadFile(path) // #nosec G304 -- chemins constants.
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(content))
		if id == "" {
			continue
		}
		sum := sha256.Sum256([]byte(fingerprintPrefix + id))
		return hex.EncodeToString(sum[:])
	}
	return ""
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
