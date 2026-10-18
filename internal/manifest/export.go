package manifest

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Export écrit tout l'annuaire dans le format que la lecture comprend :
// projets, services déclarés et leurs unités, sondes, tâches, clés
// ignorées. Les identifiants y sont toujours, jamais un secret.
func (m *Manifest) Export(ctx context.Context, annotations Annotations) ([]byte, error) {
	file, err := m.Read(ctx)
	if err != nil {
		return nil, err
	}
	data, err := Encode(file, annotations)
	if err != nil {
		return nil, err
	}
	// Le fichier doit pouvoir être relu : au-delà de la borne de la lecture,
	// l'export échoue plutôt que d'écrire un fichier que Parse refusera.
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrExportTooLarge, len(data), MaxFileBytes)
	}
	m.logger.Debug("directory exported", "bytes", len(data), "services", len(file.Services), "probes", len(file.Probes), "heartbeats", len(file.Heartbeats))
	return data, nil
}

// Read lit l'annuaire courant dans le vocabulaire du fichier, trié : deux
// lectures de la même base rendent la même chose, dans le même ordre.
func (m *Manifest) Read(ctx context.Context) (File, error) {
	names, err := m.deps.Machines.MachineNames(ctx)
	if err != nil {
		return File{}, fmt.Errorf("read machine names: %w", err)
	}
	projects, err := m.deps.Directory.ListProjects(ctx)
	if err != nil {
		return File{}, fmt.Errorf("read projects: %w", err)
	}
	services, err := m.deps.Directory.ListServices(ctx, directory.Filter{})
	if err != nil {
		return File{}, fmt.Errorf("read services: %w", err)
	}
	probes, err := m.deps.Probes.List(ctx, "")
	if err != nil {
		return File{}, fmt.Errorf("read probes: %w", err)
	}
	heartbeats, err := m.deps.Heartbeats.List(ctx)
	if err != nil {
		return File{}, fmt.Errorf("read heartbeats: %w", err)
	}
	ignored, err := m.deps.Directory.IgnoredKeys(ctx)
	if err != nil {
		return File{}, fmt.Errorf("read ignored keys: %w", err)
	}
	return File{
		Machines:   machinesOf(names),
		Projects:   projectsOf(projects),
		Services:   servicesOf(services, projects),
		Probes:     probesOf(probes),
		Heartbeats: heartbeatsOf(heartbeats),
		Ignored:    ignoredOf(ignored, names),
	}, nil
}

func machinesOf(names map[string]string) []Machine {
	machines := make([]Machine, 0, len(names))
	for _, name := range names {
		machines = append(machines, Machine{Name: name})
	}
	slices.SortFunc(machines, func(a, b Machine) int { return cmp.Compare(a.Name, b.Name) })
	return machines
}

func projectsOf(projects []directory.Project) []Project {
	listed := make([]Project, 0, len(projects))
	for _, item := range projects {
		listed = append(listed, Project{ID: item.ID, Name: item.Name, Title: item.Title})
	}
	slices.SortFunc(listed, func(a, b Project) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return listed
}

func servicesOf(services []directory.Service, projects []directory.Project) []Service {
	projectNames := make(map[string]string, len(projects))
	for _, item := range projects {
		projectNames[item.ID] = item.Name
	}
	listed := make([]Service, 0, len(services))
	for _, item := range services {
		listed = append(listed, serviceOf(item, projectNames))
	}
	slices.SortFunc(listed, func(a, b Service) int {
		return cmp.Or(cmp.Compare(a.Machine, b.Machine), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return listed
}

func serviceOf(item directory.Service, projectNames map[string]string) Service {
	service := Service{
		ID:             item.ID,
		Machine:        item.MachineName,
		Name:           item.Name,
		Title:          item.Title,
		Nature:         item.Nature,
		Origin:         item.Origin,
		ComposeProject: item.ComposeProject,
		PublicURL:      item.PublicURL,
		NeedsReview:    item.NeedsReview,
	}
	// Dans l'ordre de Capabilities() : celui des fournitures dans l'interface.
	for _, capability := range directory.Capabilities() {
		if slices.Contains(item.Provides, capability) {
			service.Provides = append(service.Provides, capability)
		}
	}
	for _, id := range item.ProjectIDs {
		// Un projet supprimé entre les deux lectures n'a plus de nom à écrire.
		if name, found := projectNames[id]; found {
			service.Projects = append(service.Projects, name)
		}
	}
	slices.Sort(service.Projects)
	// Dans l'ordre de position : celui de l'annuaire.
	for _, unit := range item.Units {
		service.Units = append(service.Units, unitOf(unit))
	}
	return service
}

func unitOf(item directory.Unit) Unit {
	unit := Unit{
		Name:          item.Name,
		Image:         item.Image,
		Version:       item.Version,
		Digest:        item.Digest,
		ContainerName: item.ContainerName,
		SystemdUnit:   item.SystemdUnit,
		Ports:         item.Ports,
	}
	// Un binaire n'a que « watch » : il ne l'écrit pas.
	if item.Kind == directory.UnitContainer {
		unit.Updates = item.UpdatePolicy
	}
	return unit
}

func probesOf(probes []probe.Probe) []Probe {
	listed := make([]Probe, 0, len(probes))
	for _, item := range probes {
		listed = append(listed, probeOf(item))
	}
	slices.SortFunc(listed, func(a, b Probe) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return listed
}

func probeOf(item probe.Probe) Probe {
	listed := Probe{
		ID:                item.ID,
		Name:              item.Name,
		Runner:            item.MachineName,
		Kind:              item.Kind,
		Target:            item.Target,
		Interval:          item.Interval,
		Timeout:           item.Timeout,
		FailureThreshold:  item.FailureThreshold,
		RecoveryThreshold: item.RecoveryThreshold,
		Method:            item.Method,
		ExpectedStatus:    item.ExpectedStatus,
		ExpectedBody:      item.ExpectedBody,
		FollowRedirects:   item.FollowRedirects,
		TLS:               item.TLS,
	}
	if item.ServiceID != "" {
		listed.Service = item.ServiceMachineName + "/" + item.ServiceName
	}
	return listed
}

// heartbeatsOf ne copie jamais le jeton de la tâche : seul le marqueur
// s'écrit, et l'objet du domaine n'a pas d'autre champ à lire.
func heartbeatsOf(heartbeats []heartbeat.Heartbeat) []Heartbeat {
	listed := make([]Heartbeat, 0, len(heartbeats))
	for _, item := range heartbeats {
		listed = append(listed, Heartbeat{ID: item.ID, Name: item.Name, Machine: item.MachineName, Every: item.Interval, Grace: item.Grace})
	}
	slices.SortFunc(listed, func(a, b Heartbeat) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return listed
}

func ignoredOf(keys []directory.IgnoredKey, names map[string]string) []IgnoredKey {
	listed := make([]IgnoredKey, 0, len(keys))
	for _, item := range keys {
		// Une machine retirée entre les deux lectures n'a plus de nom à écrire.
		if name, found := names[item.MachineID]; found {
			listed = append(listed, IgnoredKey{Machine: name, Kind: item.Kind, Value: item.Value})
		}
	}
	slices.SortFunc(listed, func(a, b IgnoredKey) int {
		return cmp.Or(cmp.Compare(a.Machine, b.Machine), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Value, b.Value))
	})
	return listed
}
