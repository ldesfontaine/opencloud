package lifecycle

import "strings"

// Le nom qu'un système se donne (NAME d'os-release) → son produit chez la
// source. La source ne publie pas cette correspondance : c'est la seule
// table tenue ici, courte, et un système absent reste « support inconnu »
// plutôt que deviné. Proxmox VE se présente comme Debian : c'est le
// support de sa base Debian qui est dit.
var systemProducts = map[string]string{
	"debian gnu/linux":                "debian",
	"ubuntu":                          "ubuntu",
	"alpine linux":                    "alpine-linux",
	"rocky linux":                     "rocky-linux",
	"almalinux":                       "almalinux",
	"fedora linux":                    "fedora",
	"fedora":                          "fedora",
	"centos linux":                    "centos",
	"centos stream":                   "centos-stream",
	"red hat enterprise linux":        "rhel",
	"red hat enterprise linux server": "rhel",
	"oracle linux server":             "oracle-linux",
	"amazon linux":                    "amazon-linux",
	"opensuse leap":                   "opensuse",
	"sles":                            "sles",
	"linux mint":                      "linuxmint",
}

// systemOf lit « Debian GNU/Linux 12 », ce que l'agent dit de sa machine :
// le nom, puis la version en dernier mot. Sans version (« Arch Linux »),
// le nom entier est essayé : la branche restera inconnue.
func systemOf(system string) (product, version string, known bool) {
	name, last := system, ""
	if cut := strings.LastIndex(system, " "); cut >= 0 && versionParts(system[cut+1:]) != nil {
		name, last = system[:cut], system[cut+1:]
	}
	product, known = systemProducts[strings.ToLower(strings.TrimSpace(name))]
	if !known {
		return "", "", false
	}
	return product, last, true
}
