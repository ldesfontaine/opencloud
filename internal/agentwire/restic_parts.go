package agentwire

import (
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Les parties d'une sauvegarde de service : son dossier Compose, un volume
// nommé de Docker, la sortie d'un outil de dump d'une base. Chacune devient
// son propre instantané restic, sous un nom stable.
const (
	ResticPartFolder   = "folder"
	ResticPartVolume   = "volume"
	ResticPartDatabase = "database"
)

// Le résultat d'une partie : ok, échouée (les autres parties partent
// quand même), ou ignorée (rien à sauvegarder là : conteneur ou volume absent).
const (
	ResticPartOK      = "ok"
	ResticPartFailed  = "failed"
	ResticPartSkipped = "skipped"
)

// Les moteurs de base dont l'agent sait lancer le dump dans le conteneur.
const (
	EnginePostgres = "postgres"
	EngineMariaDB  = "mariadb"
	EngineMySQL    = "mysql"
	EngineMongo    = "mongo"
)

// DatabaseEngines rend la liste fermée des moteurs pris en charge.
func DatabaseEngines() []string {
	return []string{EnginePostgres, EngineMariaDB, EngineMySQL, EngineMongo}
}

var (
	errResticPart   = errors.New("restic: backup part invalid")
	dockerNameRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// IsDockerName dit si un nom peut être celui d'un conteneur ou d'un volume
// Docker : jamais de « / », d'espace ni de « : » (il resterait un seul
// argument, sans interprétation).
func IsDockerName(name string) bool {
	return dockerNameRegex.MatchString(name)
}

// DatabaseEngine rend le moteur que l'image d'un conteneur désigne, vide si
// ce n'est pas une base prise en charge. Seul le nom de l'image compte (le
// dépôt et l'étiquette de version n'y changent rien) : « docker.io/library/
// postgres:16 » et « postgres » sont le même moteur. Une image dont le nom
// ne finit pas par un moteur (« my-postgres-ui ») n'en est pas un.
func DatabaseEngine(image string) string {
	name := strings.ToLower(image)
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	name = path.Base(name)
	if colon := strings.Index(name, ":"); colon >= 0 {
		name = name[:colon]
	}
	switch name {
	case "postgres", "postgresql", "postgis", "timescaledb", "pgvector":
		return EnginePostgres
	case "mariadb":
		return EngineMariaDB
	case "mysql":
		return EngineMySQL
	case "mongo", "mongodb":
		return EngineMongo
	}
	return ""
}

// ResticPartReport est ce qu'une partie d'un service a donné. Name est le nom
// du volume ou du conteneur, jamais un chemin ni un texte de l'agent ;
// Reason, un mot de la liste fermée des raisons (vide si ok).
type ResticPartReport struct {
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	BytesAdded int64  `json:"bytes_added"`
}

func (p ResticPartReport) Validate() error {
	if !slices.Contains([]string{ResticPartFolder, ResticPartVolume, ResticPartDatabase}, p.Kind) {
		return errResticPart
	}
	if !slices.Contains([]string{ResticPartOK, ResticPartFailed, ResticPartSkipped}, p.State) {
		return errResticPart
	}
	if p.Kind == ResticPartFolder {
		if p.Name != "" {
			return errResticPart
		}
	} else if !IsDockerName(p.Name) {
		return errResticPart
	}
	if p.Reason != "" && !slices.Contains(RefusalReasons(), p.Reason) {
		return errResticPart
	}
	if p.BytesAdded < 0 {
		return errResticNegative
	}
	return nil
}
