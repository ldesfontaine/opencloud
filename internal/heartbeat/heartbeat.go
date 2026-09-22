package heartbeat

import (
	"errors"
	"strings"
	"time"
)

// Les états d'un moniteur, dans le vocabulaire de la direction artistique :
// Nouveau, À l'heure, Démarré, En retard, En échec, En pause.
type Status string

const (
	StatusNew     Status = "new"
	StatusOnTime  Status = "on_time"
	StatusStarted Status = "started"
	StatusLate    Status = "late"
	StatusFailed  Status = "failed"
	StatusPaused  Status = "paused"
)

// Statuses liste les états, dans l'ordre des écrans.
func Statuses() []Status {
	return []Status{StatusNew, StatusOnTime, StatusStarted, StatusLate, StatusFailed, StatusPaused}
}

// Les trois formes de ping : fini, démarré, fini avec un code de sortie.
type Kind string

const (
	KindFinish   Kind = "finish"
	KindStart    Kind = "start"
	KindExitCode Kind = "exit_code"
)

func Kinds() []Kind {
	return []Kind{KindFinish, KindStart, KindExitCode}
}

// Le résultat d'une exécution : ouverte par un start, close par une fin ou
// par le dépassement de l'échéance.
type Outcome string

const (
	OutcomeInProgress Outcome = "in_progress"
	OutcomeSuccess    Outcome = "success"
	OutcomeFailure    Outcome = "failure"
	OutcomeTimeout    Outcome = "timeout"
)

func Outcomes() []Outcome {
	return []Outcome{OutcomeInProgress, OutcomeSuccess, OutcomeFailure, OutcomeTimeout}
}

const (
	MinInterval   = time.Minute
	MaxInterval   = 7 * 24 * time.Hour
	MaxNameLength = 80
	// Un ping peut porter un corps : la fin d'un journal, une taille. Au-delà,
	// le reste est ignoré.
	MaxPayloadBytes = 10 << 10
	MaxExitCode     = 255
	// Combien de temps le brut et les exécutions restent lisibles.
	PingRetention = 7 * 24 * time.Hour
	RunRetention  = 90 * 24 * time.Hour
)

type Heartbeat struct {
	ID    string
	Token string
	Name  string
	// Vide quand le moniteur n'est rattaché à aucune machine.
	MachineID   string
	MachineName string
	Status      Status
	Interval    time.Duration
	Grace       time.Duration
	// Zéro tant qu'aucun ping n'est arrivé.
	LastPingAt time.Time
	// Zéro quand rien n'est attendu : nouveau, en pause, déjà en retard.
	NextDeadlineAt time.Time
	// Zéro hors d'une exécution ouverte par un start.
	RunStartedAt time.Time
	// nil tant qu'aucun ping n'a porté de code de sortie.
	LastExitCode *int
	// nil tant qu'aucune exécution start/fin n'a été mesurée.
	LastDuration *time.Duration
	CreatedAt    time.Time
}

func (h Heartbeat) IsPaused() bool {
	return h.Status == StatusPaused
}

// Ping est une requête reçue, telle quelle. Source est l'adresse résolue
// aujourd'hui ; un relais par l'agent y mettra « agent:<machine> ».
type Ping struct {
	ID          int64
	HeartbeatID string
	Kind        Kind
	ExitCode    *int
	Source      string
	Method      string
	Payload     string
	ReceivedAt  time.Time
}

// Run est une exécution logique de la tâche.
type Run struct {
	ID          int64
	HeartbeatID string
	// Zéro quand la tâche n'a envoyé que sa fin.
	StartedAt   time.Time
	CompletedAt time.Time
	Duration    *time.Duration
	ExitCode    *int
	Outcome     Outcome
	Payload     string
}

// Ce que l'opérateur donne pour créer un moniteur.
type Definition struct {
	Name      string
	MachineID string
	Interval  time.Duration
	Grace     time.Duration
}

var (
	ErrNotFound        = errors.New("heartbeat not found")
	ErrNameInvalid     = errors.New("heartbeat name must be 1 to 80 characters")
	ErrIntervalInvalid = errors.New("heartbeat interval must be between 1 minute and 7 days")
	ErrGraceInvalid    = errors.New("heartbeat grace must be between 0 and the interval")
	ErrExitCodeInvalid = errors.New("exit code must be between 0 and 255")
	ErrNotPaused       = errors.New("heartbeat is not paused")
)

func (d Definition) Validate() error {
	name := strings.TrimSpace(d.Name)
	if name == "" || len(name) > MaxNameLength {
		return ErrNameInvalid
	}
	if d.Interval < MinInterval || d.Interval > MaxInterval {
		return ErrIntervalInvalid
	}
	if d.Grace < 0 || d.Grace > d.Interval {
		return ErrGraceInvalid
	}
	return nil
}
