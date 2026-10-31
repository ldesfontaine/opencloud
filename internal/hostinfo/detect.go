package hostinfo

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	netClassDir = "sys/class/net"
	virtTimeout = 5 * time.Second
	virtCommand = "systemd-detect-virt"
)

// Les cartes que la machine fabrique pour ses conteneurs et ses ponts : elles
// ne disent rien de l'endroit où elle tourne, et leur adresse change à chaque
// conteneur.
var virtualInterfacePrefixes = []string{"lo", "docker", "veth", "br-", "virbr", "tap", "fwbr", "fwpr", "fwln", "cni", "flannel", "cali", "tun"}

// Read relève la machine : le type d'après systemd-detect-virt, puis ce que le
// noyau expose sous /sys et /proc. Ce qu'il ne peut pas lire, il ne le déclare pas.
func Read(ctx context.Context) Report {
	return Detect(os.DirFS("/"), detectVirt(ctx))
}

// Detect lit les cartes réseau et les disques depuis la racine donnée
// (os.DirFS("/") en production, un faux dans les tests).
func Detect(root fs.FS, virt string) Report {
	return Report{
		Virt:       virt,
		Interfaces: readInterfaces(root),
		Storage:    readStorage(root),
	}
}

// detectVirt rend la réponse de systemd-detect-virt. La commande sort en 1 en
// disant « none » sur une machine physique : seule une sortie vide compte pour
// une commande absente. Hors systemd (Alpine, un conteneur), le type reste
// inconnu plutôt que deviné.
func detectVirt(ctx context.Context) string {
	runCtx, cancel := context.WithTimeout(ctx, virtTimeout)
	defer cancel()
	output, _ := exec.CommandContext(runCtx, virtCommand).Output()
	virt := strings.TrimSpace(string(output))
	if !virtPattern.MatchString(virt) {
		return ""
	}
	return virt
}

// readInterfaces liste les cartes qui portent une vraie adresse MAC, par nom.
func readInterfaces(root fs.FS) []Interface {
	interfaces := []Interface{}
	entries, err := fs.ReadDir(root, netClassDir)
	if err != nil {
		return interfaces
	}
	for _, entry := range entries {
		name := entry.Name()
		if isVirtualInterface(name) || len(interfaces) == MaxInterfaces {
			continue
		}
		mac := strings.ToLower(readText(root, netClassDir+"/"+name+"/address"))
		// Une carte sans adresse (tunnel, loopback) se lit en zéros.
		if !IsMAC(mac) || mac == "00:00:00:00:00:00" || !namePattern.MatchString(name) {
			continue
		}
		interfaces = append(interfaces, Interface{Name: name, MAC: mac})
	}
	return interfaces
}

func isVirtualInterface(name string) bool {
	for _, prefix := range virtualInterfacePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// readText lit un petit fichier du noyau, sans l'espace de fin ; vide s'il
// manque. Le texte vient du matériel : il passe par les contrôles du rapport.
func readText(root fs.FS, path string) string {
	content, err := fs.ReadFile(root, path)
	if err != nil {
		return ""
	}
	return strings.TrimFunc(string(content), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func readNumber(root fs.FS, path string) (int64, bool) {
	value, err := strconv.ParseInt(readText(root, path), 10, 64)
	return value, err == nil
}

// sortedNames rend les noms d'un dossier du noyau, en ordre stable.
func sortedNames(root fs.FS, dir string) []string {
	entries, err := fs.ReadDir(root, dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}
