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
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
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
	Connect(ctx context.Context, authenticated machine.Machine, address, agentVersion, capabilities, account, unavailable string) (*machine.Session, error)
	Disconnect(session *machine.Session)
	Signal(ctx context.Context, sessionToken string) (machineID string, err error)
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

type Options struct {
	Logger     *slog.Logger
	Machines   MachineService
	Resources  ResourceRecorder
	Containers ContainerService
	Probes     ProbeService
	Updates    UpdateService
	Actions    ActionService
	SSHKeys    SSHKeysService
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
	clientAddress func(r *http.Request) string
	sourceLimit   *ratelimit.BySource
	signalLimit   *ratelimit.Limiter
	parcelLimit   *ratelimit.Limiter
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
		clientAddress: opts.ClientAddress,
		sourceLimit:   ratelimit.NewBySource(perSourceRate, perSourceBurst, sourceIdle),
		signalLimit:   ratelimit.New(signalPerMachineRate, signalPerMachineBurst, signalIdle),
		parcelLimit:   ratelimit.New(parcelPerMachineRate, parcelPerMachineBurst, parcelIdle),
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
	// Tout le reste, dont /agent/challenge du protocole 1 : un agent d'une
	// autre version lit pourquoi il n'est pas servi.
	add("*", "/agent/", h.unknown)
}
