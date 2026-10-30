package role

import (
	"bufio"
	"io"
	"io/fs"
	"strings"
)

// Les signes d'un hôte Proxmox VE. Les deux se lisent sans droit et ne se
// forgent pas sans root : la base de dpkg et la table des montages
// appartiennent au système.
//
//   - pve-manager est installé : c'est le paquet qui fait un Proxmox, et le
//     seul signe qui donne la version ;
//   - /etc/pve est le montage FUSE de pmxcfs : un Proxmox dont le service
//     du cluster tourne.
//
// Les deux sont exigés. Le coût : pendant que pve-cluster est arrêté (au
// démarrage, ou à la main), le rôle n'est pas vu ; le prochain relevé le
// retrouve. Une VM ou un conteneur d'un hôte n'a ni l'un ni l'autre.
const (
	dpkgStatusPath = "var/lib/dpkg/status"
	mountInfoPath  = "proc/self/mountinfo"
	proxmoxPackage = "pve-manager"
	proxmoxMount   = "/etc/pve"
	// Un status de dpkg pèse quelques Mio ; au-delà de la borne, il est
	// jugé illisible plutôt que lu en entier.
	maxStatusBytes = 64 << 20
	maxMountBytes  = 4 << 20
	maxLineBytes   = 1 << 20
)

// Detect rend ce que la machine est, vue par root (la racine du système de
// fichiers : os.DirFS("/") en production, un faux dans les tests). Ce qu'il
// ne peut pas lire, il ne le déclare pas.
func Detect(root fs.FS) Report {
	report := Report{Roles: []Role{}}
	if version, found := findProxmoxVersion(root); found {
		report.Roles = append(report.Roles, Role{Name: ProxmoxVE, Version: version})
	}
	return report
}

func findProxmoxVersion(root fs.FS) (string, bool) {
	version, installed := readInstalledVersion(root, proxmoxPackage)
	if !installed || !hasProxmoxMount(root) {
		return "", false
	}
	if !IsVersion(version) {
		version = ""
	}
	return version, true
}

// readInstalledVersion cherche, dans le status de dpkg, le paquet installé et
// sa version. Un paquet seulement retiré ou configuré ne compte pas.
func readInstalledVersion(root fs.FS, packageName string) (string, bool) {
	file, err := root.Open(dpkgStatusPath)
	if err != nil {
		return "", false
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxStatusBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	var name, status, version string
	matches := func() bool {
		return name == packageName && strings.HasSuffix(status, " installed") && !strings.Contains(status, "not-installed")
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if matches() {
				return version, true
			}
			name, status, version = "", "", ""
			continue
		}
		// Les lignes de suite d'un champ commencent par une espace.
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		switch field {
		case "Package":
			name = value
		case "Status":
			status = value
		case "Version":
			version = value
		}
	}
	if matches() {
		return version, true
	}
	return "", false
}

// hasProxmoxMount cherche /etc/pve parmi les montages, de type fuse. Le
// point de montage est échappé dans mountinfo (\040 pour une espace) : celui
// qu'on cherche n'en porte aucune.
func hasProxmoxMount(root fs.FS) bool {
	file, err := root.Open(mountInfoPath)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxMountBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for scanner.Scan() {
		before, after, found := strings.Cut(scanner.Text(), " - ")
		if !found {
			continue
		}
		fields := strings.Fields(before)
		filesystem := strings.Fields(after)
		if len(fields) < 5 || len(filesystem) < 1 {
			continue
		}
		if fields[4] == proxmoxMount && strings.HasPrefix(filesystem[0], "fuse") {
			return true
		}
	}
	return false
}
