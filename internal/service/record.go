package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Ce que le service attend de la base ; le package store le fournit.
type Store interface {
	// ListServices rend les fiches d'une machine, archivées comprises ; toutes
	// les machines si machineID est vide.
	ListServices(ctx context.Context, machineID string) ([]Service, error)
	GetService(ctx context.Context, id string) (Service, error)
	// ApplyChanges écrit tout ce qu'un rapport change, en une transaction.
	ApplyChanges(ctx context.Context, changes Changes) error
	ListTransitions(ctx context.Context, serviceID string, limit int) ([]Transition, error)
	// LatestServiceSamples rend le dernier échantillon de chaque service.
	LatestServiceSamples(ctx context.Context) ([]Sample, error)
	ListEngines(ctx context.Context) ([]Engine, error)
	// ListNetworks rend les réseaux d'une machine, par nom.
	ListNetworks(ctx context.Context, machineID string) ([]Network, error)
	PurgeServices(ctx context.Context, transitionsBefore, archivedBefore, samplesBefore time.Time) error
}

// Changes est ce qu'un rapport change : des fiches à écrire, celles à
// archiver parce que l'inventaire ne les nomme plus, des transitions, des
// échantillons, l'état du Docker de la machine.
type Changes struct {
	MachineID string
	Now       time.Time
	Engine    *Engine
	Services  []Service
	// ArchiveMissing archive les fiches vivantes de la machine absentes de
	// Keep : un inventaire complet fait foi.
	ArchiveMissing bool
	Keep           []string
	// ReplaceNetworks remplace les réseaux de la machine par Networks : la
	// liste complète fait foi, un réseau détruit disparaît.
	ReplaceNetworks bool
	Networks        []Network
	Transitions     []Transition
	Samples         []Sample
}

// Watcher reçoit chaque écriture visible : le direct s'y branche. nil est
// toléré.
type Watcher interface {
	ServicesChanged(machineID string)
}

// Tracker est le composant : il suit les services que les agents
// rapportent ; Service, lui, est une fiche.
type Tracker struct {
	store   Store
	watcher Watcher
	logger  *slog.Logger
	relay   *Relay
	now     func() time.Time
}

func New(store Store, logger *slog.Logger) *Tracker {
	return &Tracker{store: store, logger: logger, relay: NewRelay(), now: time.Now}
}

func (t *Tracker) SetClock(now func() time.Time) {
	t.now = now
}

func (t *Tracker) SetWatcher(watcher Watcher) {
	t.watcher = watcher
}

// Record écrit ce qu'un agent rapporte de sa machine. Le rapport entier est
// refusé dès qu'un élément est hors de ce qu'un agent peut observer.
func (t *Tracker) Record(ctx context.Context, machineID string, report Report) error {
	if report.IsEmpty() {
		return nil
	}
	now := t.now()
	if err := validate(report, now); err != nil {
		return err
	}
	stored, err := t.store.ListServices(ctx, machineID)
	if err != nil {
		return err
	}
	changes := planChanges(machineID, stored, report, now)
	if err := t.store.ApplyChanges(ctx, changes); err != nil {
		return err
	}
	if t.watcher != nil {
		t.watcher.ServicesChanged(machineID)
	}
	return nil
}

// planChanges traduit le rapport en écritures, dans l'ordre du rapport :
// l'inventaire d'abord, puis chaque événement appliqué sur la fiche telle
// que le précédent l'a laissée, puis les mesures des fiches connues.
func planChanges(machineID string, stored []Service, report Report, now time.Time) Changes {
	plan := &planner{machineID: machineID, now: now, known: make(map[string]Service, len(stored)), touched: make(map[string]int)}
	for _, service := range stored {
		plan.known[service.ID] = service
	}
	if report.Engine != nil {
		plan.changes.Engine = &Engine{
			MachineID: machineID, Present: report.Engine.Present, Reason: report.Engine.Reason,
			Version: report.Engine.Version, APIVersion: report.Engine.APIVersion, CheckedAt: now,
		}
	}
	if report.Complete {
		plan.applyInventory(report.Inventory)
	}
	if report.NetworksComplete {
		plan.applyNetworks(report.Networks)
	}
	for _, event := range report.Events {
		plan.applyEvent(event)
	}
	for _, stat := range report.Stats {
		plan.applyStat(stat)
	}
	plan.changes.MachineID = machineID
	plan.changes.Now = now
	return plan.changes
}

