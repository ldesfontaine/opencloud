package agentapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Ce que l'agent attend du composant machine : l'enrôlement, la preuve, la
// session et son flux.
type MachineService interface {
	Enroll(ctx context.Context, request machine.Enrollment) (machine.Machine, error)
	Challenge(ctx context.Context, machineID string) ([]byte, error)
	Authenticate(ctx context.Context, proof machine.Proof) (machine.Machine, error)
	NoteSystem(ctx context.Context, machineID, system string) error
	Connect(ctx context.Context, machineID, address, agentVersion string) (*machine.Session, error)
	WarnIfOpenCloudHost(machine machine.Machine, fingerprint string)
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

type Options struct {
	Logger     *slog.Logger
	Machines   MachineService
	Resources  ResourceRecorder
	Containers ContainerService
	Probes     ProbeService
	Updates    UpdateService
	// ClientAddress rend l'adresse du client telle que le serveur la résout,
	// mandataires de confiance compris : la règle vit à la racine.
	ClientAddress func(r *http.Request) string
	// Clock remplace l'horloge des limiteurs, pour les tests ; nil = time.Now.
	Clock func() time.Time
}

type Handler struct {
	logger          *slog.Logger
	machines        MachineService
	resources       ResourceRecorder
	containers      ContainerService
	probes          ProbeService
	updates         UpdateService
	clientAddress   func(r *http.Request) string
	challengeLimits *challengeLimits
	signalLimits    *signalLimits
}

func New(opts Options) *Handler {
	handler := &Handler{
		logger:          opts.Logger,
		machines:        opts.Machines,
		resources:       opts.Resources,
		containers:      opts.Containers,
		probes:          opts.Probes,
		updates:         opts.Updates,
		clientAddress:   opts.ClientAddress,
		challengeLimits: newChallengeLimits(),
		signalLimits:    newSignalLimits(),
	}
	if opts.Clock != nil {
		handler.challengeLimits.setClock(opts.Clock)
		handler.signalLimits.setClock(opts.Clock)
	}
	return handler
}

// Routes déclare les routes de l'agent. Le serveur les monte à leur place
// dans son arbre, que server/routes_test.go fige.
func (h *Handler) Routes(add func(method, pattern string, handler http.HandlerFunc)) {
	add("POST", "/agent/enroll", h.enroll)
	add("POST", "/agent/challenge", h.challenge)
	add("GET", "/agent/stream", h.stream)
	add("POST", "/agent/signal", h.signal)
	add("POST", "/agent/logs/{request}", h.logs)
}
