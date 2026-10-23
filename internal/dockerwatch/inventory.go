package dockerwatch

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// deliverInventory livre les conteneurs et les réseaux de la machine, les
// deux listes complètes.
func (w *Watcher) deliverInventory(ctx context.Context) {
	inventory, err := w.inventory(ctx)
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
	w.sink.Deliver(container.Report{Complete: true, Inventory: inventory, NetworksComplete: true, Networks: networks})
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
// laissé de côté, un conteneur disparu entre les deux aussi.
func (w *Watcher) inventory(ctx context.Context) ([]container.Observed, error) {
	list, err := w.client.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	inventory := make([]container.Observed, 0, len(list))
	for _, summary := range list {
		if summary.IsOneOff() {
			continue
		}
		observed, err := w.inspect(ctx, summary.ID)
		if errors.Is(err, dockerapi.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inventory = append(inventory, observed)
	}
	return inventory, nil
}

func (w *Watcher) inspect(ctx context.Context, id string) (container.Observed, error) {
	inspected, err := w.client.Inspect(ctx, id)
	if err != nil {
		return container.Observed{}, err
	}
	return toContainer(inspected), nil
}
