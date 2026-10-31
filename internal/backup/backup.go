package backup

import (
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// SubjectDatabase est le sujet du plan de la base, le seul qui porte ce
	// nom ; tout autre plan a une source agent et un sujet libre.
	SubjectDatabase = agentwire.DatabaseSubject
	ScheduleDaily   = "daily"

	// Ce que la source d'un plan est : la base de la machine openCloud, ou
	// ce qu'un agent produit sur sa machine.
	SourceDatabase = "database"
	SourceAgent    = "agent"

	// SelectorAll prend tout ce que le producteur sait nommer.
	SelectorAll = "all"

	MaxSubject  = 40
	MaxName     = 80
	MaxSelector = 255
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

// Les clés de last_error qui disent une cible précise d'un plan machine. Elles
// portent leurs arguments après des deux-points (« backup.target_failed:5012:
// producer_failed ») : la page les compose, et seul ce qui a une forme fermée
// y entre (voir targetReason).
const (
	TargetFailedKey = "backup.target_failed"
	PruneFailedKey  = "backup.prune_failed"
)

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

// Plan est une sauvegarde planifiée : une source (la base, ou ce qu'un agent
// produit), une destination, un nombre à garder.
type Plan struct {
	ID string
	// Subject est le nom lisible et unique du plan, le préfixe de ses
	// fichiers sur la destination.
	Subject string
	// Name est le libellé affiché ; vide, la page montre le sujet.
	Name       string
	SourceKind string
	// Les trois champs suivants ne décrivent qu'une source agent : la machine
	// qui produit, le producteur, et ce qu'il en prend (« all » ou des
	// identifiants séparés par des virgules).
	SourceMachineID   string
	SourceMachineName string
	Producer          string
	Selector          string
	DestinationID     string
	Keep              int
	Schedule          string
	// Zéro tant qu'aucune tentative n'a eu lieu.
	LastAttemptAt time.Time
	// La clé du refus de la dernière tentative qui n'a pas pu partir, vide
	// sinon.
	LastError string
	// Le dernier ordre du plan : le backup.receive du dernier passage, ou
	// l'ordre en cours pendant qu'il dure. Vide s'il n'y en a pas eu, ou si la
	// purge des runs l'a emporté.
	LastRunID string
	// Les cibles du dernier passage d'un plan machine : reçues, et en échec.
	// Lues dans l'historique (backup_runs) : zéro pour un plan qui n'a pas
	// encore passé.
	LastTargetsOK     int
	LastTargetsFailed int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// FilePrefix est ce que les fichiers du plan ont en commun sur la
// destination (agentwire.BackupFilePrefix) ; l'élagage s'y borne.
func (p Plan) FilePrefix() string {
	return agentwire.BackupFilePrefix(p.Subject, "")
}

// TargetPrefix est le préfixe des fichiers d'une cible du plan : un invité par
// numéro (« pve » donne « pve-vm-5012 »). L'élagage d'une cible ne touche pas
// à celles des autres.
func (p Plan) TargetPrefix(target string) string {
	return agentwire.BackupFilePrefix(p.Subject, "vm-"+target)
}

// PlanDefinition est ce que l'opérateur écrit d'un plan : le sujet vient de
// l'adresse, le reste du formulaire.
type PlanDefinition struct {
	Subject         string
	Name            string
	SourceKind      string
	SourceMachineID string
	Producer        string
	Selector        string
	DestinationID   string
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
	Plans        []Plan
}

var (
	ErrNotFound = errors.New("backup object not found")

	ErrPassphraseTooShort   = refusal.Invalid("backup.passphrase_too_short", "the passphrase must be at least 16 characters long")
	ErrKeyringExists        = refusal.Conflict("backup.keyring_exists", "a backup key already exists: replace it explicitly")
	ErrKeyringMissing       = refusal.Conflict("backup.keyring_missing", "no backup key: create it first")
	ErrNameInvalid          = refusal.Invalid("backup.name_invalid", "destination name: lowercase letters, digits and dashes, 63 at most")
	ErrNameTaken            = refusal.Conflict("backup.name_taken", "a destination already has this name")
	ErrMachineUnknown       = refusal.Invalid("backup.machine_unknown", "the machine does not exist")
	ErrPathInvalid          = refusal.Invalid("backup.path_invalid", "destination path must be absolute, clean, and made of letters, digits, dot, dash, underscore")
	ErrRightMissing         = refusal.Conflict("backup.right_missing", "the agent of this machine may not write to this folder")
	ErrTooManyDestinations  = refusal.Invalid("backup.too_many_destinations", "at most 8 destinations")
	ErrDestinationUnknown   = refusal.Invalid("backup.destination_unknown", "the destination does not exist")
	ErrDestinationInUse     = refusal.Conflict("backup.destination_in_use", "the plan sends its backups to this destination")
	ErrKeepInvalid          = refusal.Invalid("backup.keep_invalid", "keep between 1 and 365 backups")
	ErrPlanMissing          = refusal.Conflict("backup.plan_missing", "no plan has this subject")
	ErrPlanInvalid          = refusal.Invalid("backup.plan_invalid", "plan subject must be lowercase letters, digits and dashes (40 at most), not end with -vm-<digits>, and match its source; name 80 characters at most")
	ErrPlanExists           = refusal.Conflict("backup.plan_exists", "another plan already sends the same source to this destination")
	ErrSourceMachineUnknown = refusal.Invalid("backup.source_machine_unknown", "the machine that produces the backup does not exist")
	ErrProducerUnknown      = refusal.Invalid("backup.producer_unknown", "the producer is not one of the known producers")
	ErrSelectorInvalid      = refusal.Invalid("backup.selector_invalid", "selector: all, or identifiers separated by commas")
	ErrMachineOffline       = refusal.Conflict("backup.machine_offline", "the destination machine is offline")

	// Ce qui arrête un plan machine avant son premier ordre : la source, sa
	// sélection, l'agent qui reçoit.
	ErrSourceRightMissing       = refusal.Conflict("backup.source_right_missing", "the agent of the source machine may not run this producer")
	ErrSourceOffline            = refusal.Conflict("backup.source_offline", "the source machine is offline")
	ErrSelectorNeedsList        = refusal.Conflict("backup.selector_needs_list", "all needs a Proxmox host declared on the source machine, with a reading: give the list of identifiers")
	ErrDestinationAgentOutdated = refusal.Conflict("backup.destination_agent_outdated", "the agent of the destination is too old to prune one plan without touching the others")
	ErrRunInProgress            = refusal.Conflict("backup.run_in_progress", "a backup is already running")
)
