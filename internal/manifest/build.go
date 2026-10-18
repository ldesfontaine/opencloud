package manifest

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Current est l'annuaire tel que la base le montre, lu d'un coup : ce que le
// fichier est comparé à.
type Current struct {
	// Machines donne le nom de chaque machine, par identifiant.
	Machines map[string]string
	// LocalMachineID est la machine openCloud, celle d'une sonde sans
	// « runner ».
	LocalMachineID string
	Projects       []directory.Project
	Services       []directory.Service
	Probes         []probe.Probe
	Heartbeats     []heartbeat.Heartbeat
	Ignored        []directory.IgnoredKey
}

// ErrorKeyFunc traduit l'erreur d'un composant du domaine en clé du
// catalogue, ou rend « » quand elle n'en a pas. Le paquet server la
// fournit : manifest n'importe pas server.
type ErrorKeyFunc func(err error) string

// step est un changement prêt à s'écrire : ce qu'on en affiche, et de quoi
// le faire. Les identifiants des objets que le plan crée ne sont pas connus
// avant l'application : les références restent des noms, résolus à l'écriture.
type step struct {
	change    Change
	project   projectStep
	service   serviceStep
	probe     probeStep
	heartbeat heartbeatStep
	ignored   ignoredStep
}

type projectStep struct {
	Definition directory.ProjectDefinition
	// Before est la définition que le plan a vue : l'application la relit
	// avant d'écrire, et s'arrête si l'objet a bougé entre-temps.
	Before directory.ProjectDefinition
}

type serviceStep struct {
	MachineID string
	// Wanted a pour ProjectIDs des identifiants réels ou en attente
	// (pendingPrefix) ; Projects en garde les noms, que l'application résout
	// sur sa table au moment d'écrire.
	Wanted   directory.ServiceDefinition
	Projects []string
	// Before est la définition que le plan a vue, pour une modification ou
	// une suppression.
	Before directory.ServiceDefinition
}

type probeStep struct {
	Wanted probe.Definition
	// ServiceRef est « machine/nom » ; vide, aucun service.
	ServiceRef string
	// Before est la définition que le plan a vue, pour une modification ou
	// une suppression.
	Before probe.Definition
}

type heartbeatStep struct {
	Wanted heartbeat.Definition
	// Before est la définition que le plan a vue, pour une modification ou
	// une suppression.
	Before heartbeat.Definition
}

type ignoredStep struct {
	Key directory.IgnoredKey
}

// builder construit un plan. Il ne lit rien et n'écrit rien : le fichier, la
// base lue, et des règles.
type builder struct {
	file     File
	current  Current
	prune    bool
	errorKey ErrorKeyFunc

	problems  []Problem
	unchanged int
	unmanaged []Ref

	// machineIDs donne l'identifiant d'une machine par son nom.
	machineIDs map[string]string
	names      labels
	// Les tables de l'état visé : le nom que le fichier peut citer, et
	// l'identifiant de l'objet, réel ou en attente.
	projectTargets map[string]string
	serviceTargets map[string]string

	// Les changements de chaque genre. Projets et services tiennent des clés
	// d'unicité : leur ordre d'écriture se règle à part, voir orderKeyed.
	projectUpdates, projectCreates []keyedStep
	serviceUpdates, serviceCreates []keyedStep
	projectSteps, serviceSteps     []step
	projectDeletes, serviceDeletes []step
	probeDeletes, probeUpdates     []step
	probeCreates                   []step
	heartbeatDeletes               []step
	heartbeatUpdates               []step
	heartbeatCreates               []step
	ignoredCreates, ignoredDeletes []step
	// Les clés tenues par ce que --prune supprime.
	prunedProjectKeys, prunedServiceKeys map[string]bool
}

