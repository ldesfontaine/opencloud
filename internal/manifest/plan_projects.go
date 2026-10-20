package manifest

import (
	"github.com/ldesfontaine/opencloud/internal/directory"
)

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