type planner struct {
	machineID string
	now       time.Time
	known     map[string]Service
	// touched donne la place de chaque fiche déjà dans changes.Services :
	// une fiche touchée deux fois par le même rapport n'est écrite qu'une.
	touched map[string]int
	changes Changes
}

func (p *planner) applyInventory(inventory []Container) {
	p.changes.ArchiveMissing = true
	p.changes.Keep = make([]string, 0, len(inventory))
	for _, container := range inventory {
		id := ID(p.machineID, container.ContainerID)
		p.changes.Keep = append(p.changes.Keep, id)
		previous, existed := p.known[id]
		next := p.fromContainer(container, previous, existed)
		p.record(next, previous, existed, Transition{At: p.now, Action: "inventory"})
	}
	for id, service := range p.known {
		if service.IsArchived() || contains(p.changes.Keep, id) {
			continue
		}
		archived := service
		archived.ArchivedAt = p.now
		p.record(archived, service, true, Transition{At: p.now, Action: "gone"})
	}
}

func (p *planner) applyNetworks(networks []NetworkReport) {
	p.changes.ReplaceNetworks = true
	p.changes.Networks = make([]Network, 0, len(networks))
	for _, network := range networks {
		p.changes.Networks = append(p.changes.Networks, Network{
			MachineID: p.machineID, NetworkID: network.NetworkID, Name: network.Name, Driver: network.Driver,
			Internal: network.Internal, Group: network.Group, SeenAt: p.now,
		})
	}
}

func (p *planner) applyEvent(event Event) {
	id := ID(p.machineID, event.ContainerID)
	previous, existed := p.known[id]
	transition := Transition{At: event.At, Action: event.Action, ExitCode: event.ExitCode, Replayed: event.Replayed, Snippet: event.Snippet}
	if event.Action == ActionDestroy {
		if !existed || previous.IsArchived() {
			return
		}
		archived := previous
		archived.ArchivedAt = event.At
		p.record(archived, previous, true, transition)
		return
	}
	if !existed && event.Container == nil {
		// Un événement sur un conteneur qu'on ne connaît pas et qu'on ne peut
		// pas décrire : l'inventaire suivant le rattrapera.
		return
	}
	next := previous
	if event.Container != nil {
		next = p.fromContainer(*event.Container, previous, existed)
	}
	if event.State != "" {
		next.State = event.State
	}
	if event.Health != "" {
		next.Health = event.Health
	}
	if event.ExitCode != nil {
		next.ExitCode = *event.ExitCode
	}
	next.ArchivedAt = time.Time{}
	next.LastSeenAt = p.now
	p.record(next, previous, existed, transition)
}

func (p *planner) applyStat(stat Stat) {
	id := ID(p.machineID, stat.ContainerID)
	service, known := p.known[id]
	if !known || service.IsArchived() {
		return
	}
	p.changes.Samples = append(p.changes.Samples, Sample{
		ServiceID: id, SampledAt: stat.SampledAt, CPUPercent: stat.CPUPercent, MemUsed: stat.MemUsed, MemLimit: stat.MemLimit,
	})
}

// fromContainer fait une fiche de ce que l'agent a inspecté ; ce que la base
// savait déjà (première vue) est conservé.
func (p *planner) fromContainer(container Container, previous Service, existed bool) Service {
	service := Service{
		ID:           ID(p.machineID, container.ContainerID),
		MachineID:    p.machineID,
		Kind:         KindContainer,
		Name:         container.Name,
		Group:        container.Group,
		ContainerID:  container.ContainerID,
		Image:        container.Image,
		ImageID:      container.ImageID,
		State:        container.State,
		ExitCode:     container.ExitCode,
		Health:       container.Health,
		RestartCount: container.RestartCount,
		Ports:        container.Ports,
		NetworkMode:  container.NetworkMode,
		Privileged:   container.Privileged,
		Networks:     container.Networks,
		DependsOn:    container.DependsOn,
		CreatedAt:    container.CreatedAt.UTC(),
		FirstSeenAt:  p.now,
		LastSeenAt:   p.now,
	}
	if container.StartedAt != nil {
		service.StartedAt = container.StartedAt.UTC()
	}
	if container.FinishedAt != nil {
		service.FinishedAt = container.FinishedAt.UTC()
	}
	if existed {
		service.FirstSeenAt = previous.FirstSeenAt
	}
	return service
}

