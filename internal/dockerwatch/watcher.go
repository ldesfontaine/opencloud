package dockerwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
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
}

type cpuCounters struct {
	total  uint64
	system uint64
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
	return &Watcher{client: client, sink: sink, logger: opts.Logger, opts: opts, previous: make(map[string]cpuCounters), stopRequests: make(map[string]time.Time)}
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
		w.reportEngine(container.EngineReport{Present: true, Version: engine.Version, APIVersion: engine.APIVersion})
		w.deliverInventory(ctx)
		if err := w.follow(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("docker event stream lost", "error", err)
		}
		if !sleep(ctx, reconnectDelay) {
			return
		}
	}
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

// deliverInventory livre les conteneurs et les réseaux de la machine, les
// deux listes complètes.
func (w *Watcher) deliverInventory(ctx context.Context) {
	inventory, err := w.inventory(ctx)
	if err != nil {
		w.logger.Warn("docker inventory", "error", err)
		return
	}
	networks, err := w.networks(ctx)
	if err != nil {
		w.logger.Warn("docker networks", "error", err)
		return
	}
	w.sink.Deliver(container.Report{Complete: true, Inventory: inventory, NetworksComplete: true, Networks: networks})
}

// deliverNetworks ne relit que les réseaux : après un réseau créé ou
// détruit, l'inventaire des conteneurs n'a pas bougé.
func (w *Watcher) deliverNetworks(ctx context.Context) {
	networks, err := w.networks(ctx)
	if err != nil {
		w.logger.Warn("docker networks", "error", err)
		return
	}
	w.sink.Deliver(container.Report{NetworksComplete: true, Networks: networks})
}

func (w *Watcher) networks(ctx context.Context) ([]container.NetworkReport, error) {
	list, err := w.client.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	networks := make([]container.NetworkReport, 0, len(list))
	for _, network := range list {
		networks = append(networks, container.NetworkReport{
			NetworkID: network.ID, Name: network.Name, Driver: network.Driver, Internal: network.Internal,
			Group: network.Labels[dockerapi.LabelComposeProject],
		})
	}
	if len(networks) > container.MaxNetworksPerMachine {
		networks = networks[:container.MaxNetworksPerMachine]
	}
	return networks, nil
}

