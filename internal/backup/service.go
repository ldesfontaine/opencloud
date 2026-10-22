package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Store est ce que le composant attend de la base ; le paquet store le
// fournit.
type Store interface {
	// GetBackupKeyring rend ErrNotFound tant qu'aucune clé n'existe.
	GetBackupKeyring(ctx context.Context) (Keyring, error)
	// SaveBackupKeyring écrit l'unique paire, ou remplace la précédente.
	SaveBackupKeyring(ctx context.Context, keyring Keyring) error
	ListBackupDestinations(ctx context.Context) ([]Destination, error)
	GetBackupDestination(ctx context.Context, id string) (Destination, error)
	// InsertBackupDestination rend ErrNameTaken si le nom existe.
	InsertBackupDestination(ctx context.Context, destination Destination) error
	// DeleteBackupDestination rend ErrDestinationInUse tant que le plan s'y
	// envoie, ErrNotFound si elle n'existe pas.
	DeleteBackupDestination(ctx context.Context, id string) error
	CountBackupDestinations(ctx context.Context) (int, error)
	// GetBackupPlan rend ErrNotFound sans plan.
	GetBackupPlan(ctx context.Context) (Plan, error)
	// SaveBackupPlan crée le plan ou change sa destination, son nombre
	// gardé et son rythme, sans toucher à sa dernière tentative.
	SaveBackupPlan(ctx context.Context, plan Plan) error
	DeleteBackupPlan(ctx context.Context) error
	// NoteBackupAttempt écrit la tentative : son instant, la clé de son
	// refus (vide si l'ordre est parti) et le run du backup.receive.
	NoteBackupAttempt(ctx context.Context, at time.Time, errKey, runID string) error
	// SnapshotDatabase écrit dans un fichier neuf une copie cohérente de la
	// base (VACUUM INTO).
	SnapshotDatabase(ctx context.Context, path string) error
}

// Machines est ce que le composant lit des machines : si elles sont en
// ligne et ce que leur agent a rapporté de ses droits.
type Machines interface {
	Get(ctx context.Context, id string) (machine.Status, error)
}

// Sender envoie un ordre signé à l'agent d'une machine : le composant action.
type Sender interface {
	Send(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage) (action.Run, error)
}

// Parcels prépare le colis que l'agent télécharge : la boîte d'action.
type Parcels interface {
	Create(machineID string) (*action.ParcelWriter, error)
}

// Watcher reçoit chaque changement visible ; le direct s'y branche. nil est
// toléré.
type Watcher interface {
	BackupsChanged()
}

var destinationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Service struct {
	store    Store
	machines Machines
	stateDir *os.Root
	sender   Sender
	parcels  Parcels
	watcher  Watcher
	auditor  *audit.Recorder
	logger   *slog.Logger
	now      func() time.Time
	// Le coût de scrypt d'une création de clé ; zéro prend celui d'age. Les
	// tests le baissent, une création coûte sinon un quart de gigaoctet.
	workFactor int

	// writing tient compter-puis-insérer d'un seul tenant (le plafond des
	// destinations) et une seule création de clé à la fois.
	writing sync.Mutex
	// running ne laisse préparer qu'une archive à la fois.
	running sync.Mutex
}

func New(store Store, machines Machines, stateDir *os.Root, logger *slog.Logger) *Service {
	return &Service{store: store, machines: machines, stateDir: stateDir, logger: logger, now: time.Now}
}

func (s *Service) SetSender(sender Sender)            { s.sender = sender }
func (s *Service) SetParcels(parcels Parcels)         { s.parcels = parcels }
func (s *Service) SetWatcher(watcher Watcher)         { s.watcher = watcher }
func (s *Service) SetAuditor(auditor *audit.Recorder) { s.auditor = auditor }
func (s *Service) SetClock(now func() time.Time)      { s.now = now }

// SetWorkFactor baisse le coût de scrypt : pour les tests seulement.
func (s *Service) SetWorkFactor(workFactor int) { s.workFactor = workFactor }

func (s *Service) changed() {
	if s.watcher != nil {
		s.watcher.BackupsChanged()
	}
}

