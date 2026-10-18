package manifest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

const (
	// applyTimeout borne une application : elle tourne détachée du client, il
	// faut qu'elle finisse.
	applyTimeout = 2 * time.Minute
	// journalTimeout borne l'écriture du journal qui ferme une ligne, faite
	// même quand l'application a épuisé son temps.
	journalTimeout = 10 * time.Second
)

var (
	// ErrPlanChanged : l'annuaire a changé depuis le plan que l'opérateur a
	// confirmé. L'application n'écrit rien ; elle rend le plan d'aujourd'hui.
	ErrPlanChanged = errors.New("the directory changed since the plan was confirmed")
	// ErrNotApplicable : le plan a des refus, rien ne s'écrit.
	ErrNotApplicable = errors.New("the plan has refusals")
	// ErrBusy : un autre apply est en cours.
	ErrBusy = errors.New("another apply is running")
	// ErrNothingToApply : le fichier est conforme, il n'y a rien à écrire.
	ErrNothingToApply = errors.New("nothing to apply")
	// errObjectMoved : un objet a changé entre le plan rejoué et son
	// écriture (un geste de l'interface, pendant l'application). L'écriture
	// s'arrête plutôt que d'écraser ce que l'opérateur vient de faire.
	errObjectMoved = errors.New("the object changed since the plan saw it")
)

// Request est un fichier à juger ou à appliquer. PlanHash est l'empreinte du
// plan que l'opérateur a lu et confirmé : l'application ne sert qu'à lui.
type Request struct {
	File     []byte
	FileName string
	Prune    bool
	PlanHash string
}

// Plan lit le fichier et dit ce qu'il changerait, sans rien écrire. Un fichier
// qui ne se lit pas ou qui est refusé rend un plan avec ses refus, pas une
// erreur : c'est une réponse, pas une panne.
func (m *Manifest) Plan(ctx context.Context, request Request) (Plan, error) {
	file, problems := Parse(request.File)
	var current Current
	if len(problems) == 0 {
		var err error
		if current, err = m.readCurrent(ctx); err != nil {
			return Plan{}, err
		}
	}
	plan := BuildPlan(file, problems, current, request.Prune, m.errorKey)
	plan.FileHash = HashBytes(request.File)
	if err := m.describeDeletions(ctx, &plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// readCurrent lit ce que le fichier est comparé à. Les lectures sont séparées :
// un geste de l'interface entre deux change le plan, ce que l'empreinte
// attrape à l'application.
func (m *Manifest) readCurrent(ctx context.Context) (Current, error) {
	names, err := m.deps.Machines.MachineNames(ctx)
	if err != nil {
		return Current{}, fmt.Errorf("read machine names: %w", err)
	}
	projects, err := m.deps.Directory.ListProjects(ctx)
	if err != nil {
		return Current{}, fmt.Errorf("read projects: %w", err)
	}
	services, err := m.deps.Directory.ListServices(ctx, directory.Filter{})
	if err != nil {
		return Current{}, fmt.Errorf("read services: %w", err)
	}
	probes, err := m.deps.Probes.List(ctx, "")
	if err != nil {
		return Current{}, fmt.Errorf("read probes: %w", err)
	}
	heartbeats, err := m.deps.Heartbeats.List(ctx)
	if err != nil {
		return Current{}, fmt.Errorf("read heartbeats: %w", err)
	}
	ignored, err := m.deps.Directory.IgnoredKeys(ctx)
	if err != nil {
		return Current{}, fmt.Errorf("read ignored keys: %w", err)
	}
	return Current{
		Machines: names, LocalMachineID: machine.LocalID,
		Projects: projects, Services: services, Probes: probes, Heartbeats: heartbeats, Ignored: ignored,
	}, nil
}

// describeDeletions dit ce que chaque suppression emporte (l'historique d'une
// sonde ou d'une tâche) et refuse celle d'un service qu'un composant de
// statut vise : les composants ne sont pas dans le fichier, le plan ne peut
// pas les détacher.
func (m *Manifest) describeDeletions(ctx context.Context, plan *Plan) error {
	for index := range plan.steps {
		change := &plan.steps[index].change
		if change.Action != ActionDelete {
			continue
		}
		switch change.Kind {
		case KindProbe:
			results, days, err := m.deps.History.CountProbeHistory(ctx, change.ID)
			if err != nil {
				return fmt.Errorf("count probe history: %w", err)
			}
			change.Carries = countedCarries(Carry{What: CarryProbeResults, Count: results}, Carry{What: CarryProbeDays, Count: days})
		case KindHeartbeat:
			pings, runs, err := m.deps.History.CountHeartbeatHistory(ctx, change.ID)
			if err != nil {
				return fmt.Errorf("count heartbeat history: %w", err)
			}
			change.Carries = countedCarries(Carry{What: CarryHeartbeatPings, Count: pings}, Carry{What: CarryHeartbeatRuns, Count: runs})
		case KindService:
			links, err := m.deps.Directory.ServiceLinks(ctx, change.ID)
			if err != nil {
				return fmt.Errorf("read service links: %w", err)
			}
			if len(links.Components) > 0 {
				plan.Problems = append(plan.Problems, Problem{Key: KeyPruneBlocked, Path: change.Name, Value: componentNames(links.Components)})
			}
		}
		plan.Changes[index] = *change
	}
	return nil
}

func componentNames(components []directory.LinkedObject) string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, component.Name)
	}
	return joinLabels(names)
}

