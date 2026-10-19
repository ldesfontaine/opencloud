package container

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Ce que le conteneur attend de la base ; le package store le fournit.
type Store interface {
	// ListContainers rend les fiches d'une machine, archivées comprises ; toutes
	// les machines si machineID est vide.
	ListContainers(ctx context.Context, machineID string) ([]Container, error)
	GetContainer(ctx context.Context, id string) (Container, error)
	// ApplyChanges écrit tout ce qu'un rapport change, en une transaction.
	ApplyChanges(ctx context.Context, changes Changes) error
	ListTransitions(ctx context.Context, containerID string, limit int) ([]Transition, error)
	// LatestContainerSamples rend le dernier échantillon de chaque conteneur.
	LatestContainerSamples(ctx context.Context) ([]Sample, error)
	ListEngines(ctx context.Context) ([]Engine, error)
	// ListNetworks rend les réseaux d'une machine, par nom.
	ListNetworks(ctx context.Context, machineID string) ([]Network, error)
	PurgeContainers(ctx context.Context, transitionsBefore, archivedBefore, samplesBefore time.Time) error
	// SetContainerUnits écrit le rattachement des fiches nommées, et rien
	// d'autre ; une unité disparue entre-temps s'écrit comme « non déclaré ».
	SetContainerUnits(ctx context.Context, assignments []UnitAssignment) error
}

// UnitMatch est l'unité à laquelle l'annuaire rattache un conteneur, et par
// quel niveau.
type UnitMatch struct {
	UnitID string
	Level  MatchLevel
}

// UnitAssignment est le rattachement à écrire sur une fiche ; une unité
// vide la rend « non déclarée ».
type UnitAssignment struct {
	ContainerID string
	UnitID      string
	Level       MatchLevel
}

// UnitsOnMachine applique la règle de l'annuaire aux conteneurs vivants
// d'une machine, rendus par identifiant de fiche ; une clé absente du
// résultat dit « non déclaré ». nil est toléré.
type UnitsOnMachine interface {
	MatchUnits(ctx context.Context, machineID string, containers []Container) (map[string]UnitMatch, error)
}

// Changes est ce qu'un rapport change : des fiches à écrire, celles à
// archiver parce que l'inventaire ne les nomme plus, des transitions, des
// échantillons, l'état du Docker de la machine.
type Changes struct {
	MachineID  string
	Now        time.Time
	Engine     *Engine
	Containers []Container
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
	// Les plafonds de la machine, appliqués par le store dans la même
	// transaction que l'écriture : les fiches archivées les plus anciennes, puis
	// les transitions les plus anciennes, partent au-delà. Zéro : pas de plafond.
	MaxArchived    int
	MaxTransitions int
	// Refused : des fiches neuves écartées faute de place parmi les vivantes ;
	// le Tracker le journalise, le store n'en fait rien.
	Refused int
}

// Watcher reçoit chaque écriture visible : le direct s'y branche. nil est
// toléré.
type Watcher interface {
	ContainersChanged(machineID string)
}

// Alerter reçoit chaque fiche qu'un rapport a touchée, avec les
// transitions que ce rapport lui a notées : le moteur des alertes en fait
// un arrêt non demandé, un redémarrage, une santé qui échoue. nil est
// toléré.
type Alerter interface {
	ContainerChanged(ctx context.Context, container Container, transitions []Transition)
}

