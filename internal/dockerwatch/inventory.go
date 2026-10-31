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
		for _, name := range mountedVolumes(inspected) {
			mounts[name] = append(mounts[name], observed.Name)
		}
	}
	return inventory, mounts, nil
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
