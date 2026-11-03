package container

import (
	"context"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/link"
)

// Links est ce que les conteneurs échangent avec le composant des liens : ils
// lui disent les liens que leur configuration déclare (l'ordre de démarrage,
// l'environnement), et lisent ceux qui sont stockés pour dessiner le réseau.
// nil est toléré.
type Links interface {
	RecordSeen(ctx context.Context, machineID string, seen []link.Seen) error
	List(ctx context.Context, machineID string) ([]link.Link, error)
}

// InstanceKey est la clé stable d'un conteneur : « projet/service » pour un
// service Compose (les répliques n'en font qu'une, la recréation ne la change
// pas), le nom du conteneur sinon. Jamais l'identifiant Docker.
func InstanceKey(item Container) string {
	return instanceKey(item.Group, item.ComposeService, item.Name)
}

func instanceKey(group, composeService, name string) string {
	if group != "" && composeService != "" {
		return link.ComposeKey(group, composeService)
	}
	return link.ContainerKey(name)
}

// EnvironmentLinks lit ce que l'environnement des conteneurs d'une machine
// désigne (l'agent l'a résolu contre son inventaire, avec le droit
// observe.env). Le serveur le résout encore : une cible qui n'est pas dans
// l'inventaire du même rapport, ou qui est la source elle-même, ne donne rien.
// Une paire n'est rendue qu'une fois, avec le premier port non nul.
func EnvironmentLinks(inventory []Observed) []link.Seen {
	keys := map[[2]string]string{}
	for _, item := range inventory {
		keys[[2]string{item.Group, item.Name}] = instanceKey(item.Group, item.ComposeService, item.Name)
	}
	byPair := map[[2]string]*link.Seen{}
	var seen []*link.Seen
	for _, item := range inventory {
		from := instanceKey(item.Group, item.ComposeService, item.Name)
		for _, target := range item.EnvTargets {
			to, found := keys[[2]string{target.Group, target.Name}]
			if !found || to == from {
				continue
			}
			pair := [2]string{from, to}
			if existing := byPair[pair]; existing != nil {
				if existing.Port == 0 {
					existing.Port = target.Port
				}
				continue
			}
			created := &link.Seen{From: from, To: to, Signal: link.SignalEnvironment, Port: target.Port}
			byPair[pair] = created
			seen = append(seen, created)
		}
	}
	links := make([]link.Seen, 0, len(seen))
	for _, item := range seen {
		links = append(links, *item)
	}
	slices.SortFunc(links, func(a, b link.Seen) int {
		if byFrom := strings.Compare(a.From, b.From); byFrom != 0 {
			return byFrom
		}
		return strings.Compare(a.To, b.To)
	})
	return links
}

// OrderLinks lit les dépendances déclarées des conteneurs d'une machine : une
// dépendance Compose vise un service du même projet, un lien vise un conteneur
// par son nom. Ce qui ne se résout pas ne donne pas de lien : la fiche du
// conteneur le dit. Une paire n'est rendue qu'une fois, dans l'ordre des
// clés.
func OrderLinks(containers []Container) []link.Seen {
	seen := map[[2]string]bool{}
	var orders []link.Seen
	for _, item := range containers {
		for _, dependency := range item.DependsOn {
			target, found := resolveDependency(item, dependency, containers)
			if !found {
				continue
			}
			from, to := InstanceKey(item), InstanceKey(target)
			if from == to || seen[[2]string{from, to}] {
				continue
			}
			seen[[2]string{from, to}] = true
			orders = append(orders, link.Seen{From: from, To: to, Signal: signalOf(dependency.Source)})
		}
	}
	slices.SortFunc(orders, func(a, b link.Seen) int {
		if byFrom := strings.Compare(a.From, b.From); byFrom != 0 {
			return byFrom
		}
		return strings.Compare(a.To, b.To)
	})
	return orders
}

func signalOf(source DependencySource) link.Signal {
	if source == DependencyLink {
		return link.SignalContainerLink
	}
	return link.SignalDependsOn
}

// recordEnvironmentLinks écrit les liens que l'environnement des conteneurs
// d'un inventaire complet désigne. Un échec n'arrête pas le rapport : le
// prochain inventaire les réécrit.
func (t *Tracker) recordEnvironmentLinks(ctx context.Context, machineID string, inventory []Observed) {
	if t.links == nil {
		return
	}
	if err := t.links.RecordSeen(ctx, machineID, EnvironmentLinks(inventory)); err != nil {
		t.logger.Warn("record environment links", "machine", machineID, "error", err)
	}
}

// recordOrderLinks écrit les liens d'ordre que les conteneurs vivants de la
// machine déclarent. Un échec n'arrête pas le rapport : les liens se
// réécrivent au rapport suivant.
func (t *Tracker) recordOrderLinks(ctx context.Context, machineID string) {
	if t.links == nil {
		return
	}
	living, err := t.List(ctx, machineID)
	if err != nil {
		t.logger.Warn("list containers for links", "machine", machineID, "error", err)
		return
	}
	if err := t.links.RecordSeen(ctx, machineID, OrderLinks(living)); err != nil {
		t.logger.Warn("record order links", "machine", machineID, "error", err)
	}
}

// RecordLinksAll écrit d'un coup les liens d'ordre de chaque machine : au
// démarrage, pour les conteneurs connus avant que les liens existent, sans
// attendre l'inventaire suivant de chaque agent.
func (t *Tracker) RecordLinksAll(ctx context.Context) {
	stored, err := t.store.ListContainers(ctx, "")
	if err != nil {
		t.logger.Warn("record links", "error", err)
		return
	}
	done := map[string]bool{}
	for _, found := range stored {
		if done[found.MachineID] {
			continue
		}
		done[found.MachineID] = true
		t.recordOrderLinks(ctx, found.MachineID)
	}
}
