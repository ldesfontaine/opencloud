package container

import (
	"context"
	"time"
)

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
		// L'alerte du conteneur porte son service : la fiche le sait dès ce rapport.
		changes.Containers[index].DeclaredServiceID = match.ServiceID
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
	if report.VolumesComplete {
		plan.applyVolumes(report.Volumes)
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

func (p *planner) applyVolumes(volumes []VolumeReport) {
	p.changes.ReplaceVolumes = true
	p.changes.Volumes = make([]Volume, 0, len(volumes))
	for _, volume := range volumes {
		p.changes.Volumes = append(p.changes.Volumes, Volume{
			MachineID: p.machineID, Name: volume.Name, Driver: volume.Driver, UsedBy: volume.UsedBy, SeenAt: p.now,
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
		event.State, event.ExitCode, event.Killed, event.StopRequested = "", nil, false, false
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
	if event.StopRequested {
		next.StopRequested = true
	}
	if next.State == StateRunning {
		// Docker oublie le manque de mémoire au redémarrage.
		next.OOMKilled = false
		next.Killed = false
		next.StopRequested = false
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
		sameStop := container.State == previous.State && container.FinishedAt.Equal(previous.FinishedAt)
		container.Killed = previous.Killed && sameStop
		container.StopRequested = previous.StopRequested && sameStop
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
		transition.StopRequested = next.StopRequested && next.State == StateExited
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
