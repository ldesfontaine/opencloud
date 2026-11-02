package restic

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// Store est ce que le composant attend de la base ; le paquet store le
// fournit.
type Store interface {
	// GetResticTarget rend ErrTargetUnknown si la machine n'est pas une cible.
	GetResticTarget(ctx context.Context, machineID string) (TargetRecord, error)
	ListResticTargets(ctx context.Context) ([]TargetRecord, error)
	// SaveResticTargetRequest pose ou remplace ce que l'opérateur demande,
	// sans toucher à ce que l'agent a rapporté.
	SaveResticTargetRequest(ctx context.Context, request TargetRecord) error
	// DeleteResticTarget rend ErrTargetUnknown si la ligne n'existe pas.
	DeleteResticTarget(ctx context.Context, machineID string) error
	// RecordResticTargetReport écrit l'état rapporté ; faux si la machine
	// n'est pas une cible (rien n'est écrit).
	RecordResticTargetReport(ctx context.Context, machineID string, report agentwire.ResticTargetReport, at time.Time) (bool, error)

	// InsertResticBackup rend ErrBackupExists si le nom ou la même source
	// vers la même cible existe.
	InsertResticBackup(ctx context.Context, backup Backup) error
	// UpdateResticBackup change le nom et la rétention ; ErrBackupNotFound.
	UpdateResticBackup(ctx context.Context, backup Backup) error
	DeleteResticBackup(ctx context.Context, id string) error
	GetResticBackup(ctx context.Context, id string) (Backup, error)
	ListResticBackups(ctx context.Context) ([]Backup, error)
	// FindResticBackupByRun rend la sauvegarde dont le dernier ordre est ce
	// run, envoyé à cette source ; ErrBackupNotFound sinon.
	FindResticBackupByRun(ctx context.Context, sourceID, runID string) (Backup, error)
	// NoteResticRunSent pose le dernier ordre et sa tentative.
	NoteResticRunSent(ctx context.Context, id, runID string, at time.Time) error
	// RecordResticOutcome écrit l'état du dernier ordre ; fini, il ajoute une
	// ligne d'historique et purge au-delà de la borne.
	RecordResticOutcome(ctx context.Context, id string, outcome Outcome) error
	ListRecentResticRuns(ctx context.Context, perBackup int) (map[string][]RunRecord, error)

	// EnsureResticLink pose le lien s'il n'existe pas, ses mots de passe
	// scellés, et rend celui qui est en base, ouvert ; vrai s'il vient d'être
	// posé.
	EnsureResticLink(ctx context.Context, link Link) (Link, bool, error)
	// GetResticLink rend le lien, ses mots de passe ouverts ; ErrLinkNotFound.
	GetResticLink(ctx context.Context, sourceID, targetID string) (Link, error)
	// ListResticLinks rend les liens en service, sans leurs mots de passe.
	ListResticLinks(ctx context.Context) ([]Link, error)
	// DeleteResticLink efface le lien et ses mots de passe ; ErrLinkNotFound.
	DeleteResticLink(ctx context.Context, sourceID, targetID string) error
	// SetResticLinkRetired retire le lien (at) ou le remet en service (zéro),
	// ses mots de passe gardés ; ErrLinkNotFound.
	SetResticLinkRetired(ctx context.Context, linkID string, at time.Time) error
	// NoteResticRetain écrit la dernière rétention du dépôt d'un lien.
	NoteResticRetain(ctx context.Context, linkID string, outcome RetainOutcome) error

	// Ce que la règle lit des autres domaines.
	ResticGuest(ctx context.Context, hostID string, vmid int) (proxmox.Guest, error)
	ResticService(ctx context.Context, serviceID string) (ServiceRef, error)
	ListResticServices(ctx context.Context) ([]ServiceRef, error)
	ProxmoxBackupJobsByHost(ctx context.Context) (map[string][]proxmox.BackupJob, error)
	ProxmoxJobsUnreadable(ctx context.Context) (map[string]bool, error)
}

// Machines est ce que le composant lit des machines : en ligne, et les droits
// que leur agent a rapportés.
type Machines interface {
	Get(ctx context.Context, id string) (machine.Status, error)
	List(ctx context.Context) ([]machine.Status, error)
}

// Sender envoie un ordre signé à l'agent d'une machine, et attend qu'il
// finisse : le composant action.
type Sender interface {
	Send(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage) (action.Run, error)
	// SendWithSecret signe un secret avec l'ordre ; il n'entre ni dans le
	// run, ni dans l'audit, ni dans un journal.
	SendWithSecret(ctx context.Context, machineID string, kind agentwire.Kind, params, secret json.RawMessage, label string) (action.Run, error)
	Wait(ctx context.Context, runID string) (action.Run, error)
}

// Inventory est ce que la page Sauvegardes sait déjà sans restic : les plans
// par archives d'age, ce que Proxmox garde, les volumes. Le composant backup.
type Inventory interface {
	Inventory(ctx context.Context) (backup.Inventory, error)
}

// Watcher reçoit chaque changement visible ; le direct s'y branche. nil est
// toléré.
type Watcher interface {
	BackupsChanged()
}

type Service struct {
	store     Store
	machines  Machines
	sender    Sender
	inventory Inventory
	watcher   Watcher
	auditor   *audit.Recorder
	logger    *slog.Logger
	now       func() time.Time

	// busy tient les dépôts qui reçoivent une sauvegarde ou une rétention :
	// jamais deux à la fois sur le même dépôt (une source vers une cible).
	mu   sync.Mutex
	busy map[repoKey]bool
	// passing ne laisse mener qu'un passage de la nuit à la fois.
	passing sync.Mutex

	// lifetime est la vie du serveur : une sauvegarde lancée à la main est
	// suivie derrière la requête ; background compte ces suivis.
	lifetime   context.Context
	background sync.WaitGroup
}

// repoKey nomme un dépôt : la source et sa cible.
type repoKey struct {
	source string
	target string
}

func New(store Store, machines Machines, logger *slog.Logger) *Service {
	return &Service{
		store: store, machines: machines, logger: logger, now: time.Now,
		busy: map[repoKey]bool{}, lifetime: context.Background(),
	}
}

func (s *Service) SetSender(sender Sender)              { s.sender = sender }
func (s *Service) SetInventory(inventory Inventory)     { s.inventory = inventory }
func (s *Service) SetWatcher(watcher Watcher)           { s.watcher = watcher }
func (s *Service) SetAuditor(auditor *audit.Recorder)   { s.auditor = auditor }
func (s *Service) SetClock(now func() time.Time)        { s.now = now }
func (s *Service) SetLifetime(lifetime context.Context) { s.lifetime = lifetime }

// WaitBackground attend les suivis de sauvegardes lancées à la main.
func (s *Service) WaitBackground() {
	s.background.Wait()
}

func (s *Service) changed() {
	if s.watcher != nil {
		s.watcher.BackupsChanged()
	}
}

// claimRepo réserve le dépôt d'une sauvegarde ; faux s'il en reçoit déjà une.
func (s *Service) claimRepo(key repoKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[key] {
		return false
	}
	s.busy[key] = true
	return true
}

func (s *Service) releaseRepo(key repoKey) {
	s.mu.Lock()
	delete(s.busy, key)
	s.mu.Unlock()
}
