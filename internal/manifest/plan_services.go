package manifest

import (
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

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
