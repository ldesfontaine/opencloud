package link

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
)

// ServicePrefix précède l'identifiant d'un lien.
const ServicePrefix = "lnk_"

// Ce que le service attend de la base ; le paquet store le fournit.
type Store interface {
	// InsertLink écrit un lien neuf ; ErrDuplicate si la paire existe,
	// ErrTooMany si la machine d'une extrémité est au plafond.
	InsertLink(ctx context.Context, link Link) error
	GetLink(ctx context.Context, id string) (Link, error)
	// DeleteLink retire le lien ; ErrNotFound s'il n'existe plus.
	DeleteLink(ctx context.Context, id string) error
	// ListLinks rend les liens dont une extrémité est sur la machine, tous si
	// machineID est vide, par date de première vue puis identifiant.
	ListLinks(ctx context.Context, machineID string) ([]Link, error)
	// TouchOrderLinks écrit les liens d'ordre d'une machine en une
	// transaction : un lien connu voit sa dernière vue avancer, un lien neuf
	// naît. Aucun lien n'est retiré.
	TouchOrderLinks(ctx context.Context, machineID string, links []Link) error
	MachineExists(ctx context.Context, id string) (bool, error)
}

// Watcher reçoit chaque changement visible : le direct s'y branche. nil est
// toléré.
type Watcher interface {
	DirectoryChanged()
}

type Service struct {
	store   Store
	watcher Watcher
	auditor *audit.Recorder
	logger  *slog.Logger
	now     func() time.Time
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

func (s *Service) changed() {
	if s.watcher != nil {
		s.watcher.DirectoryChanged()
	}
}

// Declare écrit le lien que l'opérateur décrit : preuve « déclaré », signal
// « opérateur », nature « configuré pour joindre ».
func (s *Service) Declare(ctx context.Context, definition Definition) (Link, error) {
	return s.write(ctx, definition, NatureConfig, ProofDeclared, audit.LinkCreate)
}

// ConfirmRoute écrit en lien déclaré la route que l'opérateur a reconnue juste
// dans les liens déduits : elle devient sienne, et ne se recalcule plus.
func (s *Service) ConfirmRoute(ctx context.Context, definition Definition) (Link, error) {
	return s.write(ctx, definition, NatureRoute, ProofDeclared, audit.LinkConfirm)
}

// DismissRoute garde la trace d'une route déduite que l'opérateur écarte :
// tant qu'elle est là, la déduction ne la propose plus.
func (s *Service) DismissRoute(ctx context.Context, definition Definition) (Link, error) {
	return s.write(ctx, definition, NatureRoute, ProofDeduced, audit.LinkDismiss)
}

// write valide une définition, vérifie ses machines, puis l'écrit avec la
// nature et la preuve demandées. Le signal est toujours « opérateur » : c'est
// un geste de l'opérateur.
func (s *Service) write(ctx context.Context, definition Definition, nature Nature, proof Proof, action audit.Action) (Link, error) {
	if err := definition.Validate(); err != nil {
		return Link{}, err
	}
	for _, machineID := range []string{definition.From.MachineID, definition.To.MachineID} {
		exists, err := s.store.MachineExists(ctx, machineID)
		if err != nil {
			return Link{}, fmt.Errorf("check link machine: %w", err)
		}
		if !exists {
			return Link{}, ErrMachine
		}
	}
	id, err := ident.New()
	if err != nil {
		return Link{}, fmt.Errorf("draw link id: %w", err)
	}
	now := s.now().UTC()
	written := Link{
		ID: ServicePrefix + id, From: definition.From, To: definition.To,
		Nature: nature, Proof: proof, Signal: SignalOperator, Port: definition.Port,
		FirstSeen: now, LastSeen: now,
	}
	if err := s.store.InsertLink(ctx, written); err != nil {
		return Link{}, err
	}
	s.auditor.Record(ctx, event(action, written))
	s.changed()
	return written, nil
}

// Restore rend à la déduction une route écartée : elle se propose de nouveau.
func (s *Service) Restore(ctx context.Context, id string) error {
	found, err := s.store.GetLink(ctx, id)
	if err != nil {
		return err
	}
	if !found.Dismissed() {
		return ErrNotFound
	}
	if err := s.store.DeleteLink(ctx, id); err != nil {
		return err
	}
	s.auditor.Record(ctx, event(audit.LinkRestore, found))
	s.changed()
	return nil
}

// Remove retire un lien que l'opérateur a écrit. Un lien tiré d'une
// configuration d'outil se refuse : il reviendrait au rapport suivant.
func (s *Service) Remove(ctx context.Context, id string) error {
	found, err := s.store.GetLink(ctx, id)
	if err != nil {
		return err
	}
	if found.Dismissed() {
		return ErrNotFound
	}
	if !found.Removable() {
		return ErrNotRemovable
	}
	if err := s.store.DeleteLink(ctx, id); err != nil {
		return err
	}
	s.auditor.Record(ctx, event(audit.LinkDelete, found))
	s.changed()
	return nil
}

// List rend les liens dont une extrémité est sur la machine, tous si
// machineID est vide. Les routes écartées n'y sont pas : ce ne sont pas des
// liens, seulement la mémoire d'un refus (ListDismissed).
func (s *Service) List(ctx context.Context, machineID string) ([]Link, error) {
	return s.filtered(ctx, machineID, func(found Link) bool { return !found.Dismissed() })
}

// ListDismissed rend les routes que l'opérateur a écartées.
func (s *Service) ListDismissed(ctx context.Context, machineID string) ([]Link, error) {
	return s.filtered(ctx, machineID, Link.Dismissed)
}

func (s *Service) filtered(ctx context.Context, machineID string, keep func(Link) bool) ([]Link, error) {
	stored, err := s.store.ListLinks(ctx, machineID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(stored, func(found Link) bool { return !keep(found) }), nil
}

// RecordOrders écrit les liens d'ordre que la configuration des conteneurs
// d'une machine déclare : ils sont revus à l'instant, ceux qu'on ne voit plus
// vieillissent sans disparaître. Un rapport qui n'en porte aucun ne touche à rien.
func (s *Service) RecordOrders(ctx context.Context, machineID string, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	now := s.now().UTC()
	links := make([]Link, 0, len(orders))
	for _, order := range orders {
		id, err := ident.New()
		if err != nil {
			return fmt.Errorf("draw link id: %w", err)
		}
		links = append(links, Link{
			ID: ServicePrefix + id, From: Endpoint{MachineID: machineID, Key: order.From}, To: Endpoint{MachineID: machineID, Key: order.To},
			Nature: NatureOrder, Proof: ProofDeclared, Signal: order.Signal, FirstSeen: now, LastSeen: now,
		})
	}
	if err := s.store.TouchOrderLinks(ctx, machineID, links); err != nil {
		return err
	}
	s.changed()
	return nil
}

func event(action audit.Action, written Link) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: LabelOf(written.From.Key) + " → " + LabelOf(written.To.Key),
		Details: audit.Details{
			"from_machine_id": written.From.MachineID, "from": written.From.Key,
			"to_machine_id": written.To.MachineID, "to": written.To.Key,
		},
	}
}