// Apply écrit ce que le fichier demande, si l'annuaire est encore tel que le
// plan confirmé l'a vu. Pas de transaction unique : chaque objet passe par le
// geste de son domaine, dans sa transaction, ce qui pousse aux agents leurs
// sondes et relance le rapprochement. Ce qui tient lieu de garantie :
//
//   - le fichier est jugé en entier avant la première écriture ;
//   - le plan est rejoué sous verrou et comparé à l'empreinte confirmée ;
//   - l'exécution est détachée du client, et bornée ;
//   - un échec s'arrête au premier refus, se journalise, et une relance qui
//     reprend le même fichier converge : ce qui est fait ressort « inchangé ».
//
// Quand le plan a changé ou a des refus, le plan rendu est celui d'aujourd'hui.
func (m *Manifest) Apply(ctx context.Context, request Request, actor Actor) (Apply, Plan, error) {
	if !m.applying.TryLock() {
		return Apply{}, Plan{}, ErrBusy
	}
	defer m.applying.Unlock()
	// Un Ctrl-C ou une coupure du client ne coupe pas l'écriture au milieu.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyTimeout)
	defer cancel()

	plan, err := m.Plan(ctx, request)
	if err != nil {
		return Apply{}, Plan{}, err
	}
	switch {
	case !plan.Applicable():
		return Apply{}, plan, ErrNotApplicable
	case plan.Hash != request.PlanHash:
		return Apply{}, plan, ErrPlanChanged
	case len(plan.Changes) == 0:
		return Apply{}, plan, ErrNothingToApply
	}
	record, err := m.confirm(ctx, request, plan, actor)
	if err != nil {
		return Apply{}, plan, err
	}
	return m.run(ctx, record, plan), plan, nil
}

// confirm ouvre la ligne du journal : à partir d'ici, tout arrêt se lit.
func (m *Manifest) confirm(ctx context.Context, request Request, plan Plan, actor Actor) (Apply, error) {
	id, err := newApplyID()
	if err != nil {
		return Apply{}, fmt.Errorf("generate apply id: %w", err)
	}
	created, updated, deleted := plan.Counts()
	record := Apply{
		ID: id, At: m.now(), Actor: actor,
		FileHash: plan.FileHash, FileName: request.FileName, Prune: plan.Prune, PlanHash: plan.Hash,
		State:   ApplyConfirmed,
		Created: created, Updated: updated, Deleted: deleted, Unchanged: plan.Unchanged,
		Changes: plan.Changes,
	}
	if err := m.deps.Journal.InsertApply(ctx, record); err != nil {
		return Apply{}, fmt.Errorf("record apply: %w", err)
	}
	return record, nil
}

// run écrit les changements un à un et ferme la ligne du journal, quoi qu'il
// arrive.
func (m *Manifest) run(ctx context.Context, record Apply, plan Plan) (result Apply) {
	end := ApplyEnd{State: ApplyFailed, Error: KeyApplyFailed}
	defer func() {
		// Une panique laisse la ligne fermée en échec, puis suit son cours.
		panicked := recover()
		m.finish(ctx, &record, end)
		result = record
		if panicked != nil {
			panic(panicked)
		}
	}()
	end = m.execute(ctx, record.ID, plan)
	return record
}

