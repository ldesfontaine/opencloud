package dockerwatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

const (
	// Sans Docker, on ressonde la socket à ce rythme : le démon peut être
	// installé ou relancé sans que l'agent redémarre.
	DefaultProbeInterval = time.Minute
	// Après une coupure du flux d'événements, avant de resonder.
	reconnectDelay = 2 * time.Second
	// Un lot de journal part à cette cadence, ou plein avant.
	batchInterval = time.Second
	// Les lots attendent ici si le lecteur est lent ; au-delà, la lecture
	// du journal attend le lecteur.
	batchBacklog = 16
	// Le délai de docker stop quand le conteneur n'en fixe pas : celui du
	// démon.
	defaultStopTimeout = 10 * time.Second
	// Ce qu'on laisse en plus du délai entre la demande d'arrêt et le
	// « die » : un -t passé à docker stop ou à compose down peut l'allonger.
	stopRequestMargin = time.Minute
	// Un -t démesuré ne fait pas d'une panne tardive un arrêt demandé.
	maxStopTimeout = 10 * time.Minute
)

// Sink reçoit chaque rapport, dans l'ordre où le veilleur l'observe.
type Sink interface {
	Deliver(report container.Report)
}

type Options struct {
	Logger            *slog.Logger
	SampleInterval    time.Duration
	InventoryInterval time.Duration
	ProbeInterval     time.Duration
}

// Watcher est le veilleur ; un par machine.
type Watcher struct {
	client *dockerapi.Client
	sink   Sink
	logger *slog.Logger
	opts   Options

	mu sync.Mutex
	// Les compteurs de la mesure précédente, par conteneur : le processeur
	// est un delta.
	previous map[string]cpuCounters
	// Le dernier événement reçu : la reconnexion repart de là.
	since string
	// Ce qu'on a dit du démon la dernière fois ; on ne le redit que s'il
	// change.
	engine  *container.EngineReport
	follows int
	// L'instant de la dernière demande d'arrêt vue par conteneur : un
	// « die » 137 sans elle est un conteneur tué brutalement.
	stopRequests map[string]time.Time
	// Les conteneurs que ce veilleur a rapportés : seuls leurs journaux
	// s'ouvrent. Le serveur n'est pas cru sur un identifiant qu'il invente.
	known map[string]bool
	// Fermé au premier inventaire livré : avant lui, known est vide parce
	// que l'agent n'a encore rien vu, pas parce que le conteneur n'existe pas.
	firstInventory chan struct{}
}

func New(client *dockerapi.Client, sink Sink, opts Options) *Watcher {
	if opts.SampleInterval <= 0 {
		opts.SampleInterval = container.SampleInterval
	}
	if opts.InventoryInterval <= 0 {
		opts.InventoryInterval = container.InventoryInterval
	}
	if opts.ProbeInterval <= 0 {
		opts.ProbeInterval = DefaultProbeInterval
	}
	return &Watcher{client: client, sink: sink, logger: opts.Logger, opts: opts, previous: make(map[string]cpuCounters), stopRequests: make(map[string]time.Time), known: make(map[string]bool), firstInventory: make(chan struct{})}
}

// Run veille jusqu'à l'arrêt du contexte : sonde, puis inventaire, flux
// d'événements et mesures tant que le démon répond ; coupé, tout reprend
// à la sonde.
func (w *Watcher) Run(ctx context.Context) {
	// Les mesures ne dépendent pas de la vie du flux d'événements : un flux
	// qui retombe aussitôt rouvert (une ligne que le démon rejoue, par
	// exemple) ne doit pas les priver de leur cadence. Run attend leur fin :
	// ce que le puits écrit peut se fermer après Run, une mesure ne doit pas
	// y écrire encore.
	var sampling sync.WaitGroup
	sampling.Go(func() { w.sampleStats(ctx) })
	defer sampling.Wait()
	for ctx.Err() == nil {
		engine, err := w.client.Ping(ctx)
		if err != nil {
			// Une annulation n'est pas « Docker ne répond pas » : on part sans rien dire.
			if ctx.Err() != nil {
				return
			}
			w.reportEngine(engineReport(engine, err))
			if !wait.For(ctx, w.opts.ProbeInterval) {
				return
			}
			continue
		}
		w.reportEngine(container.EngineReport{Present: true, Version: engine.Version, APIVersion: engine.APIVersion})
		w.deliverInventory(ctx)
		if err := w.follow(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("docker event stream lost", "error", err)
		}
		if !wait.For(ctx, reconnectDelay) {
			return
		}
	}
}

// sampleStats mesure les conteneurs à chaque cadence tant que le démon
// répond, que le flux d'événements tienne ou non. Les mesures ne touchent
// pas aux fiches : les livrer en parallèle d'un événement n'en change pas
// l'ordre. L'inventaire, lui, reste dans follow, qui le sérialise avec les
// événements : un instantané plus vieux qu'un événement ne doit pas passer
// après lui.
func (w *Watcher) sampleStats(ctx context.Context) {
	samples := time.NewTicker(w.opts.SampleInterval)
	defer samples.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-samples.C:
			if w.engineIsUp() {
				w.deliverStats(ctx)
			}
		}
	}
}

func (w *Watcher) engineIsUp() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.engine != nil && w.engine.Present
}

func engineReport(engine dockerapi.Engine, err error) container.EngineReport {
	report := container.EngineReport{Version: engine.Version, APIVersion: engine.APIVersion}
	switch {
	case errors.Is(err, dockerapi.ErrNoSocket):
		report.Reason = container.ReasonNoSocket
	case errors.Is(err, dockerapi.ErrSocketDenied):
		report.Reason = container.ReasonDenied
	case errors.Is(err, dockerapi.ErrVersionTooOld):
		report.Reason = container.ReasonTooOld
	default:
		report.Reason = container.ReasonDown
	}
	return report
}

// reportEngine ne parle que si l'état du démon change : un Docker absent
// ne remplit pas le signal d'une machine sans Docker.
func (w *Watcher) reportEngine(report container.EngineReport) {
	w.mu.Lock()
	same := w.engine != nil && *w.engine == report
	w.engine = &report
	w.mu.Unlock()
	if same {
		return
	}
	w.sink.Deliver(container.Report{Engine: &report})
}

// rememberAll remplace l'ensemble des conteneurs rapportés : un inventaire
// complet dit tout ce qui existe, et rien d'autre.
func (w *Watcher) rememberAll(ids []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.known = make(map[string]bool, len(ids))
	for _, id := range ids {
		w.known[id] = true
	}
	select {
	case <-w.firstInventory:
	default:
		close(w.firstInventory)
	}
}

func (w *Watcher) remember(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.known[id] = true
}

func (w *Watcher) forget(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.known, id)
}

func (w *Watcher) isKnown(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.known[id]
}