// BuildPlan compare le fichier à l'annuaire. C'est une fonction pure : le
// même fichier face à la même base donne le même plan, octet pour octet. Les
// refus de lecture (parsed) arrêtent tout : il n'y a pas de plan d'un fichier
// qu'on ne sait pas lire.
func BuildPlan(file File, parsed []Problem, current Current, prune bool, errorKey ErrorKeyFunc) Plan {
	b := newBuilder(file, current, prune, errorKey)
	if len(parsed) > 0 {
		b.problems = slices.Clone(parsed)
		return b.finish()
	}
	b.checkMachines()
	b.planProjects()
	b.planServices()
	b.planProbes()
	b.planHeartbeats()
	b.planIgnored()
	return b.finish()
}

func newBuilder(file File, current Current, prune bool, errorKey ErrorKeyFunc) *builder {
	if errorKey == nil {
		errorKey = func(error) string { return "" }
	}
	b := &builder{
		file: file, current: current, prune: prune, errorKey: errorKey,
		machineIDs:     make(map[string]string, len(current.Machines)),
		projectTargets: map[string]string{},
		serviceTargets: map[string]string{},
		names: labels{
			machines: current.Machines,
			projects: map[string]string{},
			services: map[string]string{},
		},
		prunedProjectKeys: map[string]bool{},
		prunedServiceKeys: map[string]bool{},
	}
	for id, name := range current.Machines {
		b.machineIDs[name] = id
	}
	return b
}

// refuse note un refus à une ligne du fichier. path nomme l'entrée visée.
func (b *builder) refuse(key string, line int, path, value string) {
	if len(b.problems) >= maxProblems {
		return
	}
	b.problems = append(b.problems, Problem{Key: key, Line: line, Path: path, Value: value})
}

// refuseError note le refus d'un composant du domaine, sous sa clé. Une
// erreur sans clé devient un refus générique : jamais le texte de l'erreur,
// qui a pu citer ce que le fichier contient.
func (b *builder) refuseError(err error, line int, path string) {
	if key := b.errorKey(err); key != "" {
		b.refuse(key, line, path, "")
		return
	}
	b.refuse(KeyEntryInvalid, line, path, path)
}

// checkMachines lit la section « machines » : chaque nom doit être celui d'une
// machine connue, une fois. Le fichier ne crée jamais une machine.
func (b *builder) checkMachines() {
	seen := map[string]bool{}
	for _, entry := range b.file.Machines {
		switch {
		case entry.Name == "":
			b.refuse(KeyEntryInvalid, entry.Line, "machines", "machines")
		case seen[entry.Name]:
			b.refuse(KeyNameDuplicate, entry.Line, entry.Name, entry.Name)
		default:
			if _, known := b.machineIDs[entry.Name]; !known {
				b.refuse(KeyMachineUnknown, entry.Line, entry.Name, entry.Name)
			}
		}
		seen[entry.Name] = true
	}
}

// machineID résout un nom de machine du fichier. Un nom inconnu est un refus
// du plan, à la ligne de l'entrée.
func (b *builder) machineID(name string, line int, path string) (string, bool) {
	id, known := b.machineIDs[name]
	if !known {
		b.refuse(KeyMachineUnknown, line, path, name)
	}
	return id, known
}

// --- Les projets ---

func (b *builder) planProjects() {
	entries := make([]entryRef, 0, len(b.file.Projects))
	for _, entry := range b.file.Projects {
		entries = append(entries, entryRef{id: entry.ID, name: entry.Name, line: entry.Line})
	}
	objects := make([]objectRef, 0, len(b.current.Projects))
	for _, project := range b.current.Projects {
		objects = append(objects, objectRef{id: project.ID, name: project.Name})
		b.names.projects[project.ID] = project.Name
	}
	matched := b.match(entries, objects, true)

	holders := keyHolders{}
	for index, entry := range b.file.Projects {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		wanted := directory.ProjectDefinition{Name: entry.Name, Title: entry.Title}.Complete()
		if found == createEntry {
			pending := pendingPrefix + wanted.Name
			b.projectTargets[wanted.Name] = pending
			b.names.projects[pending] = wanted.Name
			b.createProject(entry, wanted, holders)
			continue
		}
		existing := b.current.Projects[found]
		b.projectTargets[wanted.Name] = existing.ID
		b.names.projects[existing.ID] = wanted.Name
		b.updateProject(entry, existing, wanted, holders)
	}
	b.keepOrPruneProjects(matched, holders)
	holders.report(b, func(string) error { return directory.ErrProjectNameTaken })
	b.projectSteps = b.orderKeyed(b.projectUpdates, b.projectCreates, b.prunedProjectKeys)
}