// CreateKeyring tire la paire de clés et scelle la privée avec la phrase.
// La phrase n'est lue que d'ici : ni la base, ni le journal, ni l'audit ne
// la voient. Remplacer ne rend pas les sauvegardes déjà faites illisibles :
// chacune a sa clé scellée à côté d'elle.
func (s *Service) CreateKeyring(ctx context.Context, passphrase string, replace bool) (Keyring, error) {
	passphrase = NormalizePassphrase(passphrase)
	if err := checkPassphrase(passphrase); err != nil {
		return Keyring{}, err
	}
	s.writing.Lock()
	defer s.writing.Unlock()
	_, err := s.store.GetBackupKeyring(ctx)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Keyring{}, fmt.Errorf("read backup key: %w", err)
	}
	if exists && !replace {
		return Keyring{}, ErrKeyringExists
	}
	keyring, err := newKeyring(passphrase, s.workFactor, s.now())
	if err != nil {
		return Keyring{}, err
	}
	if err := s.store.SaveBackupKeyring(ctx, keyring); err != nil {
		return Keyring{}, fmt.Errorf("save backup key: %w", err)
	}
	verb := audit.BackupKeyringCreate
	if exists {
		verb = audit.BackupKeyringReplace
	}
	s.logger.Info("backup key created", "fingerprint", keyring.Fingerprint, "replaced", exists)
	s.auditor.Record(ctx, audit.Event{
		Action: verb, ObjectID: keyring.Fingerprint, ObjectName: keyring.Fingerprint,
		Details: audit.Details{"fingerprint": keyring.Fingerprint},
	})
	s.changed()
	return keyring, nil
}

// CreateDestination ajoute une machine qui reçoit les sauvegardes. Le droit
// de son agent se juge ici s'il l'a rapporté ; sinon c'est lui qui refusera
// l'ordre.
func (s *Service) CreateDestination(ctx context.Context, definition DestinationDefinition) (Destination, error) {
	if !destinationNamePattern.MatchString(definition.Name) {
		return Destination{}, ErrNameInvalid
	}
	target, err := s.machines.Get(ctx, definition.MachineID)
	if errors.Is(err, machine.ErrNotFound) {
		return Destination{}, ErrMachineUnknown
	}
	if err != nil {
		return Destination{}, fmt.Errorf("get machine: %w", err)
	}
	if !machine.ValidCapabilityPath(definition.Path) {
		return Destination{}, ErrPathInvalid
	}
	if rightOf(target, definition.Path) == RightMissing {
		return Destination{}, ErrRightMissing
	}
	id, err := ident.New()
	if err != nil {
		return Destination{}, fmt.Errorf("generate id: %w", err)
	}
	created := Destination{
		ID: "bkd_" + id, Name: definition.Name, MachineID: target.ID, MachineName: target.Name,
		Path: definition.Path, CreatedAt: s.now(),
	}
	s.writing.Lock()
	defer s.writing.Unlock()
	count, err := s.store.CountBackupDestinations(ctx)
	if err != nil {
		return Destination{}, fmt.Errorf("count destinations: %w", err)
	}
	if count >= MaxDestinations {
		return Destination{}, ErrTooManyDestinations
	}
	if err := s.store.InsertBackupDestination(ctx, created); err != nil {
		return Destination{}, err
	}
	s.logger.Info("backup destination created", "destination_id", created.ID, "name", created.Name, "machine_id", created.MachineID)
	s.auditor.Record(ctx, destinationEvent(audit.BackupDestinationCreate, created))
	s.changed()
	return created, nil
}

