package dockerwatch

import (
	"context"
	"errors"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// deliverInventory livre les conteneurs et les réseaux de la machine, les
// deux listes complètes.
func (w *Watcher) deliverInventory(ctx context.Context) {
	inventory, mounts, err := w.inventory(ctx)
	if err != nil {
		w.logger.Warn("docker inventory", "error", err)
		return
	}
	networks, err := w.networks(ctx)
	if err != nil {
		w.logger.Warn("docker networks", "error", err)
		return
	}
	// Retenu avant d'être livré : le serveur ne peut pas demander un journal
	// d'un conteneur que l'agent n'aurait pas encore noté.
	w.rememberAll(observedIDs(inventory))
	report := container.Report{Complete: true, Inventory: inventory, NetworksComplete: true, Networks: networks}
	// Un démon qui ne liste pas les volumes ne fait pas perdre l'inventaire :
	// le serveur garde alors la liste qu'il avait.
	if volumes, err := w.volumes(ctx, mounts); err != nil {
		w.logger.Warn("docker volumes", "error", err)
	} else {
		report.VolumesComplete, report.Volumes = true, volumes
	}
	w.sink.Deliver(report)
}

// volumes liste les volumes nommés de la machine, avec les conteneurs qui les
// montent ; mounts donne, par volume, les noms de ceux que l'inventaire a vus.
func (w *Watcher) volumes(ctx context.Context, mounts map[string][]string) ([]container.VolumeReport, error) {
	list, err := w.client.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	if len(list) > container.MaxVolumesPerMachine {
		list = list[:container.MaxVolumesPerMachine]
	}
	volumes := make([]container.VolumeReport, 0, len(list))
	for _, volume := range list {
		users := mounts[volume.Name]
		if len(users) > container.MaxVolumeUsers {
			users = users[:container.MaxVolumeUsers]
		}
		volumes = append(volumes, container.VolumeReport{Name: volume.Name, Driver: volume.Driver, UsedBy: users})
	}
	return volumes, nil
}

func observedIDs(inventory []container.Observed) []string {
	ids := make([]string, 0, len(inventory))
	for _, observed := range inventory {
		ids = append(ids, observed.ContainerID)
	}
	return ids
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
// laissé de côté, un conteneur disparu entre les deux aussi. Il rend aussi, par
// nom de volume, les conteneurs qui le montent.
func (w *Watcher) inventory(ctx context.Context) ([]container.Observed, map[string][]string, error) {
	list, err := w.client.ListContainers(ctx)
	if err != nil {
		return nil, nil, err
	}
	inventory := make([]container.Observed, 0, len(list))
	environments := make([]dockerapi.EnvHosts, 0, len(list))
	mounts := map[string][]string{}
	for _, summary := range list {
		if summary.IsOneOff() {
			continue
		}
		inspected, err := w.client.Inspect(ctx, summary.ID)
		if errors.Is(err, dockerapi.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		observed := toContainer(inspected)
		inventory = append(inventory, observed)
		environments = append(environments, w.readEnvironment(ctx, inspected.ID))
		for _, name := range mountedVolumes(inspected) {
			mounts[name] = append(mounts[name], observed.Name)
		}
	}
	w.resolveEnvironments(inventory, environments)
	return inventory, mounts, nil
}

// readEnvironment lit l'environnement d'un conteneur, et seulement avec le
// droit observe.env : sans lui, l'appel n'est même pas fait. Un échec se dit
// au journal sans rien de l'environnement, et laisse le conteneur sans lien.
func (w *Watcher) readEnvironment(ctx context.Context, id string) dockerapi.EnvHosts {
	if !w.opts.ReadEnvironment {
		return dockerapi.EnvHosts{}
	}
	hosts, err := w.client.InspectEnvironment(ctx, id)
	if err != nil && !errors.Is(err, dockerapi.ErrNotFound) {
		w.logger.Warn("docker environment", "container", id, "error", err)
	}
	return hosts
}

// resolveEnvironments pose sur chaque conteneur les instances que son
// environnement désigne. Les hôtes candidats sont oubliés ici : seules les
// cibles trouvées dans l'inventaire sortent.
func (w *Watcher) resolveEnvironments(inventory []container.Observed, environments []dockerapi.EnvHosts) {
	if !w.opts.ReadEnvironment {
		return
	}
	for index := range inventory {
		inventory[index].EnvTargets = environmentTargets(inventory[index], environments[index], inventory)
	}
}

// mountedVolumes rend les noms des volumes nommés qu'un conteneur monte, une
// fois chacun ; un dossier de l'hôte n'en est pas un.
func mountedVolumes(inspected dockerapi.Container) []string {
	var names []string
	for _, mount := range inspected.Mounts {
		if mount.Type == "volume" && mount.Name != "" && !slices.Contains(names, mount.Name) {
			names = append(names, mount.Name)
		}
	}
	return names
}
