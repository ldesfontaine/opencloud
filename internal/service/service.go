package service

import (
	"errors"
	"time"
)

// Kind est le genre d'un service ; un seul aujourd'hui.
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

const (
	// L'agent mesure chaque conteneur toutes les 30 s : trois fois moins
	// que la machine, une jauge qui bouge toutes les 30 s suffit.
	SampleInterval = 30 * time.Second
	// L'agent redonne l'inventaire complet à ce rythme, en plus de la
	// connexion : ce qu'un événement manqué aurait laissé faux se corrige.
	InventoryInterval = 5 * time.Minute
	// Passé ce délai sans échantillon, la mesure d'un service est
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
	MaxNetworksPerMachine   = 256
	MaxNetworksPerContainer = 32
	MaxAliasesPerNetwork    = 16
	MaxDependencies         = 64
	// L'extrait de journal capturé sur un arrêt anormal : les dernières
	// lignes, bornées en nombre et en taille.
	SnippetLines    = 50
	MaxSnippetBytes = 10 << 10
	maxFutureSkew   = 5 * time.Minute

	// Rétentions : les transitions d'un service vivant, la fiche d'un
	// conteneur détruit (ses transitions partent avec elle), le brut des
	// mesures. Pas d'agrégat par service : l'historique long attendra.
	TransitionRetention = 90 * 24 * time.Hour
	ArchivedRetention   = 30 * 24 * time.Hour
	SampleRetention     = 48 * time.Hour
)

// UpdatePolicy est ce que l'opérateur veut des mises à jour d'un
// service : les suivre, les taire (épinglé : vérifié, jamais montré), ou
// ne jamais interroger le registre (exclu).
type UpdatePolicy string

const (
	PolicyFollow   UpdatePolicy = ""
	PolicyPinned   UpdatePolicy = "pinned"
	PolicyExcluded UpdatePolicy = "excluded"
)

func IsUpdatePolicy(policy UpdatePolicy) bool {
	return policy == PolicyFollow || policy == PolicyPinned || policy == PolicyExcluded
}

// Service est une fiche telle que la base la connaît. Les dates zéro
// disent « jamais » ; ArchivedAt non nul dit que le conteneur est détruit.
type Service struct {
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
	UpdatePolicy   UpdatePolicy
	State          State
	ExitCode       int
	Health         Health
	RestartCount   int
	Ports          []Port
	// Le mode réseau tel que Docker le dit : bridge, host, none,
	// container:<id>, ou le nom du premier réseau joint.
	NetworkMode string
	Privileged  bool
	Networks    []Attachment
	DependsOn   []Dependency
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	ArchivedAt  time.Time
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

// Attachment est la présence d'un service sur un réseau de sa machine ;
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
func (s Service) IsArchived() bool {
	return !s.ArchivedAt.IsZero()
}

// Transition est un changement d'état ou de santé, daté par Docker. Sur un
// arrêt anormal, Snippet garde les dernières lignes du journal.
type Transition struct {
	ID             int64
	ServiceID      string
	At             time.Time
	Action         string
	PreviousState  State
	NewState       State
	PreviousHealth Health
	NewHealth      Health
	ExitCode       *int
	Replayed       bool
	Snippet        string
}

// Sample est une mesure d'un service : processeur en pourcentage d'un
// cœur (200 % = deux cœurs), mémoire en octets, limite en octets.
type Sample struct {
	ServiceID  string
	SampledAt  time.Time
	CPUPercent float64
	MemUsed    int64
	MemLimit   int64
}

// Current est la mesure courante d'un service, et si elle est assez
// fraîche pour être montrée.
type Current struct {
	ServiceID string
	Available bool
	Sample    *Sample
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
	ErrNotFound       = errors.New("service not found")
	ErrReportInvalid  = errors.New("service report out of range")
	ErrMachineOffline = errors.New("machine offline")
	ErrLogsBusy       = errors.New("too many log follows")
	ErrLogsTimeout    = errors.New("logs did not arrive in time")
	ErrLogsFailed     = errors.New("logs unavailable")
)
