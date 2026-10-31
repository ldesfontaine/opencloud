package container

import (
	"errors"
	"slices"
	"time"
)

// Kind est le genre d'un conteneur ; un seul aujourd'hui.
type Kind string

const KindContainer Kind = "container"

// State est l'état brut que Docker donne ; le front en fait une pastille.
type State string

const (
	StateCreated    State = "created"
	StateRunning    State = "running"
	StatePaused     State = "paused"
	StateRestarting State = "restarting"
	StateRemoving   State = "removing"
	StateExited     State = "exited"
	StateDead       State = "dead"
)

// Health est ce que le HEALTHCHECK du conteneur dit ; vide sans HEALTHCHECK.
type Health string

const (
	HealthStarting  Health = "starting"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// Healths liste ce qu'un HEALTHCHECK peut dire ; le vide, pas de
// HEALTHCHECK, n'est pas une santé.
func Healths() []Health {
	return []Health{HealthStarting, HealthHealthy, HealthUnhealthy}
}

const (
	// L'agent mesure chaque conteneur toutes les 30 s : trois fois moins
	// que la machine, une jauge qui bouge toutes les 30 s suffit.
	SampleInterval = 30 * time.Second
	// L'agent redonne l'inventaire complet à ce rythme, en plus de la
	// connexion : ce qu'un événement manqué aurait laissé faux se corrige.
	InventoryInterval = 5 * time.Minute
	// Passé ce délai sans échantillon, la mesure d'un conteneur est
	// « indisponible », jamais à zéro.
	StaleAfter = 90 * time.Second

	// Ce qu'un signal peut porter : des bornes larges pour une machine
	// chargée, assez serrées pour qu'un agent qui déraille soit refusé.
	MaxContainersPerReport = 256
	MaxEventsPerReport     = 512
	MaxStatsPerReport      = 1024
	MaxPortsPerContainer   = 64
	MaxNameLength          = 255
	MaxImageLength         = 512
	MaxPathLength          = 4096
	// Les réseaux d'une machine et ceux qu'un conteneur joint ; les alias
	// et les dépendances déclarées d'un conteneur.
	MaxVolumesPerMachine    = 512
	MaxVolumeUsers          = 64
	MaxNetworksPerMachine   = 256
	MaxNetworksPerContainer = 32
	MaxAliasesPerNetwork    = 16
	MaxDependencies         = 64
	// Les labels <préfixe>.* d'un conteneur : une carte courte, dont une
	// valeur va jusqu'à MaxNameLength. Deux seulement sont lus
	// (LabelService, LabelUnit) ; la carte entière part pour qu'un serveur
	// futur lise un autre suffixe sans mettre les agents à jour.
	MaxLabelsPerContainer = 16
	MaxLabelKeyLength     = 63
	// L'extrait de journal capturé sur un arrêt anormal : les dernières
	// lignes, bornées en nombre et en taille.
	SnippetLines    = 50
	MaxSnippetBytes = 10 << 10
	maxFutureSkew   = 5 * time.Minute

	// Rétentions : les transitions d'un conteneur vivant, la fiche d'un
	// conteneur détruit (ses transitions partent avec elle), le brut des
	// mesures. Pas d'agrégat par conteneur : l'historique long attendra.
	TransitionRetention = 90 * 24 * time.Hour
	ArchivedRetention   = 30 * 24 * time.Hour
	SampleRetention     = 48 * time.Hour

	// Ce qu'une machine garde au plus, quoi que son agent envoie : la
	// rétention borne la durée, ces plafonds bornent le volume, sans quoi un
	// agent qui ment remplit le disque que toutes les machines se partagent.
	//
	// Vivants : un inventaire complet n'en porte pas plus de
	// MaxContainersPerReport ; la marge couvre ceux que des événements
	// ajoutent entre deux inventaires. Un événement qui décrit une fiche
	// neuve au-delà est écarté, l'inventaire suivant dira la vérité.
	MaxLiveContainersPerMachine = 2 * MaxContainersPerReport
	// Archivés : un hôte qui redéploie une cinquantaine de services par jour
	// en garde moins de 1 500 sur ArchivedRetention. Au-delà, les plus
	// anciennes fiches archivées partent avant leur échéance.
	MaxArchivedPerMachine = 2000
	// Transitions, tous conteneurs confondus : un plafond par conteneur ne
	// borne rien tant que le nombre de conteneurs ne l'est pas. 20 000 lignes
	// font plus de 200 par jour sur TransitionRetention ; au pire, avec des
	// extraits pleins, environ 200 Mio par machine.
	MaxTransitionsPerMachine = 20000
	// Mesures : la clé en permet une par conteneur et par seconde, 170 000
	// lignes par conteneur sur SampleRetention. L'agent mesure toutes les
	// 30 s ; une mesure à moins de MinSampleSpacing d'une autre du même
	// conteneur est écartée, et un agent qui en envoie plus ne stocke rien
	// de plus.
	MinSampleSpacing = SampleInterval / 2
)

// Les deux suffixes de label qu'openCloud lit, après le préfixe de la
// marque. Des suffixes, jamais le nom du produit.
const (
	LabelService = "service"
	LabelUnit    = "unit"
	// LabelInvalid est la clé réservée que l'agent ajoute quand il a dû écarter
	// des labels de la marque et qu'il ne reste pas la paire service et unité : le
	// conteneur est étiqueté, mais mal. La valeur dit combien de clés sont
	// parties. Un label de ce nom écrit par l'image n'est jamais lu comme tel.
	LabelInvalid = "invalid"
)

// UpdatePolicy est ce que l'opérateur veut des mises à jour d'une unité de
// l'annuaire : les suivre, les taire (hold : vérifiées, jamais montrées), ou
// ne jamais interroger le registre (ignore). Elle se règle sur l'unité, pas
// sur le conteneur : un conteneur recréé retrouve celle de son unité.
type UpdatePolicy string

const (
	PolicyWatch  UpdatePolicy = "watch"
	PolicyHold   UpdatePolicy = "hold"
	PolicyIgnore UpdatePolicy = "ignore"
)

func UpdatePolicies() []UpdatePolicy {
	return []UpdatePolicy{PolicyWatch, PolicyHold, PolicyIgnore}
}

func IsUpdatePolicy(policy UpdatePolicy) bool {
	return slices.Contains(UpdatePolicies(), policy)
}

// MatchLevel dit par quoi un conteneur a été rattaché à une unité de
// l'annuaire. Le niveau « label » tient aux deux labels que l'opérateur a
// posés sur le conteneur : il désigne l'unité par son identifiant, et il
// survit à un renommage comme à une recréation. Le niveau « compose » tient
// au projet et au service Compose, que le conteneur porte même recréé ; le
// niveau « name » ne tient qu'au nom du conteneur, qu'un renommage perd : il
// est dit faible.
type MatchLevel string

const (
	MatchNone    MatchLevel = ""
	MatchLabel   MatchLevel = "label"
	MatchCompose MatchLevel = "compose"
	MatchName    MatchLevel = "name"
)

// MatchLevels liste les niveaux du plus fort au plus faible.
func MatchLevels() []MatchLevel {
	return []MatchLevel{MatchLabel, MatchCompose, MatchName}
}

// IsWeak dit si le rattachement ne tient qu'au nom du conteneur : un label,
// qui désigne l'unité par son identifiant, n'est jamais faible.
func (l MatchLevel) IsWeak() bool {
	return l == MatchName
}

// Container est une fiche telle que la base la connaît. Les dates zéro
// disent « jamais » ; ArchivedAt non nul dit que le conteneur est détruit.
type Container struct {
	ID          string
	MachineID   string
	Kind        Kind
	Name        string
	Group       string
	ContainerID string
	Image       string
	ImageID     string
	// Ce que Compose dit du service : son nom dans le projet, le dossier
	// et le fichier du projet. Vides hors Compose.
	ComposeService string
	ComposeDir     string
	ComposeFile    string
	// La politique de son unité, lue à chaque lecture ; watch sans unité.
	// L'inventaire ne l'écrit jamais.
	UpdatePolicy UpdatePolicy
	State        State
	ExitCode     int
	OOMKilled    bool
	// Tué en SIGKILL sans arrêt demandé : docker kill, un kill -9.
	Killed       bool
	Health       Health
	RestartCount int
	Ports        []Port
	// Le mode réseau tel que Docker le dit : bridge, host, none,
	// container:<id>, ou le nom du premier réseau joint.
	NetworkMode string
	Privileged  bool
	Networks    []Attachment
	DependsOn   []Dependency
	// Les labels que le conteneur porte et qu'openCloud lit. Ils désignent
	// une unité ; ils ne déclarent rien.
	Labels      Labels
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	ArchivedAt  time.Time
	// L'unité de l'annuaire à laquelle la fiche se rattache, et par quoi.
	// Vides : non déclaré ; une fiche archivée garde son dernier
	// rattachement.
	UnitID     string
	MatchLevel MatchLevel
	// Le service de l'annuaire qui porte l'unité de la fiche ; vide : non
	// déclaré.
	DeclaredServiceID string
}

// Labels sont les labels <préfixe>.* que le conteneur porte et qu'openCloud
// lit : l'identifiant du service déclaré et le nom de son unité. Ils
// désignent une unité ; ils ne déclarent rien.
type Labels struct {
	Service string
	Unit    string
	// Invalid : des labels de la marque sont là mais mal formés (hors bornes,
	// clé en majuscules, valeur illisible), et l'agent n'a rien pu en garder.
	// Le conteneur est étiqueté quand même : sans cela, un label mal écrit
	// retomberait en silence sur Compose ou sur le nom.
	Invalid bool
}

// IsEmpty dit que le conteneur ne porte aucun label de la marque, bon ou
// mauvais.
func (l Labels) IsEmpty() bool {
	return l.Service == "" && l.Unit == "" && !l.Invalid
}

// Port est un port publié sur l'hôte. Une IP vide, 0.0.0.0 ou :: dit
// toutes les interfaces.
type Port struct {
	IP            string `json:"ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

// IsPublic dit si le port écoute sur toutes les interfaces de la machine.
func (p Port) IsPublic() bool {
	return p.IP == "" || p.IP == "0.0.0.0" || p.IP == "::"
}

// Attachment est la présence d'un conteneur sur un réseau de sa machine ;
// l'adresse est vide quand le conteneur est arrêté, l'appartenance reste.
type Attachment struct {
	NetworkID string   `json:"network_id"`
	Name      string   `json:"name"`
	IP        string   `json:"ip"`
	Aliases   []string `json:"aliases"`
}

// Dependency est une dépendance déclarée : par le label Compose, ou par un
// lien hérité de « --link ». Ce qu'un réseau partagé laisse deviner n'en
// est pas une : c'est une joignabilité, et elle se calcule à la lecture.
type Dependency struct {
	Name   string           `json:"name"`
	Source DependencySource `json:"source"`
}

type DependencySource string

const (
	DependencyCompose DependencySource = "compose"
	DependencyLink    DependencySource = "link"
)

// Volume est un volume nommé Docker d'une machine : son nom, son pilote et les
// conteneurs qui le montent. Aucune donnée du volume n'est lue.
type Volume struct {
	MachineID string
	Name      string
	Driver    string
	// Les noms des conteneurs qui le montent, vivants ou arrêtés.
	UsedBy []string
	SeenAt time.Time
}

// Network est un réseau Docker d'une machine. Les réseaux bridge, host et
// none existent sur toute machine : ils ne font jamais un groupe.
type Network struct {
	MachineID string
	NetworkID string
	Name      string
	Driver    string
	Internal  bool
	// Le projet Compose qui l'a créé ; vide sinon.
	Group  string
	SeenAt time.Time
}

// IsUserDefined dit si le réseau a été créé par l'opérateur ou par
// Compose : ce sont ceux qui dessinent un groupe.
func (n Network) IsUserDefined() bool {
	switch n.Name {
	case "bridge", "host", "none":
		return false
	}
	return n.Driver != "host" && n.Driver != "null"
}

// ID dérive l'identifiant d'un conteneur sur une machine : le même
// inventaire rejoué tombe sur la même fiche.
func ID(machineID, containerID string) string {
	return machineID + ":" + containerID
}

// IsArchived dit si le conteneur n'existe plus.
func (s Container) IsArchived() bool {
	return !s.ArchivedAt.IsZero()
}

// Transition est un changement d'état ou de santé, daté par Docker. Sur un
// arrêt anormal, Snippet garde les dernières lignes du journal.
type Transition struct {
	ID             int64
	ContainerID    string
	At             time.Time
	Action         string
	PreviousState  State
	NewState       State
	PreviousHealth Health
	NewHealth      Health
	ExitCode       *int
	OOMKilled      bool
	Killed         bool
	Replayed       bool
	Snippet        string
}

// Sample est une mesure d'un conteneur : processeur en pourcentage d'un
// cœur (200 % = deux cœurs), mémoire en octets, limite en octets.
type Sample struct {
	ContainerID string
	SampledAt   time.Time
	CPUPercent  float64
	MemUsed     int64
	MemLimit    int64
}

// Current est la mesure courante d'un conteneur, et si elle est assez
// fraîche pour être montrée.
type Current struct {
	ContainerID string
	Available   bool
	Sample      *Sample
}

// Engine est ce que la machine dit de son Docker : présent ou non, et
// sinon pourquoi. L'absence n'est pas une erreur.
type Engine struct {
	MachineID  string
	Present    bool
	Reason     EngineReason
	Version    string
	APIVersion string
	CheckedAt  time.Time
}

type EngineReason string

const (
	ReasonNoSocket EngineReason = "no_socket"
	ReasonDenied   EngineReason = "denied"
	ReasonDown     EngineReason = "down"
	ReasonTooOld   EngineReason = "too_old"
)

var (
	ErrNotFound       = errors.New("container not found")
	ErrReportInvalid  = errors.New("container report out of range")
	ErrMachineOffline = errors.New("machine offline")
	ErrLogsBusy       = errors.New("too many log follows")
	ErrLogsTimeout    = errors.New("logs did not arrive in time")
	ErrLogsFailed     = errors.New("logs unavailable")
	// ErrLogRequestUnknown : personne n'attend plus cette requête, ou elle a
	// été poussée à une autre machine. ErrLogBatchTooLarge : le lot sort de
	// ce qu'un agent produit (nombre de lignes, nom du flux).
	ErrLogRequestUnknown = errors.New("log request unknown")
	ErrLogBatchTooLarge  = errors.New("log batch too large")
)

// Ce processus ne tient pas les flux des agents (une commande hors ligne) : la
// machine n'est pas forcément hors ligne, on ne peut simplement pas lui
// parler d'ici.
var ErrLogsUnavailableLocally = errors.New("logs unavailable in this process")
