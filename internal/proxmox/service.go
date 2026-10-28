package proxmox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// watchPeriod est le rythme des relevés : une VM créée ou arrêtée se voit en
// cinq minutes, sans que l'API de l'hyperviseur soit sollicitée pour rien.
const watchPeriod = 5 * time.Minute

// Store est ce que le composant attend de la base ; le paquet store le
// fournit. Les lectures de la fiche passent par le Reader.
type Store interface {
	// GetProxmoxCredentials rend l'hôte avec son secret déchiffré, ErrNotFound
	// si la machine n'en porte pas.
	GetProxmoxCredentials(ctx context.Context, machineID string) (Credentials, error)
	// SaveProxmoxHost crée l'hôte ou remplace sa connexion (adresse, jeton,
	// empreinte, version), en gardant le dernier relevé.
	SaveProxmoxHost(ctx context.Context, credentials Credentials) error
	// DeleteProxmoxHost retire l'hôte avec ses VM et ses stockages ;
	// ErrNotFound s'il n'y en avait pas.
	DeleteProxmoxHost(ctx context.Context, machineID string) error
	ListProxmoxHostIDs(ctx context.Context) ([]string, error)
	// ReplaceProxmoxReading remplace en une transaction ce que l'hôte avait ;
	// ErrNotFound s'il a été retiré entre-temps.
	ReplaceProxmoxReading(ctx context.Context, machineID string, reading Reading, at time.Time) error
	// NoteProxmoxFailure garde la clé du refus du dernier relevé, sans toucher
	// à ce que le dernier relevé réussi a montré.
	NoteProxmoxFailure(ctx context.Context, machineID, errorKey string) error
}

// Machines est ce que le composant lit des machines : celle qui porte l'hôte,
// et toutes, pour rapprocher les VM.
type Machines interface {
	Get(ctx context.Context, id string) (machine.Status, error)
	List(ctx context.Context) ([]machine.Status, error)
}

// Watcher reçoit chaque changement visible ; le direct s'y branche. nil est
// toléré.
type Watcher interface {
	ProxmoxChanged(machineID string)
}

type Service struct {
	store    Store
	machines Machines
	watcher  Watcher
	auditor  *audit.Recorder
	logger   *slog.Logger
	now      func() time.Time

	// reading ne laisse lire qu'un hôte à la fois, et ne laisse pas un relevé
	// qui a commencé avec l'ancienne connexion écrire après une redéclaration.
	reading sync.Mutex

	// Les gestes sur les invités : l'envoi des ordres, la lecture du dernier
	// relevé, et la vie du serveur que le suivi d'un ordre accompagne.
	orders     Orders
	viewer     Viewer
	lifetime   context.Context
	background sync.WaitGroup
}

func New(store Store, machines Machines, logger *slog.Logger) *Service {
	return &Service{store: store, machines: machines, logger: logger, now: time.Now, lifetime: context.Background()}
}

func (s *Service) SetWatcher(watcher Watcher)         { s.watcher = watcher }
func (s *Service) SetAuditor(auditor *audit.Recorder) { s.auditor = auditor }
func (s *Service) SetClock(now func() time.Time)      { s.now = now }

func (s *Service) changed(machineID string) {
	if s.watcher != nil {
		s.watcher.ProxmoxChanged(machineID)
	}
}

// Declare déclare l'hôte Proxmox d'une machine. Le jeton répond d'abord à
// /version : un jeton faux ou une adresse qui ne répond pas n'écrit rien. Le
// premier relevé suit tout de suite ; s'il échoue, l'hôte reste déclaré et
// sa fiche dit pourquoi.
func (s *Service) Declare(ctx context.Context, machineID string, definition Definition) error {
	host, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return err
	}
	definition, err = definition.Normalize()
	if err != nil {
		return err
	}
	credentials := Credentials{
		Host: Host{
			MachineID: machineID, APIURL: definition.APIURL, TokenID: definition.TokenID, Pin: definition.Pin,
		},
		TokenSecret: definition.TokenSecret,
	}
	s.reading.Lock()
	defer s.reading.Unlock()
	verifyCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	version, err := NewClient(credentials).Version(verifyCtx)
	if err != nil {
		return err
	}
	credentials.Version = version
	credentials.VerifiedAt = s.now()
	credentials.CreatedAt = credentials.VerifiedAt
	if err := s.store.SaveProxmoxHost(ctx, credentials); err != nil {
		return fmt.Errorf("save proxmox host: %w", err)
	}
	s.auditor.Record(ctx, setEvent(host.Machine, definition))
	s.changed(machineID)
	if err := s.refresh(ctx, machineID); err != nil {
		s.logRefresh(machineID, err)
	}
	return nil
}

// Remove retire l'hôte d'une machine, avec ce qu'il avait relevé. Les VM
// rapprochées de machines enrôlées n'y perdent rien d'autre que leur lien.
func (s *Service) Remove(ctx context.Context, machineID string) error {
	host, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return err
	}
	s.reading.Lock()
	defer s.reading.Unlock()
	if err := s.store.DeleteProxmoxHost(ctx, machineID); err != nil {
		return err
	}
	s.auditor.Record(ctx, unsetEvent(host.Machine))
	s.changed(machineID)
	return nil
}

