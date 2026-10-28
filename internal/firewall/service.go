package firewall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// watchPeriod est le rythme des relevés : une règle changée se voit en
	// cinq minutes, sans que l'API du pare-feu soit sollicitée pour rien.
	watchPeriod = 5 * time.Minute
	// verifyTimeout borne la vérification d'une déclaration : un seul appel.
	verifyTimeout = 30 * time.Second
	// readTimeout borne tout le relevé d'un pare-feu : cinq appels.
	readTimeout = 2 * time.Minute
)

// Store est ce que le composant attend de la base ; le paquet store le
// fournit. Les lectures de la fiche passent par le Reader.
type Store interface {
	// GetFirewallCredentials rend le pare-feu avec son secret déchiffré,
	// ErrNotFound s'il n'existe pas.
	GetFirewallCredentials(ctx context.Context, id string) (Credentials, error)
	// CreateFirewall écrit un pare-feu neuf ; ErrNameTaken si le nom sert
	// déjà, ErrTooMany au plafond.
	CreateFirewall(ctx context.Context, credentials Credentials) error
	// UpdateFirewall remplace la connexion d'un pare-feu. Une autre adresse
	// d'API efface le relevé : il ne décrit plus le même appareil.
	UpdateFirewall(ctx context.Context, credentials Credentials) error
	// DeleteFirewall retire le pare-feu avec son relevé ; ErrNotFound s'il
	// n'y en avait pas.
	DeleteFirewall(ctx context.Context, id string) error
	ListFirewallIDs(ctx context.Context) ([]string, error)
	// ReplaceFirewallSnapshot remplace en une transaction ce que le pare-feu
	// avait ; ErrNotFound s'il a été retiré entre-temps.
	ReplaceFirewallSnapshot(ctx context.Context, id string, snapshot Snapshot, at time.Time) error
	// NoteFirewallFailure garde la clé du refus du dernier relevé, sans
	// toucher à ce que le dernier relevé réussi a montré.
	NoteFirewallFailure(ctx context.Context, id, errorKey string) error
}

// Watcher reçoit chaque changement visible ; le direct s'y branche. nil est
// toléré.
type Watcher interface {
	FirewallsChanged()
}

type Service struct {
	store      Store
	connectors map[string]Factory
	watcher    Watcher
	auditor    *audit.Recorder
	logger     *slog.Logger
	now        func() time.Time

	// reading ne laisse lire qu'un pare-feu à la fois, et ne laisse pas un
	// relevé qui a commencé avec l'ancienne connexion écrire après une
	// redéclaration.
	reading sync.Mutex
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, connectors: map[string]Factory{}, logger: logger, now: time.Now}
}

func (s *Service) SetWatcher(watcher Watcher)         { s.watcher = watcher }
func (s *Service) SetAuditor(auditor *audit.Recorder) { s.auditor = auditor }
func (s *Service) SetClock(now func() time.Time)      { s.now = now }

// SetConnector enseigne au service comment lire un genre de pare-feu.
func (s *Service) SetConnector(kind string, factory Factory) { s.connectors[kind] = factory }

func (s *Service) changed() {
	if s.watcher != nil {
		s.watcher.FirewallsChanged()
	}
}

// connectorFor construit le connecteur du genre déclaré ; un genre que le
// câblage n'a pas enseigné est refusé comme un genre inconnu.
func (s *Service) connectorFor(credentials Credentials) (Connector, error) {
	factory, known := s.connectors[credentials.Kind]
	if !known {
		return nil, ErrKindInvalid
	}
	return factory(credentials), nil
}

// Declare déclare un pare-feu. La paire de clés répond d'abord pour la
// version : une paire fausse ou une adresse qui ne répond pas n'écrit rien. Le
// premier relevé suit tout de suite ; s'il échoue, le pare-feu reste déclaré
// et sa fiche dit pourquoi. Rend l'identifiant.
func (s *Service) Declare(ctx context.Context, definition Definition) (string, error) {
	definition, err := definition.Normalize()
	if err != nil {
		return "", err
	}
	id, err := ident.New()
	if err != nil {
		return "", fmt.Errorf("draw firewall id: %w", err)
	}
	credentials := credentialsOf(id, definition)
	s.reading.Lock()
	defer s.reading.Unlock()
	if err := s.verify(ctx, &credentials); err != nil {
		return "", err
	}
	if err := s.store.CreateFirewall(ctx, credentials); err != nil {
		return "", err
	}
	s.auditor.Record(ctx, declareEvent(audit.FirewallCreate, credentials.Firewall))
	s.changed()
	s.readAfterDeclare(ctx, id)
	return id, nil
}

// Redeclare remplace la connexion d'un pare-feu, avec la même vérification.
func (s *Service) Redeclare(ctx context.Context, id string, definition Definition) error {
	definition, err := definition.Normalize()
	if err != nil {
		return err
	}
	s.reading.Lock()
	defer s.reading.Unlock()
	current, err := s.store.GetFirewallCredentials(ctx, id)
	if err != nil {
		return err
	}
	credentials := credentialsOf(id, definition)
	credentials.CreatedAt = current.CreatedAt
	if err := s.verify(ctx, &credentials); err != nil {
		return err
	}
	if err := s.store.UpdateFirewall(ctx, credentials); err != nil {
		return err
	}
	s.auditor.Record(ctx, declareEvent(audit.FirewallUpdate, credentials.Firewall))
	s.changed()
	s.readAfterDeclare(ctx, id)
	return nil
}