func (b *builder) createProject(entry Project, wanted directory.ProjectDefinition, holders keyHolders) {
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, entry.Name)
		return
	}
	key := "project:" + wanted.Name
	holders.claim(key, keyHolder{label: wanted.Name, line: entry.Line, fromFile: true})
	fields := diffProject(directory.ProjectDefinition{}, wanted)
	change := Change{Action: ActionCreate, Kind: KindProject, Name: wanted.Name, Fields: afterOnly(fields)}
	b.projectCreates = append(b.projectCreates, keyedStep{
		step:  step{change: change, project: projectStep{Definition: wanted}},
		label: wanted.Name, line: entry.Line, wants: []string{key},
	})
}

func (b *builder) updateProject(entry Project, existing directory.Project, wanted directory.ProjectDefinition, holders keyHolders) {
	before := directory.ProjectDefinition{Name: existing.Name, Title: existing.Title}.Complete()
	key := "project:" + wanted.Name
	holders.claim(key, keyHolder{label: wanted.Name, line: entry.Line, fromFile: true})
	fields := diffProject(before, wanted)
	if len(fields) == 0 {
		b.unchanged++
		return
	}
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, entry.Name)
		return
	}
	change := Change{Action: ActionUpdate, Kind: KindProject, ID: existing.ID, Name: wanted.Name, Fields: fields}
	b.projectUpdates = append(b.projectUpdates, keyedStep{
		step:  step{change: change, project: projectStep{Definition: wanted, Before: before}},
		label: wanted.Name, line: entry.Line,
		holds: []string{"project:" + existing.Name}, wants: []string{key},
	})
}

// keepOrPruneProjects traite les projets que le fichier ne nomme pas : gardés
// (et leur nom reste pris), ou supprimés avec --prune.
func (b *builder) keepOrPruneProjects(matched matching, holders keyHolders) {
	for index, project := range b.current.Projects {
		if matched.claimed[index] {
			continue
		}
		key := "project:" + project.Name
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindProject, ID: project.ID, Name: project.Name})
			b.projectTargets[project.Name] = project.ID
			holders.claim(key, keyHolder{label: project.Name})
			continue
		}
		b.prunedProjectKeys[key] = true
		change := Change{
			Action: ActionDelete, Kind: KindProject, ID: project.ID, Name: project.Name,
			Carries: countedCarries(Carry{What: CarryProjectLinks, Count: len(project.ServiceIDs)}),
		}
		before := directory.ProjectDefinition{Name: project.Name, Title: project.Title}.Complete()
		b.projectDeletes = append(b.projectDeletes, step{change: change, project: projectStep{Before: before}})
	}
}

// afterOnly donne à un champ créé sa seule valeur d'après : il n'avait pas
// d'avant.
func afterOnly(fields []FieldChange) []FieldChange {
	for index := range fields {
		fields[index].Before = ""
	}
	return fields
}

// countedCarries ne garde que ce qui emporte vraiment quelque chose.
func countedCarries(carries ...Carry) []Carry {
	kept := []Carry{}
	for _, carry := range carries {
		if carry.Count > 0 {
			kept = append(kept, carry)
		}
	}
	return kept
}

// --- Les services ---

func serviceRef(machine, name string) string {
	return machine + "/" + name
}