// finish ferme la ligne, avec son propre délai : l'application a pu épuiser le
// sien.
func (m *Manifest) finish(ctx context.Context, record *Apply, end ApplyEnd) {
	end.FinishedAt = m.now()
	journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), journalTimeout)
	defer cancel()
	if err := m.deps.Journal.FinishApply(journalCtx, record.ID, end); err != nil {
		m.logger.Error("close apply record", "apply_id", record.ID, "error", err)
	}
	record.State, record.FinishedAt = end.State, end.FinishedAt
	record.Done, record.Error, record.ErrorObject = end.Done, end.Error, end.ErrorObject
	if m.watcher != nil {
		m.watcher.DirectoryChanged()
	}
	if end.State == ApplyApplied {
		m.logger.Info("manifest applied", "apply_id", record.ID, "created", record.Created, "updated", record.Updated, "deleted", record.Deleted)
		return
	}
	m.logger.Warn("manifest apply failed", "apply_id", record.ID, "done", end.Done, "total", len(record.Changes),
		"error", end.Error, "object", end.ErrorObject)
}

// execute écrit les changements dans l'ordre du plan et s'arrête au premier
// refus. Ce qu'il rend ferme la ligne du journal.
func (m *Manifest) execute(ctx context.Context, applyID string, plan Plan) ApplyEnd {
	run := &execution{manifest: m, projects: maps.Clone(plan.projects), services: maps.Clone(plan.services)}
	if run.projects == nil {
		run.projects = map[string]string{}
	}
	if run.services == nil {
		run.services = map[string]string{}
	}
	done := 0
	for _, next := range plan.steps {
		label := next.change.Label()
		// L'arrêt du serveur et le délai se jugent entre deux changements :
		// un changement commencé va jusqu'au bout.
		if m.lifetime.Err() != nil {
			return ApplyEnd{State: ApplyFailed, Done: done, Error: KeyInterrupted, ErrorObject: label}
		}
		if ctx.Err() != nil {
			return ApplyEnd{State: ApplyFailed, Done: done, Error: KeyTimeout, ErrorObject: label}
		}
		if err := run.write(ctx, next); err != nil {
			m.logger.Warn("apply step refused", "apply_id", applyID, "step", done+1, "object", label, "error", err)
			return ApplyEnd{State: ApplyFailed, Done: done, Error: m.failureKey(err), ErrorObject: label}
		}
		done++
		m.recordProgress(ctx, applyID, done)
	}
	return ApplyEnd{State: ApplyApplied, Done: done}
}

// recordProgress dit au journal combien de changements sont écrits. Un échec
// ici ne coupe pas l'application : le compte final est écrit à la fermeture.
func (m *Manifest) recordProgress(ctx context.Context, applyID string, done int) {
	if err := m.deps.Journal.SetApplyProgress(ctx, applyID, done); err != nil {
		m.logger.Warn("record apply progress", "apply_id", applyID, "done", done, "error", err)
	}
}

// failureKey dit pourquoi un changement a été refusé, par une clé du
// catalogue. Un objet disparu entre le plan et l'écriture est un plan périmé.
func (m *Manifest) failureKey(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return KeyTimeout
	// Une clé prise au moment d'écrire, que le plan avait trouvée libre :
	// quelqu'un l'a prise entre-temps.
	case errors.Is(err, errObjectMoved), errors.Is(err, directory.ErrNameTaken), errors.Is(err, directory.ErrComposeProjectTaken),
		errors.Is(err, directory.ErrMatchKeyTaken), errors.Is(err, directory.ErrProjectNameTaken),
		errors.Is(err, directory.ErrNotFound), errors.Is(err, directory.ErrProjectNotFound),
		errors.Is(err, probe.ErrNotFound), errors.Is(err, heartbeat.ErrNotFound), errors.Is(err, directory.ErrIgnoredNotFound):
		return KeyPlanChanged
	}
	if key := m.errorKey(err); key != "" {
		return key
	}
	return KeyApplyFailed
}

