package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// Catalog est ce que runner attend du catalogue : la liste pour l'interface,
// la définition d'une action, et sa préparation à partir de paramètres bruts.
// Prepare rend un refusal.Refusal quand un paramètre ne passe pas.
type Catalog interface {
	Definitions() []catalog.Definition
	Lookup(kind catalog.Kind) (catalog.Definition, bool)
	Prepare(kind catalog.Kind, params map[string]string) (catalog.Prepared, error)
}

// Transports rend le transport d'une machine. ErrNotEnrolled dit que la
// machine n'a pas encore de compte ni de clé : rien ne peut partir.
type Transports interface {
	For(ctx context.Context, machine store.Machine) (transport.Transport, error)
}

var (
	// ErrNotEnrolled : la machine n'est pas enrôlée. Le geste qui le lève est
	// « sudo opencloud enroll-local » sur la machine openCloud.
	ErrNotEnrolled = errors.New("machine not enrolled")
	// ErrClosed : le runner s'arrête, plus rien n'entre dans les files.
	ErrClosed = errors.New("runner closed")
)

const (
	// Réessais après une machine injoignable : on ne sait rien pour l'instant,
	// l'information reviendra avec la machine (05-execution.md).
	defaultFollowGrace = 5 * time.Minute

	// Un abonné qui ne lit pas assez vite est fermé plutôt que troué : le
	// navigateur se reconnecte et rejoue depuis le stockage.
	subscriberBuffer = 512
)

type Runner struct {
	store      *store.Store
	catalog    Catalog
	transports Transports
	logger     *slog.Logger

	// Le contexte de vie des exécuteurs : Close l'annule, chacun s'arrête
	// entre deux actions ou pendant un suivi, sans rien conclure.
	lifetime context.Context
	stop     context.CancelFunc
	workers  sync.WaitGroup

	// Champs de rythme, remplacés par les tests pour ne pas attendre.
	retryDelays []time.Duration
	followGrace time.Duration

	mu          sync.Mutex
	closed      bool
	queues      map[string]*machineQueue
	subscribers map[string]map[*subscriber]struct{}
}

func New(database *store.Store, actionCatalog Catalog, transports Transports, logger *slog.Logger) *Runner {
	lifetime, stop := context.WithCancel(context.Background())
	return &Runner{
		store:       database,
		catalog:     actionCatalog,
		transports:  transports,
		logger:      logger,
		lifetime:    lifetime,
		stop:        stop,
		retryDelays: []time.Duration{10 * time.Second, 30 * time.Second, time.Minute},
		followGrace: defaultFollowGrace,
		queues:      map[string]*machineQueue{},
		subscribers: map[string]map[*subscriber]struct{}{},
	}
}

// Close arrête les exécuteurs et ferme les abonnements. Une action en cours
// n'est pas conclue : elle reste « running » et la reprise la retrouvera.
func (r *Runner) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()

	r.stop()
	r.workers.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	for actionID, subscribers := range r.subscribers {
		for current := range subscribers {
			current.close()
		}
		delete(r.subscribers, actionID)
	}
}

// Les lectures du journal, telles que l'interface les demande. Elles passent
// par le runner pour que web n'ait qu'une dépendance sur les actions.
func (r *Runner) Action(ctx context.Context, id string) (store.Action, error) {
	return r.store.Action(ctx, id)
}

func (r *Runner) ActionsForMachine(ctx context.Context, machineID string, limit int) ([]store.Action, error) {
	return r.store.ActionsForMachine(ctx, machineID, limit)
}

func (r *Runner) Lines(ctx context.Context, actionID string, afterSeq int64) ([]store.ActionLine, error) {
	return r.store.Lines(ctx, actionID, afterSeq)
}

func (r *Runner) retryDelay(attempt int) time.Duration {
	if attempt >= len(r.retryDelays) {
		return r.retryDelays[len(r.retryDelays)-1]
	}
	return r.retryDelays[attempt]
}
