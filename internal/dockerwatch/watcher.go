package dockerwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/service"
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
)

// Sink reçoit chaque rapport, dans l'ordre où le veilleur l'observe.
type Sink interface {
	Deliver(report service.Report)
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
	engine  *service.EngineReport
	follows int
}

type cpuCounters struct {
	total  uint64
	system uint64
}

func New(client *dockerapi.Client, sink Sink, opts Options) *Watcher {
	if opts.SampleInterval <= 0 {
		opts.SampleInterval = service.SampleInterval
	}
	if opts.InventoryInterval <= 0 {
		opts.InventoryInterval = service.InventoryInterval
	}
	if opts.ProbeInterval <= 0 {
		opts.ProbeInterval = DefaultProbeInterval
	}
	return &Watcher{client: client, sink: sink, logger: opts.Logger, opts: opts, previous: make(map[string]cpuCounters)}
}

// Run veille jusqu'à l'arrêt du contexte : sonde, puis inventaire, flux
// d'événements et mesures tant que le démon répond ; coupé, tout reprend
// à la sonde.
func (w *Watcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		engine, err := w.client.Ping(ctx)
		if err != nil {
			w.reportEngine(engineReport(engine, err))
			if !sleep(ctx, w.opts.ProbeInterval) {
				return
			}
			continue
		}
		w.reportEngine(service.EngineReport{Present: true, Version: engine.Version, APIVersion: engine.APIVersion})
		w.deliverInventory(ctx)
		if err := w.follow(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("docker event stream lost", "error", err)
		}
		if !sleep(ctx, reconnectDelay) {
			return
		}
	}
}

func engineReport(engine dockerapi.Engine, err error) service.EngineReport {
	report := service.EngineReport{Version: engine.Version, APIVersion: engine.APIVersion}
	switch {
	case errors.Is(err, dockerapi.ErrNoSocket):
		report.Reason = service.ReasonNoSocket
	case errors.Is(err, dockerapi.ErrSocketDenied):
		report.Reason = service.ReasonDenied
	case errors.Is(err, dockerapi.ErrVersionTooOld):
		report.Reason = service.ReasonTooOld
	default:
		report.Reason = service.ReasonDown
	}
	return report
}

// reportEngine ne parle que si l'état du démon change : un Docker absent
// ne remplit pas le signal d'une machine sans Docker.
func (w *Watcher) reportEngine(report service.EngineReport) {
	w.mu.Lock()
	same := w.engine != nil && *w.engine == report
	w.engine = &report
	w.mu.Unlock()
	if same {
		return
	}
	w.sink.Deliver(service.Report{Engine: &report})
}

func (w *Watcher) deliverInventory(ctx context.Context) {
	inventory, err := w.inventory(ctx)
	if err != nil {
		w.logger.Warn("docker inventory", "error", err)
		return
	}
	w.sink.Deliver(service.Report{Complete: true, Inventory: inventory})
}