func credentialsOf(id string, definition Definition) Credentials {
	return Credentials{
		Firewall: Firewall{
			ID: id, Name: definition.Name, Kind: definition.Kind, APIURL: definition.APIURL,
			APIKey: definition.APIKey, Pin: definition.Pin,
		},
		APISecret: definition.APISecret,
	}
}

// verify fait répondre la paire de clés et pose la version et les dates.
func (s *Service) verify(ctx context.Context, credentials *Credentials) error {
	connector, err := s.connectorFor(*credentials)
	if err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	version, err := connector.Version(verifyCtx)
	if err != nil {
		return err
	}
	credentials.Version = version
	credentials.VerifiedAt = s.now()
	if credentials.CreatedAt.IsZero() {
		credentials.CreatedAt = credentials.VerifiedAt
	}
	return nil
}

func (s *Service) readAfterDeclare(ctx context.Context, id string) {
	if err := s.refresh(ctx, id); err != nil {
		s.logRefresh(id, err)
	}
}

// Remove retire un pare-feu, avec ce qu'il avait relevé. Le pare-feu lui-même
// n'est pas touché.
func (s *Service) Remove(ctx context.Context, id string) error {
	s.reading.Lock()
	defer s.reading.Unlock()
	current, err := s.store.GetFirewallCredentials(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteFirewall(ctx, id); err != nil {
		return err
	}
	s.auditor.Record(ctx, audit.Event{
		Action: audit.FirewallDelete, ObjectID: id, ObjectName: current.Name,
	})
	s.changed()
	return nil
}

// Refresh relit un pare-feu. Un échec de l'API est un refus : il est gardé sur
// le pare-feu (last_error) et rendu ; ce qu'un relevé réussi avait montré
// reste affiché.
func (s *Service) Refresh(ctx context.Context, id string) error {
	s.reading.Lock()
	defer s.reading.Unlock()
	return s.refresh(ctx, id)
}

func (s *Service) refresh(ctx context.Context, id string) error {
	credentials, err := s.store.GetFirewallCredentials(ctx, id)
	if err != nil {
		return err
	}
	snapshot, err := s.read(ctx, credentials)
	if err != nil {
		return s.noteFailure(ctx, id, err)
	}
	err = s.store.ReplaceFirewallSnapshot(ctx, id, snapshot, s.now())
	if errors.Is(err, ErrNotFound) {
		// Retiré pendant le relevé : il n'y a plus rien à écrire.
		return nil
	}
	if err != nil {
		return fmt.Errorf("replace firewall snapshot: %w", err)
	}
	s.changed()
	return nil
}

// read joue le relevé ; un secret vidé (clé de chiffrement perdue) est une
// paire que l'API ne reconnaîtrait pas.
func (s *Service) read(ctx context.Context, credentials Credentials) (Snapshot, error) {
	if credentials.APISecret == "" {
		return Snapshot{}, ErrUnauthorized
	}
	connector, err := s.connectorFor(credentials)
	if err != nil {
		return Snapshot{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	return Read(readCtx, connector)
}

// noteFailure garde la clé du refus sur le pare-feu et rend l'erreur. Ce qui
// n'est pas un refus (une panne de la base), ou un relevé que l'arrêt a coupé,
// ne s'écrit pas : le pare-feu n'y est pour rien.
func (s *Service) noteFailure(ctx context.Context, id string, cause error) error {
	key, isRefusal := refusal.KeyOf(cause)
	if !isRefusal || ctx.Err() != nil {
		return cause
	}
	if err := s.store.NoteFirewallFailure(ctx, id, key); err != nil && !errors.Is(err, ErrNotFound) {
		s.logger.Error("note firewall failure", "firewall_id", id, "error", err)
	}
	s.changed()
	return cause
}

// Watch relit chaque pare-feu au démarrage puis toutes les cinq minutes,
// jusqu'à l'annulation du contexte.
func (s *Service) Watch(ctx context.Context) {
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
	ids, err := s.store.ListFirewallIDs(ctx)
	if err != nil {
		s.logger.Error("list firewalls", "error", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.Refresh(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
			s.logRefresh(id, err)
		}
	}
}

// logRefresh dit au journal un relevé qui a échoué. L'erreur ne porte jamais
// la paire de clés : elle ne voyage que dans un en-tête.
func (s *Service) logRefresh(id string, err error) {
	if _, isRefusal := refusal.KeyOf(err); isRefusal {
		s.logger.Warn("firewall read refused", "firewall_id", id, "error", err)
		return
	}
	s.logger.Error("firewall read failed", "firewall_id", id, "error", err)
}

func declareEvent(action audit.Action, declared Firewall) audit.Event {
	details := audit.Details{"kind": declared.Kind, "pinned": "no"}
	if parsed, err := url.Parse(declared.APIURL); err == nil {
		details["host"] = parsed.Host
	}
	if declared.Pin != "" {
		details["pinned"] = "yes"
	}
	return audit.Event{Action: action, ObjectID: declared.ID, ObjectName: declared.Name, Details: details}
}
