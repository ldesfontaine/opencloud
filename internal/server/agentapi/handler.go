package agentapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/hostconn"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
	"github.com/ldesfontaine/opencloud/internal/restic"
	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Ce que l'agent attend du composant machine : l'enrôlement, la preuve, la
// session et son flux.
type MachineService interface {
	Enroll(ctx context.Context, request machine.Enrollment) (machine.Machine, error)
	Authenticate(ctx context.Context, proof machine.Proof) (machine.Machine, error)
	NoteSystem(ctx context.Context, machineID, system string) error
	NoteServerKey(ctx context.Context, agentID, fingerprint string) error
	NoteRootDir(ctx context.Context, agentID, root string) error
	NoteRollbackVersion(ctx context.Context, agentID, reported string) error
	NoteAnchors(ctx context.Context, agentID, reported string) error
	Connect(ctx context.Context, authenticated machine.Machine, address, agentVersion, capabilities, account, unavailable string) (*machine.Session, error)
	Disconnect(session *machine.Session)
	Signal(ctx context.Context, sessionToken string) (machineID string, err error)
	// Get rend la machine et l'agent : le serveur y relit les droits que
	// l'agent a rapportés, pour borner ce qu'il envoie.
	Get(ctx context.Context, id string) (machine.Status, error)
	SessionMachine(sessionToken string) (machineID string, err error)
}

// Ce que le signal écrit des ressources : les lectures faites depuis le
// précédent.
type ResourceRecorder interface {
	Record(ctx context.Context, machineID string, readings []sampler.Reading) error
}

// Ce que l'agent attend du composant conteneur : le signal écrit
// l'inventaire, les lots de journal répondent aux requêtes poussées.
type ContainerService interface {
	Record(ctx context.Context, machineID string, report container.Report) error
	DeliverLogs(ctx context.Context, machineID, requestID string, batch container.LogBatch) error
}

// Ce que l'agent attend du composant probe : le flux qui s'ouvre repart
// avec le jeu de sondes de sa machine, le signal écrit les résultats.
type ProbeService interface {
	Assign(ctx context.Context, machineID string)
	Record(ctx context.Context, machineID string, report probe.Report) error
}

// Ce que l'agent attend du composant update : le flux qui s'ouvre repart
// avec les images à ne pas interroger, le signal écrit les constats.
type UpdateService interface {
	Assign(ctx context.Context, machineID string)
	Record(ctx context.Context, machineID string, report update.Report) error
}

// Ce que l'agent attend du composant action : la clé publique du serveur,
// livrée à l'enrôlement, l'écriture de ce que l'agent a fait des ordres, et
// la clé neuve livrée à l'ouverture du flux pendant une rotation.
type ActionService interface {
	ServerKey() (public []byte, fingerprint string, err error)
	Record(ctx context.Context, machineID string, report agentwire.ActionReport) error
	Rekey(ctx context.Context, machineID string) error
	// OpenParcel ouvre un fichier d'un colis pour la machine qui le demande ;
	// action.ErrParcelNotFound sinon.
	OpenParcel(machineID, parcelID, name string) (*os.File, int64, error)
	// ReceiveParcel écrit le fichier que l'agent de cette machine téléverse
	// dans un colis qui l'attend ; action.ErrParcelNotFound sinon, et
	// action.ErrUpload* pour ce qu'il refuse.
	ReceiveParcel(uploaderID, parcelID, name string, content io.Reader, size int64, sum string) error
	// ConfirmAgentUpdate ferme la mise à jour que l'agent vient de finir : il
	// se reconnecte avec la version visée.
	ConfirmAgentUpdate(ctx context.Context, machineID, agentID, reported string) error
}

// Ce que l'agent attend du composant sshkeys : le signal écrit le relevé des
// clés SSH autorisées de sa machine.
type SSHKeysService interface {
	Record(ctx context.Context, machineID string, report sshkeys.Report) error
}

// Ce que l'agent attend du composant systemdunits : le signal écrit le relevé
// des unités systemd de sa machine.
type SystemdUnitsService interface {
	Record(ctx context.Context, machineID string, report systemdunits.Report) error
}

// Ce que l'agent attend du composant hostconn : le signal garde qui joint les
// ports de l'hôte de sa machine.
type HostConnectionsService interface {
	Record(ctx context.Context, machineID string, report hostconn.Report) error
}

// Ce que l'agent attend du composant role : le signal écrit ce que l'agent
// reconnaît sur sa machine.
type RolesService interface {
	Record(ctx context.Context, machineID string, report role.Report) error
}

// Ce que l'agent attend du composant machine : le signal écrit où tourne sa
// machine et ses disques.
type HostService interface {
	RecordHost(ctx context.Context, machineID string, report hostinfo.Report) error
}