func (b *builder) planServices() {
	entries := make([]entryRef, 0, len(b.file.Services))
	for _, entry := range b.file.Services {
		entries = append(entries, entryRef{id: entry.ID, name: serviceRef(entry.Machine, entry.Name), line: entry.Line})
	}
	objects := make([]objectRef, 0, len(b.current.Services))
	for _, service := range b.current.Services {
		ref := serviceRef(service.MachineName, service.Name)
		objects = append(objects, objectRef{id: service.ID, name: ref})
		b.names.services[service.ID] = ref
	}
	matched := b.match(entries, objects, true)

	// L'état visé d'abord : les sondes citent les services par leur nom d'après.
	for index, entry := range b.file.Services {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		ref := serviceRef(entry.Machine, entry.Name)
		id := pendingPrefix + ref
		if found != createEntry {
			id = b.current.Services[found].ID
		}
		if _, taken := b.serviceTargets[ref]; !taken {
			b.serviceTargets[ref] = id
		}
		b.names.services[id] = ref
	}
	for index, service := range b.current.Services {
		if matched.claimed[index] || b.prune {
			continue
		}
		ref := serviceRef(service.MachineName, service.Name)
		if _, taken := b.serviceTargets[ref]; !taken {
			b.serviceTargets[ref] = service.ID
		}
	}

	holders := keyHolders{}
	for index, entry := range b.file.Services {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		b.planService(entry, found, holders)
	}
	b.keepOrPruneServices(matched, holders)
	holders.report(b, serviceKeyError)
	b.serviceSteps = b.orderKeyed(b.serviceUpdates, b.serviceCreates, b.prunedServiceKeys)
}

// serviceKeyError dit quelle règle d'unicité la clé enfreint.
func serviceKeyError(key string) error {
	switch {
	case strings.HasPrefix(key, "compose:"):
		return directory.ErrComposeProjectTaken
	case strings.HasPrefix(key, "unit:"):
		return directory.ErrMatchKeyTaken
	}
	return directory.ErrNameTaken
}

// serviceKeys dit les clés d'unicité d'un service : son nom, son projet
// Compose et les clés d'observation de ses unités, chacune par machine.
func serviceKeys(machineID string, definition directory.ServiceDefinition) []string {
	keys := []string{"name:" + machineID + "/" + definition.Name}
	if definition.Origin == directory.OriginCompose && definition.ComposeProject != "" {
		keys = append(keys, "compose:"+machineID+"/"+definition.ComposeProject)
	}
	for _, unit := range definition.Units {
		if unit.ContainerName != "" {
			keys = append(keys, "unit:"+machineID+"/container:"+unit.ContainerName)
		}
		if unit.SystemdUnit != "" {
			keys = append(keys, "unit:"+machineID+"/systemd:"+unit.SystemdUnit)
		}
	}
	return keys
}

// wantedService traduit l'entrée du fichier en définition du domaine, avec des
// identifiants réels ou en attente. ok est faux quand une référence n'existe
// pas : le refus est noté, et l'entrée n'est pas jugée plus loin.
func (b *builder) wantedService(entry Service) (definition directory.ServiceDefinition, projects []string, ok bool) {
	ok = true
	path := serviceRef(entry.Machine, entry.Name)
	machineID := ""
	if entry.Machine != "" {
		var known bool
		if machineID, known = b.machineID(entry.Machine, entry.Line, path); !known {
			ok = false
		}
	}
	projectIDs := make([]string, 0, len(entry.Projects))
	for _, name := range entry.Projects {
		id, known := b.projectTargets[strings.TrimSpace(name)]
		if !known {
			b.refuse(KeyProjectUnknown, entry.Line, path, name)
			ok = false
			continue
		}
		projectIDs = append(projectIDs, id)
	}
	units := make([]directory.UnitDefinition, 0, len(entry.Units))
	for _, unit := range entry.Units {
		units = append(units, directory.UnitDefinition{
			Name: unit.Name, Image: unit.Image, Version: unit.Version, Digest: unit.Digest,
			ContainerName: unit.ContainerName, SystemdUnit: unit.SystemdUnit, Ports: slices.Clone(unit.Ports),
			UpdatePolicy: unit.Updates,
		})
	}
	definition = directory.ServiceDefinition{
		MachineID: machineID, Name: entry.Name, Title: entry.Title, Nature: entry.Nature, Origin: entry.Origin,
		ComposeProject: entry.ComposeProject, PublicURL: entry.PublicURL,
		Provides: slices.Clone(entry.Provides), Units: units, ProjectIDs: projectIDs,
	}
	return definition.Complete(), slices.Clone(entry.Projects), ok
}

