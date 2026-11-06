package agentwire

import "regexp"

// DockerParams sont les paramètres de docker.deploy et de docker.update.
// L'ordre ne porte que le nom du projet : l'agent calcule lui-même le dossier
// (<racine>/<projet>), il n'écrit jamais ailleurs. Le compose.yml est celui
// que le serveur a généré de l'annuaire ; l'agent le rejuge (CheckCompose)
// puis l'écrit tel quel.
type DockerParams struct {
	ServiceID string `json:"service_id"`
	Project   string `json:"project"`
	Compose   string `json:"compose"`
	// Les noms des unités qui montent un dossier de données : l'agent crée
	// data/<nom> pour chacun ; CheckCompose vérifie que ce sont ceux du
	// fichier.
	DataDirs []string `json:"data_dirs,omitempty"`
	// Vrai quand un .env part dans le secret de l'ordre : le run le dit, la
	// valeur ne s'écrit nulle part.
	EnvSent bool `json:"env_sent"`
	// Présent seulement sur docker.update d'une montée de version : l'agent
	// juge la santé du service et revient seul à la version d'avant si elle
	// ne vient pas. Absent : le redéploiement d'avant, sans filet.
	Rollback *ServiceRollback `json:"rollback,omitempty"`
}

// ServiceRollback règle le filet d'une montée de version, en secondes.
// L'agent rejuge les bornes : un serveur compromis ne le fait pas attendre
// des heures ni juger sain un service à peine lancé.
type ServiceRollback struct {
	HealthWindowSeconds int `json:"health_window_seconds"`
	StableSeconds       int `json:"stable_seconds"`
}

// Les bornes et les défauts du filet (décisions du 06/11/2026) : cinq minutes
// pour devenir sain, deux minutes de marche sans redémarrage quand le
// conteneur n'a pas de HEALTHCHECK.
const (
	MinHealthWindowSeconds     = 2 * 60
	MaxHealthWindowSeconds     = 30 * 60
	DefaultHealthWindowSeconds = 5 * 60
	MinStableSeconds           = 60
	MaxStableSeconds           = 10 * 60
	DefaultStableSeconds       = 2 * 60
)

// DefaultServiceRollback est le filet que le serveur pose tant que la
// politique n'est pas réglable.
func DefaultServiceRollback() ServiceRollback {
	return ServiceRollback{HealthWindowSeconds: DefaultHealthWindowSeconds, StableSeconds: DefaultStableSeconds}
}

// IsValid : la fenêtre et la stabilité dans leurs bornes, la stabilité
// tenant dans la fenêtre.
func (r ServiceRollback) IsValid() bool {
	return r.HealthWindowSeconds >= MinHealthWindowSeconds && r.HealthWindowSeconds <= MaxHealthWindowSeconds &&
		r.StableSeconds >= MinStableSeconds && r.StableSeconds <= MaxStableSeconds &&
		r.StableSeconds < r.HealthWindowSeconds
}

// DockerRollbackParams sont les paramètres de docker.rollback : le projet,
// rien d'autre. L'agent remet la version d'avant qu'il a lui-même gardée ;
// l'ordre ne nomme ni image ni fichier.
type DockerRollbackParams struct {
	Project string `json:"project"`
}

// DockerSecret est le secret de docker.deploy et de docker.update, signé avec
// l'ordre et jamais gardé : le contenu du .env.
type DockerSecret struct {
	Env string `json:"env"`
}

// MaxComposeBytes borne le compose.yml généré : il tient dans un ordre.
// MaxDataDirs borne les dossiers de données d'un ordre : une unité au plus
// chacun, et un service n'a pas plus de 32 unités (directory.MaxUnits).
const (
	MaxComposeBytes = 64 << 10
	MaxDataDirs     = 32
)

// Les noms que l'agent rejuge avant d'écrire : ce sont les règles de
// l'annuaire (le projet Compose et le nom d'une unité), recopiées ici parce
// que l'agent ne se fie pas à ce que l'ordre dit. Un test du composant deploy
// vérifie qu'elles s'accordent.
var (
	composeProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	dataDirNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
)

// IsComposeProject : un nom de projet qui sert de nom de dossier.
func IsComposeProject(name string) bool {
	return composeProjectPattern.MatchString(name)
}

// IsDataDirName : un nom d'unité, qui sert de nom de dossier sous data/.
func IsDataDirName(name string) bool {
	return dataDirNamePattern.MatchString(name)
}