// inventory liste puis inspecte chaque conteneur ; un « compose run » est
// laissé de côté, un conteneur disparu entre les deux aussi.
func (w *Watcher) inventory(ctx context.Context) ([]container.Observed, error) {
	list, err := w.client.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	inventory := make([]container.Observed, 0, len(list))
	for _, summary := range list {
		if summary.IsOneOff() {
			continue
		}
		observed, err := w.inspect(ctx, summary.ID)
		if errors.Is(err, dockerapi.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inventory = append(inventory, observed)
	}
	return inventory, nil
}

func (w *Watcher) inspect(ctx context.Context, id string) (container.Observed, error) {
	inspected, err := w.client.Inspect(ctx, id)
	if err != nil {
		return container.Observed{}, err
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

// handleEvent traduit une action Docker en événement de conteneur, avec le
// conteneur réinspecté ; les actions qui ne changent ni l'état ni la
// santé ni le nom ni les réseaux sont ignorées, le prochain inventaire
// couvre le reste. Un réseau créé ou détruit fait relire la liste.
func (w *Watcher) handleEvent(ctx context.Context, raw dockerapi.Event) {
	w.mu.Lock()
	w.since = raw.Since()
	w.mu.Unlock()
	if raw.Type == "network" && (raw.Action == "create" || raw.Action == "destroy") {
		w.deliverNetworks(ctx)
		return
	}
	w.noteStopRequest(raw)
	event, wanted := translate(raw)
	if !wanted {
		return
	}
	if event.Action == container.ActionDestroy {
		w.forgetCounters(event.ContainerID)
		w.forgetStopRequest(event.ContainerID)
		w.sink.Deliver(container.Report{Events: []container.Event{event}})
		return
	}
	inspected, err := w.client.Inspect(ctx, event.ContainerID)
	isGone := errors.Is(err, dockerapi.ErrNotFound)
	var observed container.Observed
	if err == nil {
		observed = toContainer(inspected)
		event.Container = &observed
	} else if !isGone {
		w.logger.Warn("inspect after event", "action", raw.Action, "error", err)
	}
	if event.Action == "die" {
		// Un conteneur qu'on retire (rm -f, compose down) est tué en
		// SIGKILL sans « stop » : c'est demandé aussi.
		isRemoved := isGone || inspected.State.Status == string(container.StateRemoving)
		event.Killed = event.ExitCode != nil && *event.ExitCode == exitCodeKilled &&
			!observed.OOMKilled && !isRemoved && !w.takeStopRequest(event.ContainerID, event.At, stopTimeout(inspected))
	}
	// Le manque de mémoire ne se lit qu'à l'inspection : elle passe avant.
	if event.Action == "die" && event.ExitCode != nil && !container.IsCleanStop(*event.ExitCode, observed.OOMKilled, event.Killed) {
		event.Snippet = w.snippet(ctx, event.ContainerID)
	}
	w.sink.Deliver(container.Report{Events: []container.Event{event}})
}

// exitCodeKilled est le code d'un processus tué par SIGKILL (128 + 9).
const exitCodeKilled = 137

// noteStopRequest retient une demande d'arrêt : docker stop, restart et
// compose down envoient d'abord le signal d'arrêt du conteneur, puis
// SIGKILL au bout du délai, et Docker émet « stop ». docker kill n'envoie
// que SIGKILL. Un conteneur qui repart n'a plus d'arrêt en attente.
func (w *Watcher) noteStopRequest(raw dockerapi.Event) {
	if raw.Type != "container" && raw.Type != "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case raw.IsStopSignal(), raw.Action == "stop":
		w.stopRequests[raw.Actor.ID] = raw.At()
	case raw.Action == "start":
		delete(w.stopRequests, raw.Actor.ID)
	}
}

// takeStopRequest dit si un arrêt a été demandé assez peu de temps avant
// ce « die », et l'efface : il ne vaut que pour un arrêt.
func (w *Watcher) takeStopRequest(id string, diedAt time.Time, timeout time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	requestedAt, ok := w.stopRequests[id]
	delete(w.stopRequests, id)
	return ok && diedAt.Sub(requestedAt) <= timeout+stopRequestMargin
}

func (w *Watcher) forgetStopRequest(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.stopRequests, id)
}

// stopTimeout rend le délai de docker stop du conteneur.
func stopTimeout(inspected dockerapi.Container) time.Duration {
	if inspected.Config.StopTimeout == nil || *inspected.Config.StopTimeout < 0 {
		return defaultStopTimeout
	}
	return time.Duration(*inspected.Config.StopTimeout) * time.Second
}

// translate dit quel état ou quelle santé une action implique. Un
// « stop » ou un « kill » ne changent rien par eux-mêmes : le « die » qui
// suit porte l'état et le code de sortie. Sur un réseau, l'acteur est le
// réseau : le conteneur est dans les attributs.
func translate(raw dockerapi.Event) (container.Event, bool) {
	if raw.Type == "network" {
		return translateNetwork(raw)
	}
	event := container.Event{At: raw.At(), Action: raw.Action, ContainerID: raw.Actor.ID}
	if strings.HasPrefix(raw.Action, "health_status") {
		_, status, _ := strings.Cut(raw.Action, ": ")
		event.Action = "health_status"
		event.Health = container.Health(status)
		return event, event.Health != ""
	}
	switch raw.Action {
	case "create":
		event.State = container.StateCreated
	case "start", "unpause":
		event.State = container.StateRunning
	case "pause":
		event.State = container.StatePaused
	case "die":
		event.State = container.StateExited
		code := raw.ExitCode()
		if code >= 0 {
			event.ExitCode = &code
		}
	case "rename", container.ActionDestroy:
	default:
		return container.Event{}, false
	}
	return event, true
}

func translateNetwork(raw dockerapi.Event) (container.Event, bool) {
	event := container.Event{At: raw.At(), ContainerID: raw.Actor.Attributes["container"]}
	switch raw.Action {
	case "connect":
		event.Action = container.ActionNetworkConnect
	case "disconnect":
		event.Action = container.ActionNetworkDisconnect
	default:
		return container.Event{}, false
	}
	return event, event.ContainerID != ""
}

// snippet lit les dernières lignes du journal d'un conteneur qui vient de
// s'arrêter mal ; bornées en lignes et en octets.
func (w *Watcher) snippet(ctx context.Context, id string) string {
	reader, err := w.client.Logs(ctx, id, dockerapi.LogsOptions{Tail: container.SnippetLines})
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
	if len(snippet) > container.MaxSnippetBytes {
		snippet = snippet[len(snippet)-container.MaxSnippetBytes:]
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
	var stats []container.Stat
	for _, summary := range list {
		if summary.State != string(container.StateRunning) || summary.IsOneOff() {
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
		w.sink.Deliver(container.Report{Stats: stats})
	}
}

// toStat fait le delta de processeur avec la mesure précédente. Un compteur
// qui recule (conteneur relancé) vaut une première mesure : rien ce tour.
func (w *Watcher) toStat(id string, measured dockerapi.Stats) (container.Stat, bool) {
	current := cpuCounters{total: measured.CPU.Usage.Total, system: measured.CPU.System}
	w.mu.Lock()
	previous, hasPrevious := w.previous[id]
	w.previous[id] = current
	w.mu.Unlock()
	if !hasPrevious || current.total < previous.total || current.system <= previous.system {
		return container.Stat{}, false
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
	return container.Stat{
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

// toContainer fait le conteneur observé de ce que Docker a inspecté.
func toContainer(inspected dockerapi.Container) container.Observed {
	observed := container.Observed{
		ContainerID:  inspected.ID,
		Name:         inspected.ShortName(),
		Group:        inspected.Config.Labels[dockerapi.LabelComposeProject],
		Image:        inspected.Config.Image,
		ImageID:      inspected.Image,
		State:        container.State(inspected.State.Status),
		ExitCode:     inspected.State.ExitCode,
		OOMKilled:    inspected.State.OOMKilled,
		RestartCount: inspected.RestartCount,
		Ports:        publishedPorts(inspected.Network.Ports),
		NetworkMode:  inspected.Host.NetworkMode,
		Privileged:   inspected.Host.Privileged,
		Networks:     attachments(inspected),
		DependsOn:    dependencies(inspected),
		Labels:       ownLabels(inspected.Config.Labels),
		CreatedAt:    dockerapi.ParseTime(inspected.Created),
	}
	if composeService := inspected.Config.Labels[dockerapi.LabelComposeService]; composeService != "" {
		observed.Name = composeService
		observed.ComposeService = composeService
		observed.ComposeDir = inspected.Config.Labels[dockerapi.LabelComposeWorkingDir]
		observed.ComposeFile, _, _ = strings.Cut(inspected.Config.Labels[dockerapi.LabelComposeConfigFiles], ",")
	}
	if inspected.State.Health != nil {
		observed.Health = container.Health(inspected.State.Health.Status)
	}
	if started := dockerapi.ParseTime(inspected.State.StartedAt); !started.IsZero() {
		observed.StartedAt = &started
	}
	if finished := dockerapi.ParseTime(inspected.State.FinishedAt); !finished.IsZero() {
		observed.FinishedAt = &finished
	}
	return observed
}

// labelSuffix est ce qu'un suffixe de label peut être : le même alphabet que
// le nom d'une unité, en minuscules.
var labelSuffix = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// ownLabels ne garde que nos labels (ceux du préfixe de la marque), préfixe
// retiré : seuls nos labels partent, jamais un label Traefik ou Compose de
// plus. Docker fusionne dans Config.Labels ceux de l'image, que son auteur
// a écrits ; l'identifiant du service, tiré au hasard, les rend inoffensifs.
// Un suffixe ou une valeur hors bornes est écarté, et la carte est coupée à
// sa taille maximale : les deux labels qu'openCloud lit passent d'abord, pour
// qu'une image bavarde ne les chasse pas.
//
// Le préfixe se reconnaît sans tenir compte de la casse : « Opencloud.service »
// est une tentative d'étiquette, pas un label d'autrui. Si des labels ont dû
// partir et qu'il ne reste pas la paire service et unité, la carte porte la
// clé réservée LabelInvalid (le nombre de clés écartées) : le conteneur est étiqueté, mal,
// et le serveur ne le laissera pas retomber sur Compose ou sur son nom.
func ownLabels(labels map[string]string) map[string]string {
	suffixes := make([]string, 0, len(labels))
	discarded := 0
	for key, value := range labels {
		suffix, ours := cutLabelPrefix(key)
		if !ours {
			continue
		}
		if suffix == container.LabelInvalid || !labelSuffix.MatchString(suffix) || value == "" || len(value) > container.MaxNameLength || hasControl(value) || key != brand.LabelPrefix+suffix {
			discarded++
			continue
		}
		suffixes = append(suffixes, suffix)
	}
	sort.Slice(suffixes, func(i, j int) bool {
		first, second := isReadLabel(suffixes[i]), isReadLabel(suffixes[j])
		if first != second {
			return first
		}
		return suffixes[i] < suffixes[j]
	})
	// Les deux labels lus passent d'abord (tri ci-dessus), donc ce qu'on garde
	// d'eux ne dépend pas de la coupe. Le signe d'un label mal formé tient la
	// place d'une clé.
	flagged := discarded > 0 && !(slices.Contains(suffixes, container.LabelService) && slices.Contains(suffixes, container.LabelUnit))
	room := container.MaxLabelsPerContainer
	if flagged {
		room--
	}
	if len(suffixes) > room {
		suffixes = suffixes[:room]
	}
	own := make(map[string]string, len(suffixes)+1)
	for _, suffix := range suffixes {
		own[suffix] = labels[brand.LabelPrefix+suffix]
	}
	if flagged {
		own[container.LabelInvalid] = strconv.Itoa(discarded)
	}
	if len(own) == 0 {
		return nil
	}
	return own
}

// cutLabelPrefix retire le préfixe de la marque, majuscules comprises.
func cutLabelPrefix(key string) (string, bool) {
	if len(key) < len(brand.LabelPrefix) || !strings.EqualFold(key[:len(brand.LabelPrefix)], brand.LabelPrefix) {
		return "", false
	}
	return key[len(brand.LabelPrefix):], true
}

func hasControl(text string) bool {
	return !utf8.ValidString(text) || strings.ContainsFunc(text, unicode.IsControl)
}

func isReadLabel(suffix string) bool {
	return suffix == container.LabelService || suffix == container.LabelUnit
}

// publishedPorts lit « 80/tcp » → [{127.0.0.1 18080}] ; un port exposé
// sans publication n'a pas de liaison et ne compte pas.
func publishedPorts(ports map[string][]dockerapi.PortBinding) []container.Port {
	var published []container.Port
	for key, bindings := range ports {
		containerPort, protocol := splitPortKey(key)
		for _, binding := range bindings {
			hostPort := atoi(binding.HostPort)
			if containerPort == 0 || hostPort == 0 {
				continue
			}
			published = append(published, container.Port{IP: binding.HostIP, HostPort: hostPort, ContainerPort: containerPort, Protocol: protocol})
		}
	}
	sortPorts(published)
	if len(published) > container.MaxPortsPerContainer {
		published = published[:container.MaxPortsPerContainer]
	}
	return published
}

// attachments lit les réseaux joints, par nom ; l'alias qu'est l'id court
// du conteneur n'apprend rien et n'est pas gardé.
func attachments(inspected dockerapi.Container) []container.Attachment {
	shortID := inspected.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	var joined []container.Attachment
	for name, endpoint := range inspected.Network.Networks {
		// Un conteneur tout juste créé n'a pas encore d'identifiant de réseau :
		// ce rattachement-là n'est pas encore un fait, le prochain inventaire
		// le donnera.
		if endpoint.NetworkID == "" {
			continue
		}
		names := endpoint.DNSNames
		if len(names) == 0 {
			names = endpoint.Aliases
		}
		aliases := make([]string, 0, len(names))
		for _, alias := range names {
			if alias != "" && alias != shortID && !contains(aliases, alias) {
				aliases = append(aliases, alias)
			}
		}
		if len(aliases) > container.MaxAliasesPerNetwork {
			aliases = aliases[:container.MaxAliasesPerNetwork]
		}
		joined = append(joined, container.Attachment{NetworkID: endpoint.NetworkID, Name: name, IP: endpoint.IPAddress, Aliases: aliases})
	}
	sort.Slice(joined, func(i, j int) bool { return joined[i].Name < joined[j].Name })
	if len(joined) > container.MaxNetworksPerContainer {
		joined = joined[:container.MaxNetworksPerContainer]
	}
	return joined
}

// dependencies lit les dépendances déclarées : le label Compose
// « db:service_started:false,cache:service_healthy:true » donne des noms de
// services ; un lien « /db:/web/db » donne un nom de conteneur.
func dependencies(inspected dockerapi.Container) []container.Dependency {
	var declared []container.Dependency
	for _, entry := range strings.Split(inspected.Config.Labels[dockerapi.LabelComposeDependsOn], ",") {
		name, _, _ := strings.Cut(entry, ":")
		if name != "" {
			declared = append(declared, container.Dependency{Name: name, Source: container.DependencyCompose})
		}
	}
	for _, link := range inspected.Host.Links {
		target, _, _ := strings.Cut(link, ":")
		target = strings.TrimPrefix(target, "/")
		if target != "" {
			declared = append(declared, container.Dependency{Name: target, Source: container.DependencyLink})
		}
	}
	if len(declared) > container.MaxDependencies {
		declared = declared[:container.MaxDependencies]
	}
	return declared
}

func contains(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
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

func sortPorts(ports []container.Port) {
	for i := 1; i < len(ports); i++ {
		for j := i; j > 0 && lessPort(ports[j], ports[j-1]); j-- {
			ports[j], ports[j-1] = ports[j-1], ports[j]
		}
	}
}

func lessPort(a, b container.Port) bool {
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