// Ce que l'agent attend du composant restic : le signal écrit la section
// restic, et les résultats des ordres restic.backup (chemin pris, bilan).
type ResticService interface {
	RecordReport(ctx context.Context, machineID string, report agentwire.ResticReport) error
	RecordResults(ctx context.Context, machineID string, results []agentwire.ActionResult) error
}

// Ce que les routes du relais attendent du courtier (restic.Broker) : la
// requête de la source, le corps que la cible tire, la réponse qu'elle pousse.
type RelayBroker interface {
	Submit(ctx context.Context, proof machine.Proof, targetID string, request restic.RelayRequest) (restic.RelayResponse, error)
	TakeBody(targetID, id string) (io.Reader, error)
	Deliver(targetID, id string, head agentwire.ResticRelayHead, body io.Reader) error
}

type Options struct {
	Logger     *slog.Logger
	Machines   MachineService
	Resources  ResourceRecorder
	Containers ContainerService
	Probes     ProbeService
	Updates    UpdateService
	Actions    ActionService
	SSHKeys    SSHKeysService
	Systemd    SystemdUnitsService
	// HostConnections garde qui joint les ports de l'hôte.
	HostConnections HostConnectionsService
	Roles           RolesService
	Host            HostService
	Restic          ResticService
	Relay           RelayBroker
	// ClientAddress rend l'adresse du client telle que le serveur la résout,
	// mandataires de confiance compris : la règle vit à la racine.
	ClientAddress func(r *http.Request) string
	// Clock remplace l'horloge des limiteurs, pour les tests ; nil = time.Now.
	Clock func() time.Time
}

type Handler struct {
	logger        *slog.Logger
	machines      MachineService
	resources     ResourceRecorder
	containers    ContainerService
	probes        ProbeService
	updates       UpdateService
	actions       ActionService
	sshKeys       SSHKeysService
	systemd       SystemdUnitsService
	hostConns     HostConnectionsService
	roles         RolesService
	host          HostService
	restic        ResticService
	relay         RelayBroker
	clientAddress func(r *http.Request) string
	sourceLimit   *ratelimit.BySource
	signalLimit   *ratelimit.Limiter
	parcelLimit   *ratelimit.Limiter
	relayLimit    *ratelimit.BySource
}

func New(opts Options) *Handler {
	handler := &Handler{
		logger:        opts.Logger,
		machines:      opts.Machines,
		resources:     opts.Resources,
		containers:    opts.Containers,
		probes:        opts.Probes,
		updates:       opts.Updates,
		actions:       opts.Actions,
		sshKeys:       opts.SSHKeys,
		systemd:       opts.Systemd,
		hostConns:     opts.HostConnections,
		roles:         opts.Roles,
		host:          opts.Host,
		restic:        opts.Restic,
		relay:         opts.Relay,
		clientAddress: opts.ClientAddress,
		sourceLimit:   ratelimit.NewBySource(perSourceRate, perSourceBurst, sourceIdle),
		signalLimit:   ratelimit.New(signalPerMachineRate, signalPerMachineBurst, signalIdle),
		parcelLimit:   ratelimit.New(parcelPerMachineRate, parcelPerMachineBurst, parcelIdle),
		relayLimit:    newRelayLimiter(opts.Clock),
	}
	if opts.Clock != nil {
		handler.sourceLimit.SetClock(opts.Clock)
		handler.signalLimit.SetClock(opts.Clock)
		handler.parcelLimit.SetClock(opts.Clock)
	}
	return handler
}

// Routes déclare les routes de l'agent. Le serveur les monte à leur place
// dans son arbre, que server/routes_test.go fige.
func (h *Handler) Routes(add func(method, pattern string, handler http.HandlerFunc)) {
	add("POST", "/agent/enroll", h.enroll)
	add("GET", "/agent/stream", h.stream)
	add("POST", "/agent/signal", h.signal)
	add("POST", "/agent/logs/{request}", h.logs)
	add("GET", agentwire.ParcelRoute+"{parcel}/{name}", h.parcel)
	add("PUT", agentwire.ParcelRoute+"{parcel}/{name}", h.uploadParcel)
	// Le relais de restic : la source envoie chaque requête (toute méthode,
	// jugée par restic.Allow), la cible tire le corps et pousse la réponse.
	add("*", agentwire.ResticRelayRoute+"{target}/", h.relayRest)
	add("GET", agentwire.ResticRelayBodyRoute+"{id}", h.relayBody)
	add("POST", agentwire.ResticRelayResponseRoute+"{id}", h.relayResponse)
	// Tout le reste, dont /agent/challenge du protocole 1 : un agent d'une
	// autre version lit pourquoi il n'est pas servi.
	add("*", "/agent/", h.unknown)
}
