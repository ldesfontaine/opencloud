package service

import "time"

// Report est ce que l'agent met dans son signal, à côté des lectures de la
// machine. Complete dit que Inventory est la liste entière, même vide ;
// NetworksComplete dit la même chose de Networks.
type Report struct {
	Engine           *EngineReport   `json:"engine,omitempty"`
	Complete         bool            `json:"complete,omitempty"`
	Inventory        []Container     `json:"inventory,omitempty"`
	NetworksComplete bool            `json:"networks_complete,omitempty"`
	Networks         []NetworkReport `json:"networks,omitempty"`
	Events           []Event         `json:"events,omitempty"`
	Stats            []Stat          `json:"stats,omitempty"`
}

// IsEmpty dit si le signal ne porte rien sur les services.
func (r Report) IsEmpty() bool {
	return r.Engine == nil && !r.Complete && !r.NetworksComplete && len(r.Events) == 0 && len(r.Stats) == 0
}

// NetworkReport est un réseau de la machine tel que l'agent l'a listé.
type NetworkReport struct {
	NetworkID string `json:"network_id"`
	Name      string `json:"name"`
	Driver    string `json:"driver"`
	Internal  bool   `json:"internal,omitempty"`
	Group     string `json:"group,omitempty"`
}

type EngineReport struct {
	Present    bool         `json:"present"`
	Reason     EngineReason `json:"reason,omitempty"`
	Version    string       `json:"version,omitempty"`
	APIVersion string       `json:"api_version,omitempty"`
}

// Container est un conteneur tel que l'agent l'a inspecté.
type Container struct {
	ContainerID string `json:"container_id"`
	Name        string `json:"name"`
	Group       string `json:"group,omitempty"`
	Image       string `json:"image"`
	ImageID     string `json:"image_id,omitempty"`
	// Ce que Compose pose en labels : le nom du service, le dossier et le
	// fichier du projet.
	ComposeService string       `json:"compose_service,omitempty"`
	ComposeDir     string       `json:"compose_dir,omitempty"`
	ComposeFile    string       `json:"compose_file,omitempty"`
	State          State        `json:"state"`
	ExitCode       int          `json:"exit_code"`
	Health         Health       `json:"health,omitempty"`
	RestartCount   int          `json:"restart_count"`
	Ports          []Port       `json:"ports,omitempty"`
	NetworkMode    string       `json:"network_mode,omitempty"`
	Privileged     bool         `json:"privileged,omitempty"`
	Networks       []Attachment `json:"networks,omitempty"`
	DependsOn      []Dependency `json:"depends_on,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	StartedAt      *time.Time   `json:"started_at,omitempty"`
	FinishedAt     *time.Time   `json:"finished_at,omitempty"`
}

// Event est un événement Docker traduit par l'agent : l'action, l'état et
// la santé qu'elle implique (vides si elle n'en change pas), et le
// conteneur réinspecté, absent quand il est détruit. Replayed marque ce
// qui a attendu une reconnexion : écrit, mais pas de quoi alerter.
type Event struct {
	At          time.Time  `json:"at"`
	Action      string     `json:"action"`
	ContainerID string     `json:"container_id"`
	State       State      `json:"state,omitempty"`
	Health      Health     `json:"health,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Replayed    bool       `json:"replayed,omitempty"`
	Snippet     string     `json:"snippet,omitempty"`
	Container   *Container `json:"container,omitempty"`
}

// ActionDestroy est la seule action qui ferme une fiche. Les actions de
// réseau ne changent ni l'état ni la santé : elles portent le conteneur
// réinspecté, avec ses réseaux du moment.
const (
	ActionDestroy           = "destroy"
	ActionNetworkConnect    = "network_connect"
	ActionNetworkDisconnect = "network_disconnect"
)

type Stat struct {
	ContainerID string    `json:"container_id"`
	SampledAt   time.Time `json:"sampled_at"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemUsed     int64     `json:"mem_used"`
	MemLimit    int64     `json:"mem_limit"`
}

// Les journaux : le serveur demande, l'agent livre par lots.
type LogRequest struct {
	ID          string `json:"id"`
	ContainerID string `json:"container_id"`
	Tail        int    `json:"tail"`
	Follow      bool   `json:"follow"`
}

type LogLine struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

// LogBatch est un lot de lignes ; Done clôt la requête, Error dit pourquoi
// elle n'a pas abouti, par un code que le front traduit.
type LogBatch struct {
	Lines []LogLine `json:"lines"`
	Done  bool      `json:"done,omitempty"`
	Error string    `json:"error,omitempty"`
}

const (
	DefaultLogTail = 100
	MaxLogTail     = 500
	// Un lot part toutes les secondes ou dès qu'il atteint ce nombre.
	MaxLinesPerBatch = 64
	// Combien de suivis une machine sert à la fois ; au-delà, refusé.
	MaxFollowsPerMachine = 4
	// Ce que le serveur attend d'un tirage unique avant de renoncer.
	LogFetchTimeout = 15 * time.Second
	// Les noms des événements du flux serveur → agent.
	CommandLogs     = "logs"
	CommandLogsStop = "logs_stop"
)
