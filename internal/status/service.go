package status

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/reader"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// Ce que le service attend de la base pour ses propres objets, composants
// et incidents ; le package store le fournit. Ce qu'il dérive des autres
// domaines (services, conteneurs, moniteurs, sondes, réglages) se lit par
// le reader.Reader, tel que leurs composants l'écrivent.
type Store interface {
	InsertComponent(ctx context.Context, component Component) error
	UpdateComponent(ctx context.Context, component Component) error
	ListComponents(ctx context.Context) ([]Component, error)
	GetComponent(ctx context.Context, id string) (Component, error)
	DeleteComponent(ctx context.Context, id string) error
	ComponentDeletionPlan(ctx context.Context, id string) (deletion.Plan, error)
	CountComponents(ctx context.Context) (int, error)

	InsertIncident(ctx context.Context, incident Incident) error
	UpdateIncident(ctx context.Context, incident Incident) error
	// Transition écrit le nouveau statut de l'incident et l'entrée du fil
	// dans la même transaction : le fil ne ment jamais sur l'état.
	Transition(ctx context.Context, incident Incident, update Update) error
	// ListIncidents rend les incidents ouverts, et les résolus depuis
	// l'instant donné ; zéro rend tout.
	ListIncidents(ctx context.Context, resolvedSince time.Time) ([]Incident, error)
	GetIncident(ctx context.Context, id string) (Incident, error)
	DeleteIncident(ctx context.Context, id string) error
	CountOpenIncidents(ctx context.Context) (int, error)
	PurgeIncidents(ctx context.Context, resolvedBefore time.Time) error
}

// Ce que le service attend du composant machine : qui est en ligne.
type Machines interface {
	List(ctx context.Context) ([]machine.Status, error)
}

// Ce que le service attend du composant des réglages : écrire les clés de
// la page, et rendre les réglages écrits.
type Settings interface {
	SetStatusPage(ctx context.Context, title, announcement string, language lang.Code) (settings.Settings, error)
}

// Watcher reçoit « la page a changé » : le bus interne pour l'administration,
// le bus public pour les visiteurs. nil est toléré.
type Watcher interface {
	StatusChanged()
}

// Feed est le bus interne, que le service écoute pour savoir quand relire :
// tout sujet peut changer l'état d'un composant.
type Feed interface {
	Subscribe() *live.Subscription
}

type Service struct {
	store    Store
	reader   reader.Reader
	machines Machines
	settings Settings
	watchers []Watcher
	feed     Feed
	logger   *slog.Logger
	now      func() time.Time

	// digest est l'empreinte du dernier instantané publié : on ne publie
	// que ce qui change de visible.
	mu     sync.Mutex
	digest string
	// creating tient compter-puis-insérer d'un seul tenant : deux créations
	// simultanées ne passent pas toutes deux sur la dernière place.
	creating sync.Mutex
}

func New(store Store, reader reader.Reader, machines Machines, settings Settings, logger *slog.Logger) *Service {
	return &Service{store: store, reader: reader, machines: machines, settings: settings, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) AddWatcher(watcher Watcher) {
	if watcher != nil {
		s.watchers = append(s.watchers, watcher)
	}
}

func (s *Service) SetFeed(feed Feed) {
	s.feed = feed
}