// DeleteDestination retire une destination que le plan n'utilise pas.
func (s *Service) DeleteDestination(ctx context.Context, id string) error {
	destination, err := s.store.GetBackupDestination(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteBackupDestination(ctx, id); err != nil {
		return err
	}
	s.logger.Info("backup destination deleted", "destination_id", id, "name", destination.Name)
	s.auditor.Record(ctx, destinationEvent(audit.BackupDestinationDelete, destination))
	s.changed()
	return nil
}

// SavePlan crée le plan de la base, ou change sa destination et le nombre
// de sauvegardes gardées.
func (s *Service) SavePlan(ctx context.Context, definition PlanDefinition) (Plan, error) {
	if definition.Keep == 0 {
		definition.Keep = DefaultKeep
	}
	if definition.Keep < MinKeep || definition.Keep > MaxKeep {
		return Plan{}, ErrKeepInvalid
	}
	destination, err := s.store.GetBackupDestination(ctx, definition.DestinationID)
	if errors.Is(err, ErrNotFound) {
		return Plan{}, ErrDestinationUnknown
	}
	if err != nil {
		return Plan{}, err
	}
	id, err := ident.New()
	if err != nil {
		return Plan{}, fmt.Errorf("generate id: %w", err)
	}
	now := s.now()
	plan := Plan{
		ID: "bkp_" + id, Subject: SubjectDatabase, DestinationID: destination.ID, Keep: definition.Keep,
		Schedule: ScheduleDaily, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.SaveBackupPlan(ctx, plan); err != nil {
		return Plan{}, fmt.Errorf("save backup plan: %w", err)
	}
	saved, err := s.store.GetBackupPlan(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("read backup plan: %w", err)
	}
	s.logger.Info("backup plan saved", "plan_id", saved.ID, "destination_id", saved.DestinationID, "keep", saved.Keep)
	s.auditor.Record(ctx, audit.Event{
		Action: audit.BackupPlanUpdate, ObjectID: saved.ID, ObjectName: SubjectDatabase,
		Details: audit.Details{"destination_id": saved.DestinationID, "keep": fmt.Sprint(saved.Keep)},
	})
	s.changed()
	return saved, nil
}

// DeletePlan arrête les sauvegardes : rien ne part plus, ce qui est déjà
// sur la destination y reste.
func (s *Service) DeletePlan(ctx context.Context) error {
	plan, err := s.store.GetBackupPlan(ctx)
	if err != nil {
		return err
	}
	if err := s.store.DeleteBackupPlan(ctx); err != nil {
		return err
	}
	s.logger.Info("backup plan deleted", "plan_id", plan.ID)
	s.auditor.Record(ctx, audit.Event{
		Action: audit.BackupPlanDelete, ObjectID: plan.ID, ObjectName: SubjectDatabase,
	})
	s.changed()
	return nil
}

// Overview rend ce que la page montre : la clé, les destinations avec l'état
// du droit de leur agent, le plan.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	var overview Overview
	keyring, err := s.store.GetBackupKeyring(ctx)
	switch {
	case err == nil:
		overview.Keyring = &keyring
	case !errors.Is(err, ErrNotFound):
		return Overview{}, fmt.Errorf("read backup key: %w", err)
	}
	destinations, err := s.store.ListBackupDestinations(ctx)
	if err != nil {
		return Overview{}, fmt.Errorf("list destinations: %w", err)
	}
	overview.Destinations = make([]DestinationView, 0, len(destinations))
	for _, destination := range destinations {
		view := DestinationView{Destination: destination, Right: RightUnknown}
		if target, err := s.machines.Get(ctx, destination.MachineID); err == nil {
			view.Right = rightOf(target, destination.Path)
		}
		overview.Destinations = append(overview.Destinations, view)
	}
	plan, err := s.store.GetBackupPlan(ctx)
	switch {
	case err == nil:
		overview.Plan = &plan
	case !errors.Is(err, ErrNotFound):
		return Overview{}, fmt.Errorf("read backup plan: %w", err)
	}
	return overview, nil
}

// rightOf juge si l'agent de la machine peut écrire dans ce dossier : « ok »
// ou « missing » une fois ses droits rapportés, « unknown » avant. Sans agent
// actif, rien ne peut y être reçu.
func rightOf(target machine.Status, path string) RightState {
	if !target.Agent.IsActive() {
		return RightMissing
	}
	if target.Agent.ReportedAt.IsZero() {
		return RightUnknown
	}
	if machine.CoversPath(target.Agent.Capabilities, machine.CapabilityBackupReceive, path) {
		return RightOK
	}
	return RightMissing
}

func destinationEvent(verb audit.Action, destination Destination) audit.Event {
	return audit.Event{
		Action: verb, ObjectID: destination.ID, ObjectName: destination.Name,
		Details: audit.Details{"name": destination.Name, "machine_id": destination.MachineID, "path": destination.Path},
	}
}