// Tracker est le composant : il suit les conteneurs que les agents
// rapportent ; Container, lui, est une fiche.
type Tracker struct {
	store   Store
	watcher Watcher
	alerter Alerter
	logger  *slog.Logger
	relay   *Relay
	now     func() time.Time
	units   UnitsOnMachine
	// Un verrou par machine (*sync.Mutex) : lire les fiches, rapprocher et
	// écrire d'un seul tenant, pour qu'un rapport et un nouveau
	// rapprochement de l'annuaire ne s'écrasent pas.
	locks sync.Map
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

func (t *Tracker) SetAlerter(alerter Alerter) {
	t.alerter = alerter
}

// SetUnits branche l'annuaire : sans lui, aucun conteneur n'est rattaché.
func (t *Tracker) SetUnits(units UnitsOnMachine) {
	t.units = units
}

func (t *Tracker) machineLock(machineID string) *sync.Mutex {
	lock, _ := t.locks.LoadOrStore(machineID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// Record écrit ce qu'un agent rapporte de sa machine. Un élément hors de ce
// qu'un agent peut observer est écarté et journalisé ; le reste du rapport
// passe. Seule une forme d'ensemble absurde refuse le rapport entier.
func (t *Tracker) Record(ctx context.Context, machineID string, report Report) error {
	if report.IsEmpty() {
		return nil
	}
	now := t.now()
	report, discards, err := validate(report, now)
	if err != nil {
		return err
	}
	for _, dropped := range discards {
		t.logger.Warn("discard invalid container report element", "machine", machineID, "element", dropped.what, "reason", dropped.reason)
	}
	changes, err := t.write(ctx, machineID, report, now)
	if err != nil {
		return err
	}
	t.alert(ctx, changes)
	if t.watcher != nil {
		t.watcher.ContainersChanged(machineID)
	}
	return nil
}

// write lit les fiches de la machine, planifie le rapport, rapproche les
// conteneurs touchés des unités de l'annuaire et écrit le tout, sous le
// verrou de la machine. Le verrou est relâché avant d'alerter et de
// prévenir le direct : ni l'un ni l'autre n'a besoin de lui.
func (t *Tracker) write(ctx context.Context, machineID string, report Report, now time.Time) (Changes, error) {
	lock := t.machineLock(machineID)
	lock.Lock()
	defer lock.Unlock()
	stored, err := t.store.ListContainers(ctx, machineID)
	if err != nil {
		return Changes{}, err
	}
	changes := planChanges(machineID, stored, report, now)
	changes.MaxArchived, changes.MaxTransitions = MaxArchivedPerMachine, MaxTransitionsPerMachine
	if changes.Refused > 0 {
		t.logger.Warn("container records refused, machine is full", "machine", machineID, "refused", changes.Refused, "limit", MaxLiveContainersPerMachine)
	}
	t.match(ctx, machineID, &changes)
	if err := t.store.ApplyChanges(ctx, changes); err != nil {
		return Changes{}, err
	}
	return changes, nil
}

// match pose l'unité de chaque fiche vivante que le rapport touche. Un
// rapport sans fiche vivante, ne portant que des mesures par exemple, ne
// dérange pas l'annuaire. Si le rapprochement échoue le rapport passe
// quand même, avec le rattachement que les fiches avaient.
func (t *Tracker) match(ctx context.Context, machineID string, changes *Changes) {
	if t.units == nil {
		return
	}
	living := make([]int, 0, len(changes.Containers))
	containers := make([]Container, 0, len(changes.Containers))
	for index, touched := range changes.Containers {
		if !touched.IsArchived() {
			living = append(living, index)
			containers = append(containers, touched)
		}
	}
	if len(containers) == 0 {
		return
	}
	matches, err := t.units.MatchUnits(ctx, machineID, containers)
	if err != nil {
		t.logger.Warn("match containers to units", "machine", machineID, "error", err)
		return
	}
	for _, index := range living {
		match := matches[changes.Containers[index].ID]
		changes.Containers[index].UnitID = match.UnitID
		changes.Containers[index].MatchLevel = match.Level
	}
}

// Rematch refait le rapprochement des conteneurs vivants d'une machine
// avec l'annuaire tel qu'il est maintenant, et n'écrit que ce qui change.
// L'annuaire l'appelle après chaque écriture, une fois sa transaction
// validée : jamais pendant, sinon il attendrait le verrou de la machine
// que le rapport en cours tient en lisant l'annuaire.
func (t *Tracker) Rematch(ctx context.Context, machineID string) error {
	if t.units == nil {
		return nil
	}
	count, err := t.rematch(ctx, machineID)
	if err != nil {
		return err
	}
	if count > 0 {
		t.logger.Info("containers rematched", "machine", machineID, "count", count)
		if t.watcher != nil {
			t.watcher.ContainersChanged(machineID)
		}
	}
	return nil
}

func (t *Tracker) rematch(ctx context.Context, machineID string) (int, error) {
	lock := t.machineLock(machineID)
	lock.Lock()
	defer lock.Unlock()
	stored, err := t.store.ListContainers(ctx, machineID)
	if err != nil {
		return 0, err
	}
	living := make([]Container, 0, len(stored))
	for _, found := range stored {
		if !found.IsArchived() {
			living = append(living, found)
		}
	}
	if len(living) == 0 {
		return 0, nil
	}
	matches, err := t.units.MatchUnits(ctx, machineID, living)
	if err != nil {
		return 0, err
	}
	var assignments []UnitAssignment
	for _, found := range living {
		match := matches[found.ID]
		if match.UnitID != found.UnitID || match.Level != found.MatchLevel {
			assignments = append(assignments, UnitAssignment{ContainerID: found.ID, UnitID: match.UnitID, Level: match.Level})
		}
	}
	if len(assignments) == 0 {
		return 0, nil
	}
	if err := t.store.SetContainerUnits(ctx, assignments); err != nil {
		return 0, err
	}
	return len(assignments), nil
}

// RematchAll rattache d'un coup les conteneurs de chaque machine : au
// démarrage, pour ce que l'annuaire a reçu avant que le rapprochement
// existe, sans attendre l'inventaire suivant de chaque agent.
func (t *Tracker) RematchAll(ctx context.Context) {
	stored, err := t.store.ListContainers(ctx, "")
	if err != nil {
		t.logger.Warn("rematch containers", "error", err)
		return
	}
	done := map[string]bool{}
	for _, found := range stored {
		if done[found.MachineID] {
			continue
		}
		done[found.MachineID] = true
		if err := t.Rematch(ctx, found.MachineID); err != nil {
			t.logger.Warn("rematch containers", "machine", found.MachineID, "error", err)
		}
	}
}

// alert donne au moteur chaque fiche touchée avec ses transitions, dans
// l'ordre du rapport.
func (t *Tracker) alert(ctx context.Context, changes Changes) {
	if t.alerter == nil {
		return
	}
	for _, touched := range changes.Containers {
		var transitions []Transition
		for _, transition := range changes.Transitions {
			if transition.ContainerID == touched.ID {
				transitions = append(transitions, transition)
			}
		}
		t.alerter.ContainerChanged(ctx, touched, transitions)
	}
}

// planChanges traduit le rapport en écritures, dans l'ordre du rapport :
// l'inventaire d'abord, puis chaque événement appliqué sur la fiche telle
// que le précédent l'a laissée, puis les mesures des fiches connues.
func planChanges(machineID string, stored []Container, report Report, now time.Time) Changes {
	plan := &planner{machineID: machineID, now: now, known: make(map[string]Container, len(stored)), touched: make(map[string]int)}
	for _, container := range stored {
		plan.known[container.ID] = container
		if !container.IsArchived() {
			plan.living++
		}
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
	known     map[string]Container
	// touched donne la place de chaque fiche déjà dans changes.Containers :
	// une fiche touchée deux fois par le même rapport n'est écrite qu'une.
	touched map[string]int
	// living compte les fiches vivantes, au fil du rapport, pour refuser les
	// neuves au-delà de MaxLiveContainersPerMachine.
	living  int
	changes Changes
}

func (p *planner) applyInventory(inventory []Observed) {
	p.changes.ArchiveMissing = true
	p.changes.Keep = make([]string, 0, len(inventory))
	for _, container := range inventory {
		id := ID(p.machineID, container.ContainerID)
		p.changes.Keep = append(p.changes.Keep, id)
		previous, existed := p.known[id]
		next := p.fromContainer(container, previous, existed)
		p.record(next, previous, existed, Transition{At: p.now, Action: "inventory"})
	}
	for id, container := range p.known {
		if container.IsArchived() || contains(p.changes.Keep, id) {
			continue
		}
		archived := container
		archived.ArchivedAt = p.now
		p.record(archived, container, true, Transition{At: p.now, Action: "gone"})
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
	// Un instant dans le futur n'a pas de sens pour un fait déjà arrivé : la
	// validation tolère l'avance des horloges, la base garde l'heure du serveur.
	at := event.At
	if at.After(p.now) {
		at = p.now
	}
	transition := Transition{At: at, Action: event.Action, ExitCode: event.ExitCode, Replayed: event.Replayed, Snippet: event.Snippet}
	if event.Action == ActionDestroy {
		if !existed || previous.IsArchived() {
			return
		}
		archived := previous
		archived.ArchivedAt = at
		p.record(archived, previous, true, transition)
		return
	}
	if !existed && event.Container == nil {
		// Un événement sur un conteneur qu'on ne connaît pas et qu'on ne peut
		// pas décrire : l'inventaire suivant le rattrapera.
		return
	}
	// Une fiche archivée qu'un événement ranime compte comme une neuve : sans
	// quoi les archivées, rendues vivantes, passeraient le plafond.
	if (!existed || previous.IsArchived()) && p.living >= MaxLiveContainersPerMachine {
		p.changes.Refused++
		return
	}
	next := previous
	if event.Container != nil {
		next = p.fromContainer(*event.Container, previous, existed)
	}
	// Un événement plus vieux que l'état que la réinspection a lu est de
	// l'histoire : il ne remet pas la fiche dans un état que le conteneur a
	// quitté, et il ne compte pas comme un redémarrage de plus.
	if event.Container != nil && event.State != "" && isSuperseded(event, next, p.now) {
		transition.Replayed = true
		event.State, event.ExitCode, event.Killed = "", nil, false
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
	if event.Killed {
		next.Killed = true
	}
	if next.State == StateRunning {
		// Docker oublie le manque de mémoire au redémarrage.
		next.OOMKilled = false
		next.Killed = false
	}
	next.ArchivedAt = time.Time{}
	next.LastSeenAt = p.now
	p.record(next, previous, existed, transition)
}

// staleEventAge est l'âge à partir duquel un événement qui précède l'état
// réinspecté est tenu pour rejoué. Un « die » tout frais précède lui aussi le
// « start » d'un conteneur que sa politique de redémarrage a déjà relancé :
// l'écarter ferait perdre les plantages en boucle. L'âge se lit sur
// l'horloge du serveur, l'instant sur celle de l'agent : un agent en retard
// de l'écart toléré, plus un signal (30 s), doit encore paraître frais.
const staleEventAge = maxFutureSkew + time.Minute

// isSuperseded dit si l'événement est plus ancien que le dernier changement
// d'état (démarrage ou arrêt) que la réinspection a lu, et assez vieux ou
// déjà marqué rejoué pour que ce soit un rejeu, non un événement frais
// devancé par une relance.
func isSuperseded(event Event, inspected Container, now time.Time) bool {
	changedAt := inspected.StartedAt
	if inspected.FinishedAt.After(changedAt) {
		changedAt = inspected.FinishedAt
	}
	if changedAt.IsZero() || !event.At.Before(changedAt) {
		return false
	}
	return event.Replayed || now.Sub(event.At) > staleEventAge
}

func (p *planner) applyStat(stat Stat) {
	id := ID(p.machineID, stat.ContainerID)
	container, known := p.known[id]
	if !known || container.IsArchived() {
		return
	}
	p.changes.Samples = append(p.changes.Samples, Sample{
		ContainerID: id, SampledAt: stat.SampledAt, CPUPercent: stat.CPUPercent, MemUsed: stat.MemUsed, MemLimit: stat.MemLimit,
	})
}

// fromContainer fait une fiche de ce que l'agent a observé ; ce que la base
// savait déjà (première vue) est conservé.
func (p *planner) fromContainer(observed Observed, previous Container, existed bool) Container {
	container := Container{
		ID:             ID(p.machineID, observed.ContainerID),
		MachineID:      p.machineID,
		Kind:           KindContainer,
		Name:           observed.Name,
		Group:          observed.Group,
		ContainerID:    observed.ContainerID,
		Image:          observed.Image,
		ImageID:        observed.ImageID,
		ComposeService: observed.ComposeService,
		ComposeDir:     observed.ComposeDir,
		ComposeFile:    observed.ComposeFile,
		State:          observed.State,
		ExitCode:       observed.ExitCode,
		OOMKilled:      observed.OOMKilled,
		Health:         observed.Health,
		RestartCount:   observed.RestartCount,
		Ports:          observed.Ports,
		NetworkMode:    observed.NetworkMode,
		Privileged:     observed.Privileged,
		Networks:       observed.Networks,
		DependsOn:      observed.DependsOn,
		Labels:         labelsOf(observed.Labels),
		CreatedAt:      observed.CreatedAt.UTC(),
		FirstSeenAt:    p.now,
		LastSeenAt:     p.now,
	}
	if observed.StartedAt != nil {
		container.StartedAt = observed.StartedAt.UTC()
	}
	if observed.FinishedAt != nil {
		container.FinishedAt = observed.FinishedAt.UTC()
	}
	if existed {
		// Docker ne sait pas qu'un arrêt était brutal : l'inventaire le
		// garde tant que c'est le même arrêt.
		container.Killed = previous.Killed && container.State == previous.State && container.FinishedAt.Equal(previous.FinishedAt)
		container.FirstSeenAt = previous.FirstSeenAt
		// L'inventaire ne dit rien de l'annuaire : le rattachement survit.
		container.UnitID = previous.UnitID
		container.MatchLevel = previous.MatchLevel
	}
	return container
}

// labelsOf ne retient que les deux labels que l'annuaire lit, et le signe
// que d'autres ont été écartés. Les valeurs ne sont pas rognées : un label
// tronqué désignerait peut-être une autre unité, mieux vaut qu'il ne désigne
// rien.
func labelsOf(observed map[string]string) Labels {
	_, invalid := observed[LabelInvalid]
	return Labels{Service: observed[LabelService], Unit: observed[LabelUnit], Invalid: invalid}
}

// record garde la fiche et note la transition si l'état ou la santé a
// changé, ou si la fiche est nouvelle.
func (p *planner) record(next, previous Container, existed bool, transition Transition) {
	changed := !existed || next.State != previous.State || next.Health != previous.Health || next.IsArchived() != previous.IsArchived()
	if changed {
		transition.ContainerID = next.ID
		transition.NewState = next.State
		transition.NewHealth = next.Health
		transition.OOMKilled = next.OOMKilled && (next.State == StateExited || next.State == StateDead)
		transition.Killed = next.Killed && next.State == StateExited
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
	wasLiving := existed && !previous.IsArchived()
	switch {
	case !next.IsArchived() && !wasLiving:
		p.living++
	case next.IsArchived() && wasLiving:
		p.living--
	}
	p.known[next.ID] = next
	if index, seen := p.touched[next.ID]; seen {
		p.changes.Containers[index] = next
		return
	}
	p.touched[next.ID] = len(p.changes.Containers)
	p.changes.Containers = append(p.changes.Containers, next)
}

func contains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// List rend les conteneurs vivants, d'une machine ou de toutes.
func (t *Tracker) List(ctx context.Context, machineID string) ([]Container, error) {
	all, err := t.store.ListContainers(ctx, machineID)
	if err != nil {
		return nil, err
	}
	live := make([]Container, 0, len(all))
	for _, container := range all {
		if !container.IsArchived() {
			live = append(live, container)
		}
	}
	return live, nil
}

// Images rend l'image de chaque conteneur vivant, toutes machines confondues.
func (t *Tracker) Images(ctx context.Context) ([]string, error) {
	living, err := t.List(ctx, "")
	if err != nil {
		return nil, err
	}
	images := make([]string, 0, len(living))
	for _, container := range living {
		images = append(images, container.Image)
	}
	return images, nil
}

func (t *Tracker) Get(ctx context.Context, id string) (Container, error) {
	return t.store.GetContainer(ctx, id)
}

func (t *Tracker) Transitions(ctx context.Context, id string, limit int) ([]Transition, error) {
	if _, err := t.store.GetContainer(ctx, id); err != nil {
		return nil, err
	}
	return t.store.ListTransitions(ctx, id, limit)
}

// Topology rend ce que l'onglet Réseau d'une machine dessine : ses
// conteneurs vivants, ses groupes et ses arêtes.
func (t *Tracker) Topology(ctx context.Context, machineID string) (Topology, error) {
	containers, err := t.List(ctx, machineID)
	if err != nil {
		return Topology{}, err
	}
	networks, err := t.store.ListNetworks(ctx, machineID)
	if err != nil {
		return Topology{}, err
	}
	return BuildTopology(containers, networks), nil
}

// Engines rend ce que chaque machine a dit de son Docker.
func (t *Tracker) Engines(ctx context.Context) ([]Engine, error) {
	return t.store.ListEngines(ctx)
}

// CurrentAll rend la mesure courante de chaque conteneur qui en a une.
func (t *Tracker) CurrentAll(ctx context.Context) ([]Current, error) {
	samples, err := t.store.LatestContainerSamples(ctx)
	if err != nil {
		return nil, err
	}
	now := t.now()
	currents := make([]Current, 0, len(samples))
	for _, sample := range samples {
		fresh := now.Sub(sample.SampledAt) <= StaleAfter
		currents = append(currents, Current{ContainerID: sample.ContainerID, Available: fresh, Sample: &sample})
	}
	return currents, nil
}

// NeedsAttention dit si un conteneur va mal : c'est ce que compte la barre
// latérale. Le front dérive ses pastilles de la même règle.
func NeedsAttention(container Container) bool {
	switch container.State {
	case StateRunning:
		return container.Health == HealthUnhealthy
	case StateRestarting, StateDead:
		return true
	case StateExited:
		return !IsCleanStop(container.ExitCode, container.OOMKilled, container.Killed)
	}
	return false
}

// IsCleanExit : 0 est une fin normale, 137 et 143 un arrêt demandé
// (SIGKILL et SIGTERM, ce que docker stop envoie).
func IsCleanExit(code int) bool {
	return code == 0 || code == 137 || code == 143
}

// IsCleanStop : un code propre ne suffit pas. Le noyau qui tue faute de
// mémoire envoie aussi SIGKILL (137), Docker le dit par OOMKilled ; un
// docker kill aussi, l'agent le dit par killed quand aucun arrêt n'a été
// demandé juste avant.
func IsCleanStop(code int, oomKilled, killed bool) bool {
	return !oomKilled && !killed && IsCleanExit(code)
}

// Count rend le nombre de conteneurs vivants et ceux qui demandent attention.
func (t *Tracker) Count(ctx context.Context) (total, attention int, err error) {
	containers, err := t.List(ctx, "")
	if err != nil {
		return 0, 0, err
	}
	for _, container := range containers {
		if NeedsAttention(container) {
			attention++
		}
	}
	return len(containers), attention, nil
}

// Purge efface chaque table au-delà de sa rétention.
func (t *Tracker) Purge(ctx context.Context) error {
	now := t.now()
	return t.store.PurgeContainers(ctx, now.Add(-TransitionRetention), now.Add(-ArchivedRetention), now.Add(-SampleRetention))
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
		t.logger.Warn("purge containers", "error", err)
	}
}
