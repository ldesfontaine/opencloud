package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/web"
)

// Ce que le serveur attend du composant qui mémorise les réglages.
type SettingsStore interface {
	Load() (settings.Settings, error)
	Save(settings.Settings) error
}

// Ce que le serveur attend du composant machine : l'API d'un côté,
// l'agent de l'autre.
type MachineService interface {
	List(ctx context.Context) ([]machine.Status, error)
	Get(ctx context.Context, id string) (machine.Status, error)
	Count(ctx context.Context) (total, online int, err error)
	CreateToken(ctx context.Context, name string) (string, machine.Token, error)
	CreateReenrollToken(ctx context.Context, machineID string) (string, machine.Token, error)
	PendingTokens(ctx context.Context) ([]machine.Token, error)
	CancelToken(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	Enroll(ctx context.Context, request machine.Enrollment) (machine.Machine, error)
	Challenge(ctx context.Context, machineID string) ([]byte, error)
	Authenticate(ctx context.Context, proof machine.Proof) (machine.Machine, error)
	Connect(ctx context.Context, machineID, address, agentVersion string) (*machine.Session, error)
	Disconnect(session *machine.Session)
	Signal(ctx context.Context, sessionToken string) (machineID string, err error)
	Command(machineID, name string, payload any) error
}

// Ce que le serveur attend du composant service : l'API lit, le signal de
// l'agent écrit, les journaux passent dans les deux sens.
type ServiceTracker interface {
	Record(ctx context.Context, machineID string, report service.Report) error
	List(ctx context.Context, machineID string) ([]service.Service, error)
	Get(ctx context.Context, id string) (service.Service, error)
	Transitions(ctx context.Context, id string, limit int) ([]service.Transition, error)
	Engines(ctx context.Context) ([]service.Engine, error)
	Topology(ctx context.Context, machineID string) (service.Topology, error)
	CurrentAll(ctx context.Context) ([]service.Current, error)
	Count(ctx context.Context) (total, attention int, err error)
	FollowLogs(ctx context.Context, serviceID string, tail int) (<-chan service.LogBatch, error)
	FetchLogs(ctx context.Context, serviceID string, tail int) ([]service.LogLine, error)
	DeliverLogs(ctx context.Context, requestID string, batch service.LogBatch) bool
}

// Ce que le serveur attend du composant resource : l'API lit, le signal de
// l'agent écrit.
type ResourceService interface {
	Record(ctx context.Context, machineID string, readings []sampler.Reading) error
	Current(ctx context.Context, machineID string) (resource.Current, error)
	CurrentAll(ctx context.Context) ([]resource.Current, error)
	History(ctx context.Context, machineID, window string) (resource.Window, []sampler.Reading, error)
}

// Ce que le serveur attend du composant probe : l'API lit et agit, le
// signal de l'agent écrit, et le flux qui s'ouvre repart avec le jeu de
// sondes de sa machine.
type ProbeService interface {
	List(ctx context.Context, machineID string) ([]probe.Probe, error)
	Get(ctx context.Context, id string) (probe.Probe, error)
	Count(ctx context.Context) (total, attention int, err error)
	// Certificates compte les certificats vus et ceux qui approchent de
	// leur fin, pour la vue d'ensemble.
	Certificates(ctx context.Context) (probe.Certificates, error)
	Create(ctx context.Context, definition probe.Definition) (probe.Probe, error)
	Delete(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Results(ctx context.Context, id string, limit int) ([]probe.Result, error)
	Days(ctx context.Context, id string, span time.Duration) ([]probe.Day, error)
	Uptimes(ctx context.Context, id string) ([]probe.Uptime, error)
	Assign(ctx context.Context, machineID string)
	Record(ctx context.Context, machineID string, report probe.Report) error
}

// Ce que le serveur attend du composant heartbeat : l'API d'un côté,
// les pings publics de l'autre.
type HeartbeatService interface {
	List(ctx context.Context) ([]heartbeat.Heartbeat, error)
	Get(ctx context.Context, id string) (heartbeat.Heartbeat, error)
	Count(ctx context.Context) (total, attention int, err error)
	Create(ctx context.Context, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	Delete(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Pings(ctx context.Context, id string, limit int) ([]heartbeat.Ping, error)
	Runs(ctx context.Context, id string, limit int) ([]heartbeat.Run, error)
	Receive(ctx context.Context, token string, ping heartbeat.Ping) (heartbeat.Heartbeat, error)
}

// Ce que le serveur attend du composant statut : l'instantané public d'un
// côté, l'administration des composants, des incidents et de la page de
// l'autre.
type StatusService interface {
	Snapshot(ctx context.Context) (status.Snapshot, error)
	Page() (status.Page, error)
	SetPage(ctx context.Context, page status.Page) (status.Page, error)
	Components(ctx context.Context) ([]status.Component, error)
	GetComponent(ctx context.Context, id string) (status.Component, error)
	CreateComponent(ctx context.Context, definition status.ComponentDefinition) (status.Component, error)
	UpdateComponent(ctx context.Context, id string, definition status.ComponentDefinition) (status.Component, error)
	DeleteComponent(ctx context.Context, id string) error
	Incidents(ctx context.Context) ([]status.Incident, error)
	GetIncident(ctx context.Context, id string) (status.Incident, error)
	OpenIncident(ctx context.Context, definition status.IncidentDefinition) (status.Incident, error)
	AddUpdate(ctx context.Context, id string, next status.IncidentStatus, message string) (status.Incident, error)
	ChangeIncident(ctx context.Context, id string, change status.IncidentChange) (status.Incident, error)
	DeleteIncident(ctx context.Context, id string) error
	CountOpenIncidents(ctx context.Context) (int, error)
}

// Ce que le serveur attend du bus du direct : un abonnement par onglet, et
// le compte pour plafonner. Le bus public, qui ne porte que « status »,
// est du même type.
type Live interface {
	Subscribe() *live.Subscription
	Count() int
}

type Server struct {
	logger         *slog.Logger
	version        string
	catalogs       lang.Catalogs
	settings       SettingsStore
	machines       MachineService
	heartbeats     HeartbeatService
	resources      ResourceService
	services       ServiceTracker
	probes         ProbeService
	status         StatusService
	live           Live
	publicLive     Live
	logStreams     atomic.Int32
	pingLimits     *pingLimits
	statusLimits   *statusLimits
	publicURL      string
	trustedProxies []netip.Prefix
	// Le front compilé, embarqué : voir spa.go.
	app     fs.FS
	handler http.Handler
	clock   func() time.Time

	// Les réglages courants, relus à chaque requête, réécrits à chaque changement.
	mu      sync.RWMutex
	current settings.Settings
}

type Options struct {
	Logger     *slog.Logger
	Version    string
	Settings   SettingsStore
	Machines   MachineService
	Heartbeats HeartbeatService
	Resources  ResourceService
	Services   ServiceTracker
	Probes     ProbeService
	Status     StatusService
	Live       Live
	// PublicLive est le bus des visiteurs de la page de statut : à part,
	// pour que leurs connexions ne comptent pas parmi les onglets.
	PublicLive Live
	// Adresse publique d'openCloud pour la commande d'installation ; vide :
	// déduite de la requête.
	PublicURL      string
	TrustedProxies []netip.Prefix
	// Clock remplace l'horloge, pour les tests ; nil = time.Now.
	Clock func() time.Time
}

func New(opts Options) (*Server, error) {
	catalogs, err := lang.Load()
	if err != nil {
		return nil, fmt.Errorf("load languages: %w", err)
	}
	current, err := opts.Settings.Load()
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	app, err := web.Dist()
	if err != nil {
		return nil, fmt.Errorf("open front: %w", err)
	}
	// La table MIME de Go ignore woff2 ; sans cela le navigateur reçoit un
	// octet-stream et certains refusent la police.
	if err := mime.AddExtensionType(".woff2", "font/woff2"); err != nil {
		return nil, fmt.Errorf("register woff2 type: %w", err)
	}
	server := &Server{
		logger:         opts.Logger,
		version:        opts.Version,
		catalogs:       catalogs,
		settings:       opts.Settings,
		machines:       opts.Machines,
		heartbeats:     opts.Heartbeats,
		resources:      opts.Resources,
		services:       opts.Services,
		probes:         opts.Probes,
		status:         opts.Status,
		live:           opts.Live,
		publicLive:     opts.PublicLive,
		pingLimits:     newPingLimits(),
		statusLimits:   newStatusLimits(),
		publicURL:      opts.PublicURL,
		trustedProxies: opts.TrustedProxies,
		app:            app,
		clock:          opts.Clock,
		current:        current,
	}
	if opts.Clock != nil {
		server.pingLimits.setClock(opts.Clock)
		server.statusLimits.setClock(opts.Clock)
	}
	server.handler = server.chain(securityHeaders(server.mux()))
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// La langue de l'interface : celle du réglage, sinon le français.
func (s *Server) language() lang.Code {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current.Language == "" {
		return lang.Default
	}
	return s.current.Language
}

// saveLanguage relit le fichier avant d'écrire : la page de statut y tient
// ses propres réglages, qu'une copie en mémoire écraserait.
func (s *Server) saveLanguage(code lang.Code) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.settings.Load()
	if err != nil {
		return err
	}
	current.Language = code
	if err := s.settings.Save(current); err != nil {
		return err
	}
	s.current = current
	return nil
}

// Command fait du serveur le commandeur des agents : c'est lui qui tient
// leurs flux. Une machine sans flux est hors ligne pour le relais.
func (s *Server) Command(machineID, name string, payload any) error {
	err := s.machines.Command(machineID, name, payload)
	if errors.Is(err, machine.ErrNotConnected) {
		return service.ErrMachineOffline
	}
	return err
}
