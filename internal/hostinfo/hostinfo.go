package hostinfo

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const osReleasePath = "/etc/os-release"

// L'identifiant de machine de systemd, puis celui de dbus sur un système
// sans systemd.
var machineIDPaths = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// L'identifiant du démarrage en cours, tiré par le noyau. Une image clonée
// (modèle de VPS, clone Proxmox) garde le machine-id de son modèle ; deux
// noyaux distincts n'ont jamais le même boot_id.
const bootIDPath = "/proc/sys/kernel/random/boot_id"

// L'empreinte est préfixée : elle ne se compare pas à une autre empreinte
// du même identifiant faite par un autre logiciel.
const fingerprintPrefix = brand.HostFingerprintPrefix

type Info struct {
	Hostname string
	OS       string
	Arch     string
	Address  string
	// Empreinte de l'identifiant de machine et du démarrage, jamais les
	// identifiants bruts ; vide s'il en manque un.
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

// HostFingerprint rend l'empreinte de cette machine pour ce démarrage : le
// machine-id seul confondrait deux clones d'une même image.
func HostFingerprint() string {
	return readHostFingerprint(machineIDPaths, bootIDPath)
}

// readHostFingerprint lit le premier identifiant de machine présent et
// l'identifiant de démarrage, et rend l'empreinte des deux ; s'il en manque
// un, rien : mieux vaut ne pas reconnaître la machine openCloud que prendre
// une autre machine pour elle.
func readHostFingerprint(machineIDPaths []string, bootIDPath string) string {
	machineID := readFirstIdentifier(machineIDPaths)
	bootID := readFirstIdentifier([]string{bootIDPath})
	if machineID == "" || bootID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fingerprintPrefix + machineID + "\n" + bootID))
	return hex.EncodeToString(sum[:])
}

func readFirstIdentifier(paths []string) string {
	for _, path := range paths {
		content, err := os.ReadFile(path) // #nosec G304 -- chemins constants.
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(content)); id != "" {
			return id
		}
	}
	return ""
}

// OSName rend le système de cette machine, tel qu'os-release le nomme
// maintenant : une montée de version le change, et l'agent le redit à
// chaque connexion.
func OSName() string {
	return readOSName(osReleasePath)
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