func (b *builder) planService(entry Service, found int, holders keyHolders) {
	path := serviceRef(entry.Machine, entry.Name)
	wanted, projects, resolved := b.wantedService(entry)
	if !resolved {
		return
	}
	if found == createEntry {
		b.createService(entry, wanted, projects, holders)
		return
	}
	existing := b.current.Services[found]
	before := existing.Definition().Complete()
	diff := diffService(before, wanted, b.names)
	holders.claimAll(serviceKeys(wanted.MachineID, wanted), keyHolder{label: path, line: entry.Line, fromFile: true})
	if len(diff.Fields) == 0 {
		b.unchanged++
		return
	}
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, path)
		return
	}
	change := Change{
		Action: ActionUpdate, Kind: KindService, ID: existing.ID, Name: path,
		Fields: diff.Fields, Notes: uniqueNotes(diff.Notes),
	}
	b.serviceUpdates = append(b.serviceUpdates, keyedStep{
		step: step{
			change:  change,
			service: serviceStep{MachineID: wanted.MachineID, Wanted: wanted, Projects: projects, Before: before},
		},
		label: path, line: entry.Line,
		holds: serviceKeys(existing.MachineID, before), wants: serviceKeys(wanted.MachineID, wanted),
	})
}

func (b *builder) createService(entry Service, wanted directory.ServiceDefinition, projects []string, holders keyHolders) {
	path := serviceRef(entry.Machine, entry.Name)
	holders.claimAll(serviceKeys(wanted.MachineID, wanted), keyHolder{label: path, line: entry.Line, fromFile: true})
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, path)
		return
	}
	fields := afterOnly(diffService(directory.ServiceDefinition{}, wanted, b.names).Fields)
	change := Change{Action: ActionCreate, Kind: KindService, Name: path, Fields: fields}
	b.serviceCreates = append(b.serviceCreates, keyedStep{
		step: step{
			change:  change,
			service: serviceStep{MachineID: wanted.MachineID, Wanted: wanted, Projects: projects},
		},
		label: path, line: entry.Line, wants: serviceKeys(wanted.MachineID, wanted),
	})
}

// uniqueNotes ne dit chaque note qu'une fois : trois unités ajoutées ne font
// qu'un « unité ajoutée ».
func uniqueNotes(notes []Note) []Note {
	var unique []Note
	for _, note := range notes {
		if !slices.Contains(unique, note) {
			unique = append(unique, note)
		}
	}
	return unique
}

// keepOrPruneServices traite les services que le fichier ne nomme pas.
func (b *builder) keepOrPruneServices(matched matching, holders keyHolders) {
	for index, service := range b.current.Services {
		if matched.claimed[index] {
			continue
		}
		ref := serviceRef(service.MachineName, service.Name)
		keys := serviceKeys(service.MachineID, service.Definition())
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindService, ID: service.ID, Name: ref})
			holders.claimAll(keys, keyHolder{label: ref})
			continue
		}
		for _, key := range keys {
			b.prunedServiceKeys[key] = true
		}
		change := Change{
			Action: ActionDelete, Kind: KindService, ID: service.ID, Name: ref,
			Carries: countedCarries(
				Carry{What: CarryUnits, Count: len(service.Units)},
				Carry{What: CarryProjectLinks, Count: len(service.ProjectIDs)}),
		}
		b.serviceDeletes = append(b.serviceDeletes, step{change: change, service: serviceStep{Before: service.Definition().Complete()}})
	}
}

// --- Les sondes ---

