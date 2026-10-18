package manifest

import (
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

const (
	// Version du format, première ligne du fichier. Un format qui change
	// d'une façon incompatible prend le numéro suivant.
	Version = 1
	// MaxFileBytes borne ce qu'on lit : un annuaire de quelques centaines de
	// services tient dans une fraction de cela. Le corps JSON d'une requête
	// reste sous 1 Mio, échappement compris.
	MaxFileBytes = 256 << 10

	// SecretKept est la seule forme admise d'un secret : la valeur reste
	// celle que le serveur garde, le fichier ne la dit pas.
	SecretKept = "kept"
)

// File est le contenu d'un fichier, dans le vocabulaire du fichier : des
// noms et des références « machine/nom », jamais les identifiants internes
// d'une machine. Line vaut 0 pour ce que l'export construit ; la lecture y
// met la ligne de l'entrée, pour que les erreurs la citent.
type File struct {
	Machines   []Machine
	Projects   []Project
	Services   []Service
	Probes     []Probe
	Heartbeats []Heartbeat
	Ignored    []IgnoredKey
}

// Machine est lue, jamais créée ni supprimée : le fichier nomme celles
// auxquelles ses références ont droit.
type Machine struct {
	Name string
	Line int
}

type Project struct {
	ID    string
	Name  string
	Title string
	Line  int
}

// Service est le service déclaré. NeedsReview et Proposed ne viennent que de
// l'export, qui en fait un commentaire : la lecture ne les pose jamais, l'état
// n'est pas dans le fichier.
type Service struct {
	ID             string
	Machine        string
	Name           string
	Title          string
	Nature         directory.Nature
	Origin         directory.Origin
	ComposeProject string
	PublicURL      string
	Provides       []directory.Capability
	// Les noms des projets du service.
	Projects    []string
	Units       []Unit
	NeedsReview bool
	// Proposé par l'export depuis des conteneurs non déclarés, pas encore
	// déclaré.
	Proposed bool
	Line     int
}

// Unit n'a pas d'identifiant dans le fichier : le modèle n'en écrit pas.
type Unit struct {
	Name          string
	Image         string
	Version       string
	Digest        string
	ContainerName string
	SystemdUnit   string
	Ports         []string
	// Vide pour un binaire, qui n'a que « watch ».
	Updates container.UpdatePolicy
	// Proposée par l'export à un service déjà déclaré : elle vient d'un
	// conteneur non déclaré. Même règle que Service.Proposed.
	Proposed bool
	Line     int
}

// Probe : Runner vide veut dire la machine openCloud. Service s'écrit
// « machine/nom », vide quand la sonde ne vise aucun service.
type Probe struct {
	ID                string
	Name              string
	Runner            string
	Service           string
	Kind              probe.Kind
	Target            string
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	Method            string
	ExpectedStatus    string
	ExpectedBody      string
	FollowRedirects   bool
	TLS               bool
	Line              int
}

// Heartbeat est une tâche. Son jeton n'est pas ici : le fichier écrit
// « secret: kept » et le serveur garde le sien.
type Heartbeat struct {
	ID      string
	Name    string
	Machine string
	Every   time.Duration
	Grace   time.Duration
	Line    int
}

// IgnoredKey désigne un projet Compose ou un conteneur que la machine
// montre sans qu'il y ait rien à déclarer.
type IgnoredKey struct {
	Machine string
	Kind    directory.IgnoreKind
	Value   string
	Line    int
}
