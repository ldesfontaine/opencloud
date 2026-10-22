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