func (b *builder) planProbes() {
	entries := make([]entryRef, 0, len(b.file.Probes))
	for _, entry := range b.file.Probes {
		entries = append(entries, entryRef{id: entry.ID, name: strings.TrimSpace(entry.Name), line: entry.Line})
	}
	objects := make([]objectRef, 0, len(b.current.Probes))
	for _, item := range b.current.Probes {
		objects = append(objects, objectRef{id: item.ID, name: item.Name})
	}
	matched := b.match(entries, objects, false)

	perMachine := map[string]int{}
	lastLine := map[string]int{}
	for index, entry := range b.file.Probes {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		runnerID, ok := b.planProbe(entry, found)
		if ok {
			perMachine[runnerID]++
			lastLine[runnerID] = entry.Line
		}
	}
	for index, item := range b.current.Probes {
		if matched.claimed[index] {
			continue
		}
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindProbe, ID: item.ID, Name: item.Name})
			perMachine[item.MachineID]++
			continue
		}
		change := Change{Action: ActionDelete, Kind: KindProbe, ID: item.ID, Name: item.Name}
		b.probeDeletes = append(b.probeDeletes, step{change: change, probe: probeStep{Before: item.Definition().Complete()}})
	}
	b.checkProbeCeiling(perMachine, lastLine)
}

// checkProbeCeiling refuse un plan qui laisserait une machine avec plus de
// sondes que le domaine n'en admet : mieux vaut le dire au plan qu'au
// milieu de l'application.
func (b *builder) checkProbeCeiling(perMachine, lastLine map[string]int) {
	for machineID, count := range perMachine {
		if count > probe.MaxProbesPerMachine {
			b.refuseError(probe.ErrTooMany, lastLine[machineID], b.names.machine(machineID))
		}
	}
}

// planProbe juge une entrée de sonde ; elle rend la machine qui sonde, pour le
// plafond, et faux quand l'entrée est refusée.
func (b *builder) planProbe(entry Probe, found int) (runnerID string, ok bool) {
	wanted, serviceRef, ok := b.wantedProbe(entry)
	if !ok {
		return "", false
	}
	runnerID = wanted.MachineID
	if found == createEntry {
		if err := wanted.Validate(); err != nil {
			b.refuseError(err, entry.Line, entry.Name)
			return runnerID, false
		}
		fields := afterOnly(diffProbe(probe.Definition{}, wanted, b.names))
		change := Change{Action: ActionCreate, Kind: KindProbe, Name: wanted.Name, Fields: fields}
		b.probeCreates = append(b.probeCreates, step{change: change, probe: probeStep{Wanted: wanted, ServiceRef: serviceRef}})
		return runnerID, true
	}
	existing := b.current.Probes[found]
	fields := diffProbe(existing.Definition().Complete(), wanted, b.names)
	if len(fields) == 0 {
		b.unchanged++
		return runnerID, true
	}
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, entry.Name)
		return runnerID, false
	}
	change := Change{Action: ActionUpdate, Kind: KindProbe, ID: existing.ID, Name: wanted.Name, Fields: fields}
	if wanted.ResetsState(existing) {
		change.Notes = []Note{NoteProbeResets}
	}
	if probe.TargetPassword(existing.Target) != probe.TargetPassword(wanted.Target) {
		change.Notes = append(change.Notes, NotePasswordChanged)
	}
	b.probeUpdates = append(b.probeUpdates, step{change: change, probe: probeStep{
		Wanted: wanted, ServiceRef: serviceRef, Before: existing.Definition().Complete(),
	}})
	return runnerID, true
}

// wantedProbe traduit l'entrée en définition du domaine : la machine qui
// sonde par son identifiant, le service par celui de l'état visé, réel ou
// en attente.
func (b *builder) wantedProbe(entry Probe) (definition probe.Definition, ref string, ok bool) {
	ok = true
	runner := entry.Runner
	if runner == "" {
		runner = b.current.Machines[b.current.LocalMachineID]
	}
	runnerID, known := b.machineID(runner, entry.Line, entry.Name)
	if !known {
		ok = false
	}
	serviceID := ""
	if entry.Service != "" {
		machine, name, found := strings.Cut(entry.Service, "/")
		if !found || machine == "" || name == "" || strings.Contains(name, "/") {
			b.refuse(KeyReferenceBroken, entry.Line, entry.Name, entry.Service)
			ok = false
		} else if id, exists := b.serviceTargets[entry.Service]; exists {
			serviceID, ref = id, entry.Service
		} else {
			b.refuse(KeyServiceUnknown, entry.Line, entry.Name, entry.Service)
			ok = false
		}
	}
	definition = probe.Definition{
		Name: entry.Name, Kind: entry.Kind, Target: entry.Target, MachineID: runnerID, ServiceID: serviceID,
		Interval: entry.Interval, Timeout: entry.Timeout,
		FailureThreshold: entry.FailureThreshold, RecoveryThreshold: entry.RecoveryThreshold,
		Method: entry.Method, ExpectedStatus: entry.ExpectedStatus, ExpectedBody: entry.ExpectedBody,
		FollowRedirects: entry.FollowRedirects, TLS: entry.TLS,
	}
	return definition.Complete(), ref, ok
}

