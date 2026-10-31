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
	// ListNetworks rend les réseaux d'une machine, par nom.
	ListNetworks(ctx context.Context, machineID string) ([]Network, error)
	// ListVolumes rend les volumes d'une machine, par nom.
	ListVolumes(ctx context.Context, machineID string) ([]Volume, error)
	PurgeContainers(ctx context.Context, transitionsBefore, archivedBefore, samplesBefore time.Time) error
	// SetContainerUnits écrit le rattachement des fiches nommées, et rien
	// d'autre ; une unité disparue entre-temps s'écrit comme « non déclaré ».
	SetContainerUnits(ctx context.Context, assignments []UnitAssignment) error
}

// UnitMatch est l'unité à laquelle l'annuaire rattache un conteneur, par
// quel niveau, et le service déclaré qui porte l'unité.
type UnitMatch struct {
	UnitID    string
	Level     MatchLevel
	ServiceID string
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
	// ReplaceVolumes remplace de même les volumes de la machine.
	ReplaceVolumes bool
	Volumes        []Volume
	Transitions    []Transition
	Samples        []Sample
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

// Get rend une fiche par son identifiant, archivée comprise : c'est ce que
// l'annuaire lit pour proposer un conteneur ; il ne peut pas recevoir le
// Reader sans cycle d'imports.
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
// latérale, écrit en SQL dans le store (containerNeedsAttention) et tenu
// égal à celui-ci par un test de parité. Le front dérive ses pastilles de
// la même règle.
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