// record garde la fiche et note la transition si l'état ou la santé a
// changé, ou si la fiche est nouvelle.
func (p *planner) record(next, previous Service, existed bool, transition Transition) {
	changed := !existed || next.State != previous.State || next.Health != previous.Health || next.IsArchived() != previous.IsArchived()
	if changed {
		transition.ServiceID = next.ID
		transition.NewState = next.State
		transition.NewHealth = next.Health
		if existed {
			transition.PreviousState = previous.State
			transition.PreviousHealth = previous.Health
		}
		if next.IsArchived() {
			transition.NewState = ""
			transition.NewHealth = ""
		}
		p.changes.Transitions = append(p.changes.Transitions, transition)
	}
	p.known[next.ID] = next
	if index, seen := p.touched[next.ID]; seen {
		p.changes.Services[index] = next
		return
	}
	p.touched[next.ID] = len(p.changes.Services)
	p.changes.Services = append(p.changes.Services, next)
}

func contains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// List rend les services vivants, d'une machine ou de toutes.
func (t *Tracker) List(ctx context.Context, machineID string) ([]Service, error) {
	all, err := t.store.ListServices(ctx, machineID)
	if err != nil {
		return nil, err
	}
	live := make([]Service, 0, len(all))
	for _, service := range all {
		if !service.IsArchived() {
			live = append(live, service)
		}
	}
	return live, nil
}

func (t *Tracker) Get(ctx context.Context, id string) (Service, error) {
	return t.store.GetService(ctx, id)
}

func (t *Tracker) Transitions(ctx context.Context, id string, limit int) ([]Transition, error) {
	if _, err := t.store.GetService(ctx, id); err != nil {
		return nil, err
	}
	return t.store.ListTransitions(ctx, id, limit)
}

// Topology rend ce que l'onglet Réseau d'une machine dessine : ses
// services vivants, ses groupes et ses arêtes.
func (t *Tracker) Topology(ctx context.Context, machineID string) (Topology, error) {
	services, err := t.List(ctx, machineID)
	if err != nil {
		return Topology{}, err
	}
	networks, err := t.store.ListNetworks(ctx, machineID)
	if err != nil {
		return Topology{}, err
	}
	return BuildTopology(services, networks), nil
}

// Engines rend ce que chaque machine a dit de son Docker.
func (t *Tracker) Engines(ctx context.Context) ([]Engine, error) {
	return t.store.ListEngines(ctx)
}

// CurrentAll rend la mesure courante de chaque service qui en a une.
func (t *Tracker) CurrentAll(ctx context.Context) ([]Current, error) {
	samples, err := t.store.LatestServiceSamples(ctx)
	if err != nil {
		return nil, err
	}
	now := t.now()
	currents := make([]Current, 0, len(samples))
	for _, sample := range samples {
		fresh := now.Sub(sample.SampledAt) <= StaleAfter
		currents = append(currents, Current{ServiceID: sample.ServiceID, Available: fresh, Sample: &sample})
	}
	return currents, nil
}

// NeedsAttention dit si un service va mal : c'est ce que compte la barre
// latérale. Le front dérive ses pastilles de la même règle.
func NeedsAttention(service Service) bool {
	switch service.State {
	case StateRunning:
		return service.Health == HealthUnhealthy
	case StateRestarting, StateDead:
		return true
	case StateExited:
		return !IsCleanExit(service.ExitCode)
	}
	return false
}

// IsCleanExit : 0 est une fin normale, 137 et 143 un arrêt demandé
// (SIGKILL et SIGTERM, ce que docker stop envoie).
func IsCleanExit(code int) bool {
	return code == 0 || code == 137 || code == 143
}

// Count rend le nombre de services vivants et ceux qui demandent attention.
func (t *Tracker) Count(ctx context.Context) (total, attention int, err error) {
	services, err := t.List(ctx, "")
	if err != nil {
		return 0, 0, err
	}
	for _, service := range services {
		if NeedsAttention(service) {
			attention++
		}
	}
	return len(services), attention, nil
}

// Purge efface chaque table au-delà de sa rétention.
func (t *Tracker) Purge(ctx context.Context) error {
	now := t.now()
	return t.store.PurgeServices(ctx, now.Add(-TransitionRetention), now.Add(-ArchivedRetention), now.Add(-SampleRetention))
}

const purgePeriod = 24 * time.Hour

// Watch est la boucle de fond : la purge au départ puis une fois par jour.
func (t *Tracker) Watch(ctx context.Context) {
	t.purge(ctx)
	ticker := time.NewTicker(purgePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.purge(ctx)
		}
	}
}

func (t *Tracker) purge(ctx context.Context) {
	if err := t.Purge(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.logger.Warn("purge services", "error", err)
	}
}
