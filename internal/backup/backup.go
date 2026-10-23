package backup

import (
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// SubjectDatabase : ce que le plan sauvegarde. Les services viendront
	// avec le déploiement.
	SubjectDatabase = "database"
	ScheduleDaily   = "daily"
	// dailyAtHour : l'heure du serveur après laquelle la nuit lance sa
	// sauvegarde. Une heure de l'horloge, pas une durée depuis minuit : le
	// jour d'un changement d'heure en compte une de plus ou de moins.
	dailyAtHour = 3

	MinPassphrase   = 16
	MaxPassphrase   = 1024
	MaxDestinations = 8
	DefaultKeep     = 14
	MinKeep         = 1
	MaxKeep         = 365

	watchPeriod = 15 * time.Minute
)

// PrepareFailedKey est la clé que last_error garde quand l'archive n'a pas pu
// être préparée (disque plein, base illisible) : ce n'est pas un refus, la
// cause est au journal du serveur.
const PrepareFailedKey = "backup.prepare_failed"

// Keyring est la paire de clés : la publique chiffre, la privée est scellée
// par la phrase de passe que la base ne voit jamais.
type Keyring struct {
	Recipient      string
	Fingerprint    string
	IdentitySealed []byte
	CreatedAt      time.Time
}

// Destination est une machine dont l'agent reçoit les sauvegardes, et le
// dossier où il les écrit.
type Destination struct {
	ID          string
	Name        string
	MachineID   string
	MachineName string
	Path        string
	CreatedAt   time.Time
}

type DestinationDefinition struct {
	Name      string
	MachineID string
	Path      string
}

// Plan est la sauvegarde planifiée de la base.
type Plan struct {
	ID            string
	Subject       string
	DestinationID string
	Keep          int
	Schedule      string
	// Zéro tant qu'aucune tentative n'a eu lieu.
	LastAttemptAt time.Time
	// La clé du refus de la dernière tentative qui n'a pas pu partir, vide
	// sinon.
	LastError string
	// Le dernier ordre backup.receive ; vide s'il n'y en a pas eu, ou si la
	// purge des runs l'a emporté.
	LastRunID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type PlanDefinition struct {
	DestinationID string
	// Zéro vaut DefaultKeep.
	Keep int
}

// RightState dit si l'agent d'une destination peut écrire dans son dossier :
// « unknown » tant qu'il n'a pas rapporté ses droits. « unavailable » : il a le
// droit, mais son compte ne peut pas l'exercer (DestinationView.RightReason
// dit pourquoi) ; le serveur l'affiche sans refuser l'envoi, l'agent refuse au
// moment de l'ordre, avec la raison.
type RightState string

const (
	RightOK          RightState = "ok"
	RightMissing     RightState = "missing"
	RightUnavailable RightState = "unavailable"
	RightUnknown     RightState = "unknown"
)

type DestinationView struct {
	Destination
	Right RightState
	// Avec « unavailable », la raison que l'agent a rapportée.
	RightReason string
}

// Overview est ce que la page montre : ce qui existe et ce qui manque.
type Overview struct {
	Keyring      *Keyring
	Destinations []DestinationView
	Plan         *Plan
}

var (
	ErrNotFound = errors.New("backup object not found")

	ErrPassphraseTooShort  = refusal.Invalid("backup.passphrase_too_short", "the passphrase must be at least 16 characters long")
	ErrKeyringExists       = refusal.Conflict("backup.keyring_exists", "a backup key already exists: replace it explicitly")
	ErrKeyringMissing      = refusal.Conflict("backup.keyring_missing", "no backup key: create it first")
	ErrNameInvalid         = refusal.Invalid("backup.name_invalid", "destination name: lowercase letters, digits and dashes, 63 at most")
	ErrNameTaken           = refusal.Conflict("backup.name_taken", "a destination already has this name")
	ErrMachineUnknown      = refusal.Invalid("backup.machine_unknown", "the machine does not exist")
	ErrPathInvalid         = refusal.Invalid("backup.path_invalid", "destination path must be absolute, clean, and made of letters, digits, dot, dash, underscore")
	ErrRightMissing        = refusal.Conflict("backup.right_missing", "the agent of this machine may not write to this folder")
	ErrTooManyDestinations = refusal.Invalid("backup.too_many_destinations", "at most 8 destinations")
	ErrDestinationUnknown  = refusal.Invalid("backup.destination_unknown", "the destination does not exist")
	ErrDestinationInUse    = refusal.Conflict("backup.destination_in_use", "the plan sends its backups to this destination")
	ErrKeepInvalid         = refusal.Invalid("backup.keep_invalid", "keep between 1 and 365 backups")
	ErrPlanMissing         = refusal.Conflict("backup.plan_missing", "no plan: choose a destination")
	ErrMachineOffline      = refusal.Conflict("backup.machine_offline", "the destination machine is offline")
)