// inventory liste puis inspecte chaque conteneur ; un « compose run » est
// laissé de côté, un conteneur disparu entre les deux aussi.
func (w *Watcher) inventory(ctx context.Context) ([]service.Container, error) {
	list, err := w.client.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	inventory := make([]service.Container, 0, len(list))
	for _, summary := range list {
		if summary.IsOneOff() {
			continue
		}
		container, err := w.inspect(ctx, summary.ID)
		if errors.Is(err, dockerapi.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inventory = append(inventory, container)
	}
	return inventory, nil
}

func (w *Watcher) inspect(ctx context.Context, id string) (service.Container, error) {
	inspected, err := w.client.Inspect(ctx, id)
	if err != nil {
		return service.Container{}, err
	}
	return toContainer(inspected), nil
}

// follow tient le flux d'événements et les deux cadences tant que le démon
// répond ; il rend la main sur la première coupure.
func (w *Watcher) follow(ctx context.Context) error {
	w.mu.Lock()
	since := w.since
	w.mu.Unlock()
	stream, err := w.client.Events(ctx, since)
	if err != nil {
		return err
	}
	defer stream.Close()

	events := make(chan dockerapi.Event)
	failed := make(chan error, 1)
	go func() {
		defer close(events)
		for {
			event, err := stream.Next()
			if err != nil {
				failed <- err
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	samples := time.NewTicker(w.opts.SampleInterval)
	defer samples.Stop()
	inventories := time.NewTicker(w.opts.InventoryInterval)
	defer inventories.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-failed:
			if errors.Is(err, io.EOF) {
				return errors.New("event stream closed")
			}
			return err
		case event := <-events:
			w.handleEvent(ctx, event)
		case <-samples.C:
			w.deliverStats(ctx)
		case <-inventories.C:
			w.deliverInventory(ctx)
		}
	}
}

// handleEvent traduit une action Docker en événement de service, avec le
// conteneur réinspecté ; les actions qui ne changent ni l'état ni la
// santé ni le nom sont ignorées, le prochain inventaire couvre le reste.
func (w *Watcher) handleEvent(ctx context.Context, raw dockerapi.Event) {
	w.mu.Lock()
	w.since = raw.Since()
	w.mu.Unlock()
	event, wanted := translate(raw)
	if !wanted {
		return
	}
	if event.Action == service.ActionDestroy {
		w.forgetCounters(event.ContainerID)
		w.sink.Deliver(service.Report{Events: []service.Event{event}})
		return
	}
	if event.Action == "die" && event.ExitCode != nil && !service.IsCleanExit(*event.ExitCode) {
		event.Snippet = w.snippet(ctx, event.ContainerID)
	}
	container, err := w.inspect(ctx, event.ContainerID)
	if err == nil {
		event.Container = &container
	} else if !errors.Is(err, dockerapi.ErrNotFound) {
		w.logger.Warn("inspect after event", "action", raw.Action, "error", err)
	}
	w.sink.Deliver(service.Report{Events: []service.Event{event}})
}

// translate dit quel état ou quelle santé une action implique. Un
// « stop » ou un « kill » ne changent rien par eux-mêmes : le « die » qui
// suit porte l'état et le code de sortie.
func translate(raw dockerapi.Event) (service.Event, bool) {
	event := service.Event{At: raw.At(), Action: raw.Action, ContainerID: raw.Actor.ID}
	if strings.HasPrefix(raw.Action, "health_status") {
		_, status, _ := strings.Cut(raw.Action, ": ")
		event.Action = "health_status"
		event.Health = service.Health(status)
		return event, event.Health != ""
	}
	switch raw.Action {
	case "create":
		event.State = service.StateCreated
	case "start", "unpause":
		event.State = service.StateRunning
	case "pause":
		event.State = service.StatePaused
	case "die":
		event.State = service.StateExited
		code := raw.ExitCode()
		if code >= 0 {
			event.ExitCode = &code
		}
	case "rename", service.ActionDestroy:
	default:
		return service.Event{}, false
	}
	return event, true
}

// snippet lit les dernières lignes du journal d'un conteneur qui vient de
// s'arrêter mal ; bornées en lignes et en octets.
func (w *Watcher) snippet(ctx context.Context, id string) string {
	reader, err := w.client.Logs(ctx, id, dockerapi.LogsOptions{Tail: service.SnippetLines})
	if err != nil {
		return ""
	}
	defer reader.Close()
	var lines []string
	for {
		line, err := reader.Next()
		if err != nil {
			break
		}
		lines = append(lines, line.Text)
	}
	snippet := strings.Join(lines, "\n")
	if len(snippet) > service.MaxSnippetBytes {
		snippet = snippet[len(snippet)-service.MaxSnippetBytes:]
	}
	return snippet
}

func (w *Watcher) forgetCounters(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.previous, id)
}

// deliverStats mesure chaque conteneur qui tourne ; la première mesure
// d'un conteneur n'a pas de delta et ne donne rien.
func (w *Watcher) deliverStats(ctx context.Context) {
	list, err := w.client.ListContainers(ctx)
	if err != nil {
		w.logger.Warn("list for stats", "error", err)
		return
	}
	var stats []service.Stat
	for _, summary := range list {
		if summary.State != string(service.StateRunning) || summary.IsOneOff() {
			continue
		}
		measured, err := w.client.Stats(ctx, summary.ID)
		if err != nil {
			continue
		}
		stat, ok := w.toStat(summary.ID, measured)
		if ok {
			stats = append(stats, stat)
		}
	}
	if len(stats) > 0 {
		w.sink.Deliver(service.Report{Stats: stats})
	}
}

// toStat fait le delta de processeur avec la mesure précédente. Un compteur
// qui recule (conteneur relancé) vaut une première mesure : rien ce tour.
func (w *Watcher) toStat(id string, measured dockerapi.Stats) (service.Stat, bool) {
	current := cpuCounters{total: measured.CPU.Usage.Total, system: measured.CPU.System}
	w.mu.Lock()
	previous, hasPrevious := w.previous[id]
	w.previous[id] = current
	w.mu.Unlock()
	if !hasPrevious || current.total < previous.total || current.system <= previous.system {
		return service.Stat{}, false
	}
	cpuDelta := float64(current.total - previous.total)
	systemDelta := float64(current.system - previous.system)
	cores := measured.CPU.OnlineCPUs
	if cores <= 0 {
		cores = 1
	}
	sampledAt := measured.ReadAt()
	if sampledAt.IsZero() {
		sampledAt = time.Now().UTC()
	}
	return service.Stat{
		ContainerID: id,
		SampledAt:   sampledAt.Truncate(time.Second),
		CPUPercent:  cpuDelta / systemDelta * float64(cores) * 100,
		MemUsed:     clampInt64(measured.MemoryUsed()),
		MemLimit:    clampInt64(measured.Memory.Limit),
	}, true
}

func clampInt64(value uint64) int64 {
	if value > 1<<62 {
		return 1 << 62
	}
	return int64(value)
}

// toContainer fait un conteneur de service d'un conteneur inspecté.
func toContainer(inspected dockerapi.Container) service.Container {
	container := service.Container{
		ContainerID:  inspected.ID,
		Name:         inspected.ShortName(),
		Group:        inspected.Config.Labels[dockerapi.LabelComposeProject],
		Image:        inspected.Config.Image,
		ImageID:      inspected.Image,
		State:        service.State(inspected.State.Status),
		ExitCode:     inspected.State.ExitCode,
		RestartCount: inspected.RestartCount,
		Ports:        publishedPorts(inspected.Network.Ports),
		CreatedAt:    dockerapi.ParseTime(inspected.Created),
	}
	if composeService := inspected.Config.Labels[dockerapi.LabelComposeService]; composeService != "" {
		container.Name = composeService
	}
	if inspected.State.Health != nil {
		container.Health = service.Health(inspected.State.Health.Status)
	}
	if started := dockerapi.ParseTime(inspected.State.StartedAt); !started.IsZero() {
		container.StartedAt = &started
	}
	if finished := dockerapi.ParseTime(inspected.State.FinishedAt); !finished.IsZero() {
		container.FinishedAt = &finished
	}
	return container
}

// publishedPorts lit « 80/tcp » → [{127.0.0.1 18080}] ; un port exposé
// sans publication n'a pas de liaison et ne compte pas.
func publishedPorts(ports map[string][]dockerapi.PortBinding) []service.Port {
	var published []service.Port
	for key, bindings := range ports {
		containerPort, protocol := splitPortKey(key)
		for _, binding := range bindings {
			hostPort := atoi(binding.HostPort)
			if containerPort == 0 || hostPort == 0 {
				continue
			}
			published = append(published, service.Port{IP: binding.HostIP, HostPort: hostPort, ContainerPort: containerPort, Protocol: protocol})
		}
	}
	sortPorts(published)
	if len(published) > service.MaxPortsPerContainer {
		published = published[:service.MaxPortsPerContainer]
	}
	return published
}

func splitPortKey(key string) (int, string) {
	port, protocol, ok := strings.Cut(key, "/")
	if !ok {
		return atoi(port), "tcp"
	}
	return atoi(port), protocol
}

func atoi(value string) int {
	number := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		number = number*10 + int(r-'0')
	}
	return number
}

func sortPorts(ports []service.Port) {
	for i := 1; i < len(ports); i++ {
		for j := i; j > 0 && lessPort(ports[j], ports[j-1]); j-- {
			ports[j], ports[j-1] = ports[j-1], ports[j]
		}
	}
}

func lessPort(a, b service.Port) bool {
	if a.HostPort != b.HostPort {
		return a.HostPort < b.HostPort
	}
	return a.Protocol+a.IP < b.Protocol+b.IP
}

func sleep(ctx context.Context, delay time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}