// --- Les tâches ---

func (b *builder) planHeartbeats() {
	entries := make([]entryRef, 0, len(b.file.Heartbeats))
	for _, entry := range b.file.Heartbeats {
		entries = append(entries, entryRef{id: entry.ID, name: strings.TrimSpace(entry.Name), line: entry.Line})
	}
	objects := make([]objectRef, 0, len(b.current.Heartbeats))
	for _, item := range b.current.Heartbeats {
		objects = append(objects, objectRef{id: item.ID, name: item.Name})
	}
	matched := b.match(entries, objects, false)

	for index, entry := range b.file.Heartbeats {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		b.planHeartbeat(entry, found)
	}
	for index, item := range b.current.Heartbeats {
		if matched.claimed[index] {
			continue
		}
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindHeartbeat, ID: item.ID, Name: item.Name})
			continue
		}
		change := Change{Action: ActionDelete, Kind: KindHeartbeat, ID: item.ID, Name: item.Name}
		b.heartbeatDeletes = append(b.heartbeatDeletes, step{change: change, heartbeat: heartbeatStep{Before: item.Definition()}})
	}
}

func (b *builder) planHeartbeat(entry Heartbeat, found int) {
	machineID := ""
	if entry.Machine != "" {
		var known bool
		if machineID, known = b.machineID(entry.Machine, entry.Line, entry.Name); !known {
			return
		}
	}
	wanted := heartbeat.Definition{Name: strings.TrimSpace(entry.Name), MachineID: machineID, Interval: entry.Every, Grace: entry.Grace}
	if found == createEntry {
		if err := wanted.Validate(); err != nil {
			b.refuseError(err, entry.Line, entry.Name)
			return
		}
		fields := afterOnly(diffHeartbeat(heartbeat.Definition{}, wanted, b.names))
		// Le jeton n'est jamais dans le fichier : le serveur le tire.
		change := Change{Action: ActionCreate, Kind: KindHeartbeat, Name: wanted.Name, Fields: fields, Notes: []Note{NoteTokenGenerated}}
		b.heartbeatCreates = append(b.heartbeatCreates, step{change: change, heartbeat: heartbeatStep{Wanted: wanted}})
		return
	}
	existing := b.current.Heartbeats[found]
	before := existing.Definition()
	fields := diffHeartbeat(before, wanted, b.names)
	if len(fields) == 0 {
		b.unchanged++
		return
	}
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, entry.Name)
		return
	}
	change := Change{Action: ActionUpdate, Kind: KindHeartbeat, ID: existing.ID, Name: wanted.Name, Fields: fields}
	rhythmChanged := before.Interval != wanted.Interval || before.Grace != wanted.Grace
	if rhythmChanged && !existing.IsPaused() {
		change.Notes = []Note{NoteHeartbeatDeadlineMoves}
	}
	b.heartbeatUpdates = append(b.heartbeatUpdates, step{change: change, heartbeat: heartbeatStep{Wanted: wanted, Before: before}})
}

// --- Les clés ignorées ---

func ignoredName(machine string, kind directory.IgnoreKind, value string) string {
	return machine + "/" + string(kind) + ":" + value
}