// FailInterrupted passe en échec les passages qu'un arrêt brutal a laissés en
// cours : au démarrage, plus rien ne les écrit. Rend leur nombre.
func (m *Manifest) FailInterrupted(ctx context.Context) (int, error) {
	count, err := m.deps.Journal.FailInterruptedApplies(ctx, m.now(), KeyInterrupted)
	if err != nil {
		return 0, fmt.Errorf("fail interrupted applies: %w", err)
	}
	if count > 0 {
		m.logger.Warn("apply interrupted by a stop, marked failed", "count", count)
	}
	return count, nil
}

// Applies rend les derniers passages, du plus récent au plus ancien.
func (m *Manifest) Applies(ctx context.Context, limit int) ([]Apply, error) {
	return m.deps.Journal.ListApplies(ctx, limit)
}

// GetApply rend un passage avec son plan.
func (m *Manifest) GetApply(ctx context.Context, id string) (Apply, error) {
	return m.deps.Journal.GetApply(ctx, id)
}

// execution porte les tables que l'écriture met à jour au fil des créations :
// les identifiants réels des projets et des services par leur nom.
type execution struct {
	manifest *Manifest
	projects map[string]string
	services map[string]string
}

// write applique un changement par le geste de son domaine.
func (e *execution) write(ctx context.Context, next step) error {
	switch next.change.Kind {
	case KindProject:
		return e.writeProject(ctx, next)
	case KindService:
		return e.writeService(ctx, next)
	case KindProbe:
		return e.writeProbe(ctx, next)
	case KindHeartbeat:
		return e.writeHeartbeat(ctx, next)
	case KindIgnored:
		return e.writeIgnored(ctx, next)
	}
	return fmt.Errorf("unknown change kind %q", next.change.Kind)
}

func (e *execution) writeProject(ctx context.Context, next step) error {
	directoryService := e.manifest.deps.Directory
	switch next.change.Action {
	case ActionCreate:
		created, err := directoryService.CreateProject(ctx, next.project.Definition)
		if err == nil {
			e.projects[created.Name] = created.ID
		}
		return err
	case ActionUpdate:
		if err := e.guardProject(ctx, next); err != nil {
			return err
		}
		_, err := directoryService.UpdateProject(ctx, next.change.ID, next.project.Definition)
		return err
	}
	if err := e.guardProject(ctx, next); err != nil {
		return err
	}
	return directoryService.DeleteProject(ctx, next.change.ID)
}

// guardProject relit le projet juste avant de l'écrire : s'il n'est plus tel
// que le plan l'a vu, rien ne s'écrit.
func (e *execution) guardProject(ctx context.Context, next step) error {
	projects, err := e.manifest.deps.Directory.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.ID != next.change.ID {
			continue
		}
		now := directory.ProjectDefinition{Name: project.Name, Title: project.Title}.Complete()
		if len(diffProject(next.project.Before, now)) > 0 {
			return errObjectMoved
		}
		return nil
	}
	return directory.ErrProjectNotFound
}

// writeService relit le service avant de l'éditer : les unités se retrouvent
// par leur nom, et leur identifiant est reporté. Sans ce report, la politique
// de mise à jour et le rattachement des conteneurs d'une unité seraient
// perdus à chaque édition, l'unité étant recréée.
func (e *execution) writeService(ctx context.Context, next step) error {
	directoryService := e.manifest.deps.Directory
	if next.change.Action == ActionDelete {
		if _, err := e.guardedService(ctx, next); err != nil {
			return err
		}
		return directoryService.DeleteService(ctx, next.change.ID)
	}
	definition := next.service.Wanted
	projectIDs, err := e.projectIDs(next.service.Projects)
	if err != nil {
		return err
	}
	definition.ProjectIDs = projectIDs
	if next.change.Action == ActionCreate {
		created, err := directoryService.CreateService(ctx, definition)
		if err == nil {
			e.services[next.change.Name] = created.ID
		}
		return err
	}
	existing, err := e.guardedService(ctx, next)
	if err != nil {
		return err
	}
	definition.Units = withKnownUnitIDs(definition.Units, existing.Units)
	_, err = directoryService.UpdateService(ctx, next.change.ID, definition)
	return err
}