// Refresh relit l'hôte d'une machine. Un échec de l'API est un refus : il est
// gardé sur l'hôte (last_error) et rendu ; ce qu'un relevé réussi avait
// montré reste affiché.
func (s *Service) Refresh(ctx context.Context, machineID string) error {
	s.reading.Lock()
	defer s.reading.Unlock()
	return s.refresh(ctx, machineID)
}

func (s *Service) refresh(ctx context.Context, machineID string) error {
	credentials, err := s.store.GetProxmoxCredentials(ctx, machineID)
	if err != nil {
		return err
	}
	reading, err := s.read(ctx, credentials)
	if err != nil {
		return s.noteFailure(ctx, machineID, err)
	}
	if err := s.prepare(ctx, machineID, &reading); err != nil {
		return err
	}
	err = s.store.ReplaceProxmoxReading(ctx, machineID, reading, s.now())
	if errors.Is(err, ErrNotFound) {
		// Retiré pendant le relevé : il n'y a plus rien à écrire.
		return nil
	}
	if err != nil {
		return fmt.Errorf("replace proxmox reading: %w", err)
	}
	s.changed(machineID)
	return nil
}

// read joue le relevé ; un secret vidé (clé de chiffrement perdue) est un
// jeton que l'API ne reconnaîtrait pas.
func (s *Service) read(ctx context.Context, credentials Credentials) (Reading, error) {
	if credentials.TokenSecret == "" {
		return Reading{}, ErrUnauthorized
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	return NewClient(credentials).Read(readCtx)
}

// prepare pose ce que le relevé de l'API ne sait pas : le nom du nœud de la
// machine et les machines enrôlées auxquelles les VM correspondent.
func (s *Service) prepare(ctx context.Context, machineID string, reading *Reading) error {
	host, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return fmt.Errorf("get machine %s: %w", machineID, err)
	}
	all, err := s.machines.List(ctx)
	if err != nil {
		return fmt.Errorf("list machines: %w", err)
	}
	reading.Node = hostNode(*reading, host.Hostname)
	link(reading.Guests, all, machineID)
	return nil
}

// noteFailure garde la clé du refus sur l'hôte et rend l'erreur. Ce qui n'est
// pas un refus (une panne de la base), ou un relevé que l'arrêt a coupé, ne
// s'écrit pas : l'hôte n'y est pour rien.
func (s *Service) noteFailure(ctx context.Context, machineID string, cause error) error {
	key, isRefusal := refusal.KeyOf(cause)
	if !isRefusal || ctx.Err() != nil {
		return cause
	}
	if err := s.store.NoteProxmoxFailure(ctx, machineID, key); err != nil && !errors.Is(err, ErrNotFound) {
		s.logger.Error("note proxmox failure", "machine_id", machineID, "error", err)
	}
	s.changed(machineID)
	return cause
}

// Watch relit chaque hôte au démarrage puis toutes les cinq minutes, jusqu'à
// l'annulation du contexte.
func (s *Service) Watch(ctx context.Context) {
	// Les suivis d'un geste sur un invité finissent avant que la base ne se ferme.
	defer s.background.Wait()
	s.refreshAll(ctx)
	ticker := time.NewTicker(watchPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshAll(ctx)
		}
	}
}

func (s *Service) refreshAll(ctx context.Context) {
	ids, err := s.store.ListProxmoxHostIDs(ctx)
	if err != nil {
		s.logger.Error("list proxmox hosts", "error", err)
		return
	}
	for _, machineID := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.Refresh(ctx, machineID); err != nil && !errors.Is(err, ErrNotFound) {
			s.logRefresh(machineID, err)
		}
	}
}

// logRefresh dit au journal un relevé qui a échoué. L'erreur ne porte jamais
// le jeton : il ne voyage que dans un en-tête.
func (s *Service) logRefresh(machineID string, err error) {
	if _, isRefusal := refusal.KeyOf(err); isRefusal {
		s.logger.Warn("proxmox read refused", "machine_id", machineID, "error", err)
		return
	}
	s.logger.Error("proxmox read failed", "machine_id", machineID, "error", err)
}

func setEvent(declared machine.Machine, definition Definition) audit.Event {
	details := audit.Details{"token_id": definition.TokenID, "pinned": "no"}
	if parsed, err := url.Parse(definition.APIURL); err == nil {
		details["host"] = parsed.Host
	}
	if definition.Pin != "" {
		details["pinned"] = "yes"
	}
	return audit.Event{Action: audit.MachineProxmoxSet, ObjectID: declared.ID, ObjectName: declared.Name, Details: details}
}

func unsetEvent(declared machine.Machine) audit.Event {
	return audit.Event{Action: audit.MachineProxmoxUnset, ObjectID: declared.ID, ObjectName: declared.Name}
}