func (b *builder) planIgnored() {
	present := make(map[directory.IgnoredKey]bool, len(b.current.Ignored))
	for _, key := range b.current.Ignored {
		present[ignoredIdentity(key)] = true
	}
	wantedKeys := map[directory.IgnoredKey]bool{}
	for _, entry := range b.file.Ignored {
		name := ignoredName(entry.Machine, entry.Kind, entry.Value)
		machineID, known := b.machineID(entry.Machine, entry.Line, name)
		if !known {
			continue
		}
		key := directory.IgnoredKey{MachineID: machineID, Kind: entry.Kind, Value: entry.Value}.Complete()
		identity := ignoredIdentity(key)
		if wantedKeys[identity] {
			b.refuse(KeyNameDuplicate, entry.Line, name, name)
			continue
		}
		wantedKeys[identity] = true
		if present[identity] {
			b.unchanged++
			continue
		}
		if err := key.Validate(); err != nil {
			b.refuseError(err, entry.Line, name)
			continue
		}
		change := Change{Action: ActionCreate, Kind: KindIgnored, Name: name}
		b.ignoredCreates = append(b.ignoredCreates, step{change: change, ignored: ignoredStep{Key: key}})
	}
	for _, key := range b.current.Ignored {
		if wantedKeys[ignoredIdentity(key)] {
			continue
		}
		name := ignoredName(b.names.machine(key.MachineID), key.Kind, key.Value)
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindIgnored, Name: name})
			continue
		}
		change := Change{Action: ActionDelete, Kind: KindIgnored, Name: name}
		b.ignoredDeletes = append(b.ignoredDeletes, step{change: change, ignored: ignoredStep{Key: key}})
	}
}

// ignoredIdentity est la clé ignorée sans sa date : la seule chose qui la
// distingue.
func ignoredIdentity(key directory.IgnoredKey) directory.IgnoredKey {
	return directory.IgnoredKey{MachineID: key.MachineID, Kind: key.Kind, Value: key.Value}
}

// --- L'assemblage ---

// finish range les changements dans l'ordre d'écriture, qui est celui de
// l'affichage, et scelle le plan.
func (b *builder) finish() Plan {
	plan := Plan{Prune: b.prune, Unchanged: b.unchanged}
	slices.SortStableFunc(b.problems, func(a, c Problem) int {
		return cmp.Or(cmp.Compare(boolRank(a.Line == 0), boolRank(c.Line == 0)), cmp.Compare(a.Line, c.Line))
	})
	plan.Problems = b.problems
	if plan.Problems == nil {
		plan.Problems = []Problem{}
	}
	ordered := [][]step{
		b.projectSteps, b.serviceSteps,
		sortedByName(b.probeDeletes), b.probeUpdates, b.probeCreates,
		sortedByName(b.heartbeatDeletes), b.heartbeatUpdates, b.heartbeatCreates,
		b.ignoredCreates, sortedByName(b.ignoredDeletes),
		sortedByName(b.serviceDeletes), sortedByName(b.projectDeletes),
	}
	for _, steps := range ordered {
		plan.steps = append(plan.steps, steps...)
	}
	plan.Changes = make([]Change, 0, len(plan.steps))
	for _, step := range plan.steps {
		plan.Changes = append(plan.Changes, step.change)
	}
	slices.SortFunc(b.unmanaged, func(a, c Ref) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(c.Kind)), cmp.Compare(a.Name, c.Name), cmp.Compare(a.ID, c.ID))
	})
	plan.Unmanaged = b.unmanaged
	if plan.Unmanaged == nil {
		plan.Unmanaged = []Ref{}
	}
	plan.projects, plan.services = b.projectTargets, b.serviceTargets
	plan.Hash = hashChanges(plan.Prune, plan.Changes)
	return plan
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}

func kindRank(kind Kind) int {
	return slices.Index(Kinds(), kind)
}

// sortedByName range des suppressions par nom puis identifiant : l'ordre des
// lignes de la base ne doit pas changer l'empreinte.
func sortedByName(steps []step) []step {
	sorted := slices.Clone(steps)
	slices.SortStableFunc(sorted, func(a, c step) int {
		return cmp.Or(cmp.Compare(a.change.Name, c.change.Name), cmp.Compare(a.change.ID, c.change.ID))
	})
	return sorted
}

// String décrit un changement pour un journal technique, sans valeur.
func (c Change) String() string {
	return fmt.Sprintf("%s %s", c.Action, c.Label())
}
