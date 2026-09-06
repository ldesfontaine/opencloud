package actiondir

import (
	"path"
	"regexp"
	"strconv"
)

const (
	// Root est le dossier des actions sur toute machine, la machine openCloud
	// comprise : /var/lib/opencloud appartient au compte opencloud.
	Root = "/var/lib/opencloud/actions"

	ScriptName   = "run.sh"     // le script versionné, 0755
	ParamsName   = "params.env" // EnvironmentFile= de l'unité, 0600
	TimeoutName  = "timeout"    // RuntimeMaxSec en secondes, chiffres ASCII, une ligne
	FilesDirName = "files"      // les fichiers rendus par Go, que le script pose

	// LauncherPath est la seule commande que sudoers autorise au compte
	// opencloud ; SystemdRunPath ce que le lanceur exécute, par execve.
	LauncherPath   = "/usr/local/sbin/oc-launch"
	SystemdRunPath = "/usr/bin/systemd-run"

	UnitPrefix = "oc-action-"

	// Bornes du délai maximum d'une action : au moins une seconde, au plus
	// une journée. Le lanceur refuse le reste.
	MinTimeoutSeconds = 1
	MaxTimeoutSeconds = 86400
)

// L'identifiant d'une action : la seule chose variable dans le vecteur de
// lancement, d'où la forme étroite. Même regex dans sudoers et le lanceur.
var idPattern = regexp.MustCompile(`^[0-9a-z-]{1,40}$`)

// ValidID dit si id peut apparaître dans un chemin, un nom d'unité et une
// ligne de commande sans rien y ajouter.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Dir rend le dossier d'une action sur la machine. L'appelant a validé id.
func Dir(id string) string {
	return path.Join(Root, id)
}

// UnitName rend le nom de l'unité transitoire : oc-action-<id>.
func UnitName(id string) string {
	return UnitPrefix + id
}

// ParseTimeout lit le contenu du fichier timeout : des chiffres, une ligne,
// dans les bornes. Tout le reste est refusé.
func ParseTimeout(content string) (int, bool) {
	trimmed := content
	if n := len(trimmed); n > 0 && trimmed[n-1] == '\n' {
		trimmed = trimmed[:n-1]
	}
	if trimmed == "" || len(trimmed) > 5 {
		return 0, false
	}
	for _, c := range trimmed {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.Atoi(trimmed)
	if err != nil || seconds < MinTimeoutSeconds || seconds > MaxTimeoutSeconds {
		return 0, false
	}
	return seconds, true
}
