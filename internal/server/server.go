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
	"sync/atomic"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/update"
	"github.com/ldesfontaine/opencloud/web"
)

// Ce que le serveur attend de la base, qui mémorise les réglages.
type SettingsStore interface {
	LoadSettings(ctx context.Context) (settings.Settings, error)
	UpdateSettings(ctx context.Context, change func(*settings.Settings)) (settings.Settings, error)
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
	NoteSystem(ctx context.Context, machineID, system string) error
	WarnIfOpenCloudHost(machine machine.Machine, fingerprint string)
	Disconnect(session *machine.Session)
	Signal(ctx context.Context, sessionToken string) (machineID string, err error)
	SessionMachine(sessionToken string) (machineID string, err error)
	Command(machineID, name string, payload any) error
}

// Ce que le serveur attend du composant conteneur : l'API lit, le signal de
// l'agent écrit, les journaux passent dans les deux sens.
type ContainerTracker interface {
	Record(ctx context.Context, machineID string, report container.Report) error
	List(ctx context.Context, machineID string) ([]container.Container, error)
	Get(ctx context.Context, id string) (container.Container, error)
	Transitions(ctx context.Context, id string, limit int) ([]container.Transition, error)
	Engines(ctx context.Context) ([]container.Engine, error)
	Topology(ctx context.Context, machineID string) (container.Topology, error)
	CurrentAll(ctx context.Context) ([]container.Current, error)
	Count(ctx context.Context) (total, attention int, err error)
	FollowLogs(ctx context.Context, containerID string, tail int) (<-chan container.LogBatch, error)
	FetchLogs(ctx context.Context, containerID string, tail int) ([]container.LogLine, error)
	DeliverLogs(ctx context.Context, machineID, requestID string, batch container.LogBatch) bool
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
	Update(ctx context.Context, id string, definition probe.Definition) (probe.Probe, error)
	Delete(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Results(ctx context.Context, id string, limit int) ([]probe.Result, error)
	Days(ctx context.Context, id string, span time.Duration) ([]probe.Day, error)
	Uptimes(ctx context.Context, id string) ([]probe.Uptime, error)
	Assign(ctx context.Context, machineID string)
	Record(ctx context.Context, machineID string, report probe.Report) error
}

// Ce que le serveur attend de l'annuaire : l'API lit et écrit les services
// que l'opérateur déclare, et leurs projets.
type DirectoryService interface {
	ListServices(ctx context.Context, filter directory.Filter) ([]directory.Service, error)
	GetService(ctx context.Context, id string) (directory.Service, error)
	CreateService(ctx context.Context, definition directory.ServiceDefinition) (directory.Service, error)
	UpdateService(ctx context.Context, id string, definition directory.ServiceDefinition) (directory.Service, error)
	// DeleteService refuse avec un *directory.LinkedError tant que des
	// sondes ou des composants de statut visent le service.
	DeleteService(ctx context.Context, id string) error
	ServiceLinks(ctx context.Context, id string) (directory.Links, error)
	Review(ctx context.Context, id string) (directory.Service, error)
	SetUnitUpdatePolicy(ctx context.Context, unitID string, policy container.UpdatePolicy) (directory.Service, error)
	ListProjects(ctx context.Context) ([]directory.Project, error)
	GetProject(ctx context.Context, id string) (directory.Project, error)
	CreateProject(ctx context.Context, definition directory.ProjectDefinition) (directory.Project, error)
	UpdateProject(ctx context.Context, id string, definition directory.ProjectDefinition) (directory.Project, error)
	DeleteProject(ctx context.Context, id string) error
	// L'écart entre ce qui est déclaré et ce que les machines montrent, et
	// ce qui en découle : adopter un conteneur, ignorer une clé, dire à quoi
	// se rattache une fiche.
	Gaps(ctx context.Context, filter directory.Filter) (directory.GapReport, error)
	Propose(ctx context.Context, containerID string) (directory.Proposal, error)
	Ignore(ctx context.Context, key directory.IgnoredKey) error
	Unignore(ctx context.Context, key directory.IgnoredKey) error
	ContainerLink(ctx context.Context, item container.Container) (directory.ContainerLink, error)
}

// Ce que le serveur attend du composant manifest : le fichier de l'annuaire,
// de l'export au journal des passages.
type ManifestService interface {
	// Export écrit tout l'annuaire, commentaires dans la langue donnée par
	// les annotations, avec en plus des propositions si les options le disent.
	// Jamais un secret.
	Export(ctx context.Context, annotations manifest.Annotations, options manifest.ExportOptions) ([]byte, error)
	// Plan dit ce que le fichier changerait, sans rien écrire.
	Plan(ctx context.Context, request manifest.Request) (manifest.Plan, error)
	// Apply écrit ce que le fichier demande si le plan est celui que
	// l'opérateur a confirmé (request.PlanHash). Sur ErrPlanChanged et
	// ErrNotApplicable, le plan rendu est celui d'aujourd'hui.
	Apply(ctx context.Context, request manifest.Request, actor manifest.Actor) (manifest.Apply, manifest.Plan, error)
	Applies(ctx context.Context, limit int) ([]manifest.Apply, error)
	GetApply(ctx context.Context, id string) (manifest.Apply, error)
}

// Ce que le serveur attend du composant update : l'API lit et règle, le
// signal de l'agent écrit, et le flux qui s'ouvre repart avec les
// exclusions de sa machine.
type UpdateService interface {
	Record(ctx context.Context, machineID string, report update.Report) error
	Checks(ctx context.Context, machineID string) (map[string]update.Check, error)
	Count(ctx context.Context) (int, error)
	CheckNow(ctx context.Context, machineID string) error
	Assign(ctx context.Context, machineID string)
}

// Ce que le serveur attend du composant lifecycle : la fiche lit le support
// d'une image, Paramètres lit l'état de la dernière lecture et en demande
// une quand le réglage vient d'être activé.
type LifecycleService interface {
	Of(image string) lifecycle.Verdict
	OfSystem(system string) lifecycle.Verdict
	State() lifecycle.State
	Kick()
}

// Ce que le serveur attend du composant heartbeat : l'API d'un côté,
// les pings publics de l'autre.
type HeartbeatService interface {
	List(ctx context.Context) ([]heartbeat.Heartbeat, error)
	Get(ctx context.Context, id string) (heartbeat.Heartbeat, error)
	Count(ctx context.Context) (total, attention int, err error)
	Create(ctx context.Context, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	Update(ctx context.Context, id string, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	RegenerateToken(ctx context.Context, id string) error
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
	Page(ctx context.Context) (status.Page, error)
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

// Ce que le serveur attend du moteur des alertes : la page, l'acquittement,
// les canaux et les silences.
type AlertService interface {
	List(ctx context.Context, status alert.Status) ([]alert.Alert, error)
	Get(ctx context.Context, id int64) (alert.Alert, error)
	Deliveries(ctx context.Context, alertID int64) ([]alert.Delivery, error)
	Acknowledge(ctx context.Context, id int64) (alert.Alert, error)
	AcknowledgeMany(ctx context.Context, ids []int64) (int, error)
	Count(ctx context.Context) (alert.Counts, error)
	Channels(ctx context.Context) ([]alert.Channel, error)
	GetChannel(ctx context.Context, id int64) (alert.Channel, error)
	CreateChannel(ctx context.Context, definition alert.ChannelDefinition) (alert.Channel, error)
	UpdateChannel(ctx context.Context, id int64, definition alert.ChannelDefinition) (alert.Channel, error)
	DeleteChannel(ctx context.Context, id int64) error
	Silences(ctx context.Context) ([]alert.Silence, error)
	CreateSilence(ctx context.Context, definition alert.SilenceDefinition) (alert.Silence, error)
	DeleteSilence(ctx context.Context, id int64) error
}

// Ce que le serveur attend du notifieur : tester un canal, ou le relais
// d'e-mails, tout de suite.
type Notifier interface {
	Test(ctx context.Context, channel alert.Channel) (alert.Outcome, error)
	TestMail(ctx context.Context, recipient string) (alert.Outcome, error)
}

// Ce que le serveur attend du bus du direct : un abonnement par onglet. Le
// bus public, qui ne porte que « status », est du même type.
type Live interface {
	Subscribe() *live.Subscription
}

type Server struct {
	logger       *slog.Logger
	version      string
	catalogs     lang.Catalogs
	settings     SettingsStore
	machines     MachineService
	heartbeats   HeartbeatService
	resources    ResourceService
	containers   ContainerTracker
	probes       ProbeService
	directory    DirectoryService
	manifest     ManifestService
	updates      UpdateService
	lifecycle    LifecycleService
	status       StatusService
	alerts       AlertService
	notifier     Notifier
	mcp          MCPService
	auth         AuthService
	live         Live
	publicLive   Live
	logStreams   atomic.Int32
	pingLimits   *pingLimits
	statusLimits *statusLimits
	mcpLimits    *mcpLimits
	agentLimits  *challengeLimits
	authLimits   *authLimits
	// Les onglets en direct, administration et page publique.
	eventTabs  *slots
	publicTabs *slots
	// Le serveur MCP et son transport HTTP, montés une fois ; voir mcp.go.
	mcpServer  *gomcp.Server
	mcpHandler http.Handler
	// La socket de commande ne sert que les routes du fichier de l'annuaire.
	controlHandler http.Handler
	configPath     string
	publicURL      string
	trustedProxies []netip.Prefix
	listen         string
	// Le front compilé, embarqué : voir spa.go.
	app     fs.FS
	handler http.Handler
	clock   func() time.Time
}

type Options struct {
	Logger     *slog.Logger
	Version    string
	Settings   SettingsStore
	Machines   MachineService
	Heartbeats HeartbeatService
	Resources  ResourceService
	Containers ContainerTracker
	Probes     ProbeService
	Directory  DirectoryService
	Manifest   ManifestService
	Updates    UpdateService
	Lifecycle  LifecycleService
	Status     StatusService
	Alerts     AlertService
	Notifier   Notifier
	MCP        MCPService
	// Auth garde l'interface de l'opérateur ; voir auth.go.
	Auth AuthService
	Live Live
	// PublicLive est le bus des visiteurs de la page de statut : à part,
	// pour que leurs connexions ne comptent pas parmi les onglets.
	PublicLive Live
	// Adresse publique d'openCloud pour la commande d'installation ; vide :
	// déduite de la requête.
	PublicURL      string
	TrustedProxies []netip.Prefix
	// Listen est l'adresse d'écoute : son hôte est un Host reconnu.
	Listen string
	// ConfigPath est le fichier de configuration de ce serveur, cité dans
	// la commande du client MCP local.
	ConfigPath string
	// Clock remplace l'horloge, pour les tests ; nil = time.Now.
	Clock func() time.Time
}

func New(opts Options) (*Server, error) {
	catalogs, err := lang.Load()
	if err != nil {
		return nil, fmt.Errorf("load languages: %w", err)
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
		containers:     opts.Containers,
		probes:         opts.Probes,
		directory:      opts.Directory,
		manifest:       opts.Manifest,
		updates:        opts.Updates,
		lifecycle:      opts.Lifecycle,
		status:         opts.Status,
		alerts:         opts.Alerts,
		notifier:       opts.Notifier,
		mcp:            opts.MCP,
		auth:           opts.Auth,
		live:           opts.Live,
		publicLive:     opts.PublicLive,
		pingLimits:     newPingLimits(),
		statusLimits:   newStatusLimits(),
		mcpLimits:      newMCPLimits(),
		agentLimits:    newChallengeLimits(),
		authLimits:     newAuthLimits(),
		eventTabs:      newSlots(maxEventSubscribers),
		publicTabs:     newSlots(maxPublicSubscribers),
		configPath:     opts.ConfigPath,
		publicURL:      opts.PublicURL,
		trustedProxies: opts.TrustedProxies,
		listen:         opts.Listen,
		app:            app,
		clock:          opts.Clock,
	}
	if opts.Clock != nil {
		server.pingLimits.setClock(opts.Clock)
		server.statusLimits.setClock(opts.Clock)
		server.mcpLimits.setClock(opts.Clock)
		server.agentLimits.setClock(opts.Clock)
		server.authLimits.setClock(opts.Clock)
	}
	server.mcpServer = server.newMCPServer(false)
	server.mcpHandler = server.newMCPHandler()
	server.handler = server.chain(securityHeaders(server.mux()))
	server.controlHandler = server.chain(server.controlMux())
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// now est l'horloge du serveur : celle des tests quand ils en donnent une.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// language rend la langue de l'interface : celle du réglage, sinon le
// français. Les réglages se relisent dans la base à chaque usage, jamais
// d'une copie : un autre processus, ou la reprise de settings.toml, a pu
// les changer.
func (s *Server) language(ctx context.Context) (lang.Code, error) {
	current, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	if current.Language == "" {
		return lang.Default, nil
	}
	return current.Language, nil
}

// saveLanguage ne touche que la langue : la page de statut tient ses
// propres clés sur la même ligne, et UpdateSettings les préserve.
func (s *Server) saveLanguage(ctx context.Context, code lang.Code) error {
	_, err := s.settings.UpdateSettings(ctx, func(current *settings.Settings) {
		current.Language = code
	})
	return err
}

// Command fait du serveur le commandeur des agents : c'est lui qui tient
// leurs flux. Une machine sans flux est hors ligne pour le relais ; une
// file pleine est un refus, pas une panne.
func (s *Server) Command(machineID, name string, payload any) error {
	err := s.machines.Command(machineID, name, payload)
	if errors.Is(err, machine.ErrNotConnected) {
		return container.ErrMachineOffline
	}
	if errors.Is(err, machine.ErrBusy) {
		// Les sondes lisent leur propre sentinelle : elles n'importent pas
		// machine, qui les importe.
		return fmt.Errorf("command %s: %w: %w", name, machine.ErrBusy, probe.ErrCommandBusy)
	}
	return err
}