// sameService dit que le service relu est celui que le plan a vu : aucun champ
// du fichier n'a changé (les ensembles se comparent comme des ensembles), et
// chaque unité est toujours celle de même identifiant, pas une recréée.
func sameService(seen, now directory.ServiceDefinition) bool {
	if len(diffService(seen, now, labels{}).Fields) > 0 {
		return false
	}
	ids := make(map[string]string, len(seen.Units))
	for _, unit := range seen.Units {
		ids[unit.Name] = unit.ID
	}
	for _, unit := range now.Units {
		if ids[unit.Name] != unit.ID {
			return false
		}
	}
	return true
}

// guardedService relit le service juste avant de l'écrire et le compare à ce
// que le plan a vu. Une modification faite dans l'interface pendant
// l'application (un titre, une unité renommée) l'arrête : elle ne s'écrase pas
// sans bruit, et la relance recalcule le plan.
func (e *execution) guardedService(ctx context.Context, next step) (directory.Service, error) {
	existing, err := e.manifest.deps.Directory.GetService(ctx, next.change.ID)
	if err != nil {
		return directory.Service{}, err
	}
	if !sameService(next.service.Before, existing.Definition().Complete()) {
		return directory.Service{}, errObjectMoved
	}
	return existing, nil
}

// withKnownUnitIDs donne à chaque unité du fichier l'identifiant de l'unité
// de même nom dans le service : le fichier n'écrit pas d'identifiant d'unité.
func withKnownUnitIDs(wanted []directory.UnitDefinition, existing []directory.Unit) []directory.UnitDefinition {
	kept := make([]directory.UnitDefinition, 0, len(wanted))
	for _, unit := range wanted {
		unit.ID = ""
		for _, found := range existing {
			if found.Name == unit.Name {
				unit.ID = found.ID
				break
			}
		}
		kept = append(kept, unit)
	}
	return kept
}

// projectIDs résout les noms de projet sur la table tenue à jour au fil des
// créations. Un nom qui manque est un plan périmé : le projet devait exister.
func (e *execution) projectIDs(names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, found := e.projects[strings.TrimSpace(name)]
		if !found || strings.HasPrefix(id, pendingPrefix) {
			return nil, directory.ErrProjectNotFound
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (e *execution) writeProbe(ctx context.Context, next step) error {
	probes := e.manifest.deps.Probes
	if next.change.Action != ActionCreate {
		current, err := probes.Get(ctx, next.change.ID)
		if err != nil {
			return err
		}
		if len(diffProbe(next.probe.Before, current.Definition().Complete(), labels{})) > 0 {
			return errObjectMoved
		}
	}
	if next.change.Action == ActionDelete {
		return probes.Delete(ctx, next.change.ID)
	}
	definition := next.probe.Wanted
	definition.ServiceID = ""
	if next.probe.ServiceRef != "" {
		id, found := e.services[next.probe.ServiceRef]
		if !found || strings.HasPrefix(id, pendingPrefix) {
			return directory.ErrNotFound
		}
		definition.ServiceID = id
	}
	if next.change.Action == ActionCreate {
		_, err := probes.Create(ctx, definition)
		return err
	}
	_, err := probes.Update(ctx, next.change.ID, definition)
	return err
}

// writeHeartbeat ne touche jamais au jeton : une tâche existante garde le
// sien (aucun appel à RegenerateToken), une tâche créée reçoit celui que le
// serveur tire.
func (e *execution) writeHeartbeat(ctx context.Context, next step) error {
	heartbeats := e.manifest.deps.Heartbeats
	if next.change.Action != ActionCreate {
		current, err := heartbeats.Get(ctx, next.change.ID)
		if err != nil {
			return err
		}
		if len(diffHeartbeat(next.heartbeat.Before, current.Definition(), labels{})) > 0 {
			return errObjectMoved
		}
	}
	switch next.change.Action {
	case ActionCreate:
		_, err := heartbeats.Create(ctx, next.heartbeat.Wanted)
		return err
	case ActionUpdate:
		_, err := heartbeats.Update(ctx, next.change.ID, next.heartbeat.Wanted)
		return err
	}
	return heartbeats.Delete(ctx, next.change.ID)
}

func (e *execution) writeIgnored(ctx context.Context, next step) error {
	if next.change.Action == ActionCreate {
		return e.manifest.deps.Directory.Ignore(ctx, next.ignored.Key)
	}
	return e.manifest.deps.Directory.Unignore(ctx, next.ignored.Key)
}
