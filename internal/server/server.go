package server

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/settings"
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
	Signal(ctx context.Context, sessionToken string) error
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

type Server struct {
	logger         *slog.Logger
	version        string
	catalogs       lang.Catalogs
	settings       SettingsStore
	machines       MachineService
	heartbeats     HeartbeatService
	pingLimits     *pingLimits
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
		pingLimits:     newPingLimits(),
		publicURL:      opts.PublicURL,
		trustedProxies: opts.TrustedProxies,
		app:            app,
		clock:          opts.Clock,
		current:        current,
	}
	if opts.Clock != nil {
		server.pingLimits.setClock(opts.Clock)
	}
	server.handler = securityHeaders(server.mux())
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

func (s *Server) saveLanguage(code lang.Code) error {
	s.mu.Lock()
	s.current.Language = code
	snapshot := s.current
	s.mu.Unlock()
	return s.settings.Save(snapshot)
}
