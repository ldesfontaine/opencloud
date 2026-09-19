package service

import (
	"sort"
	"strconv"
)

// InternetNode est l'identifiant du nœud « Internet » dans la topologie :
// tout port publié sur toutes les interfaces y est relié.
const InternetNode = "internet"

type EdgeKind string

const (
	// Une route publique : d'Internet au service, par un port publié.
	EdgePublic EdgeKind = "public"
	// Une dépendance déclarée : du service vers celui dont il dépend.
	EdgeDepends EdgeKind = "depends"
)

// Topology est ce que l'onglet Réseau dessine : le serveur rend les faits,
// le front place et trace. Les nœuds sont Internet et chaque service ;
// les groupes sont les réseaux créés par l'opérateur ou Compose, chacun
// avec les services qu'il abrite ; un service dans plusieurs réseaux va
// dans celui où il a le plus de voisins.
type Topology struct {
	Services []Service
	Groups   []Group
	Edges    []Edge
}

type Group struct {
	NetworkID string
	Name      string
	Internal  bool
	Members   []string
}

type Edge struct {
	From     string
	To       string
	Kind     EdgeKind
	Port     int
	Protocol string
	Source   DependencySource
}

// BuildTopology calcule la topologie d'une machine à partir de ses services
// vivants et de ses réseaux. Un réseau que la machine n'a pas listé mais
// qu'un service dit joindre compte quand même : l'appartenance fait foi.
func BuildTopology(services []Service, networks []Network) Topology {
	topology := Topology{Services: services, Groups: []Group{}, Edges: []Edge{}}
	groups := groupsOf(services, networks)
	placed := placeServices(services, groups)
	for _, group := range groups {
		group.Members = placed[group.NetworkID]
		if len(group.Members) > 0 {
			topology.Groups = append(topology.Groups, group)
		}
	}
	for _, item := range services {
		topology.Edges = append(topology.Edges, publicEdges(item)...)
	}
	for _, item := range services {
		topology.Edges = append(topology.Edges, dependencyEdges(item, services)...)
	}
	return topology
}

// groupsOf rend, par nom, les réseaux qui font un groupe : ceux de la liste
// de la machine, complétés par ceux que les services joignent.
func groupsOf(services []Service, networks []Network) []Group {
	byID := map[string]Group{}
	for _, network := range networks {
		if network.IsUserDefined() {
			byID[network.NetworkID] = Group{NetworkID: network.NetworkID, Name: network.Name, Internal: network.Internal}
		}
	}
	listed := map[string]bool{}
	for _, network := range networks {
		listed[network.NetworkID] = true
	}
	for _, item := range services {
		for _, attachment := range item.Networks {
			if listed[attachment.NetworkID] {
				continue
			}
			candidate := Network{NetworkID: attachment.NetworkID, Name: attachment.Name, Driver: "bridge"}
			if candidate.IsUserDefined() {
				byID[attachment.NetworkID] = Group{NetworkID: attachment.NetworkID, Name: attachment.Name}
			}
		}
	}
	groups := make([]Group, 0, len(byID))
	for _, group := range byID {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups
}

// placeServices choisit le groupe de chaque service : le réseau où il a le
// plus de voisins, le premier par le nom à égalité. Rend les membres par
// réseau, dans l'ordre des services.
func placeServices(services []Service, groups []Group) map[string][]string {
	population := map[string]int{}
	names := map[string]string{}
	for _, group := range groups {
		names[group.NetworkID] = group.Name
	}
	for _, item := range services {
		for _, attachment := range item.Networks {
			if _, isGroup := names[attachment.NetworkID]; isGroup {
				population[attachment.NetworkID]++
			}
		}
	}
	placed := map[string][]string{}
	for _, item := range services {
		chosen := ""
		for _, attachment := range item.Networks {
			if _, isGroup := names[attachment.NetworkID]; !isGroup {
				continue
			}
			if chosen == "" || population[attachment.NetworkID] > population[chosen] ||
				(population[attachment.NetworkID] == population[chosen] && names[attachment.NetworkID] < names[chosen]) {
				chosen = attachment.NetworkID
			}
		}
		if chosen != "" {
			placed[chosen] = append(placed[chosen], item.ID)
		}
	}
	return placed
}

// publicEdges relie Internet à chaque port publié sur toutes les
// interfaces ; 0.0.0.0 et :: sur le même port ne font qu'une arête. Un
// conteneur en mode host n'a pas de port publié : Docker n'en dit rien.
func publicEdges(item Service) []Edge {
	edges := []Edge{}
	seen := map[string]bool{}
	for _, port := range item.Ports {
		key := port.Protocol + ":" + strconv.Itoa(port.HostPort)
		if !port.IsPublic() || seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, Edge{From: InternetNode, To: item.ID, Kind: EdgePublic, Port: port.HostPort, Protocol: port.Protocol})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Port != edges[j].Port {
			return edges[i].Port < edges[j].Port
		}
		return edges[i].Protocol < edges[j].Protocol
	})
	return edges
}

// dependencyEdges résout chaque dépendance déclarée : une dépendance
// Compose vise un service du même projet, un lien vise un conteneur par
// son nom. Ce qui ne se résout pas n'a pas d'arête : la fiche le dit.
func dependencyEdges(item Service, services []Service) []Edge {
	edges := []Edge{}
	for _, dependency := range item.DependsOn {
		target, found := resolveDependency(item, dependency, services)
		if !found || target.ID == item.ID {
			continue
		}
		edges = append(edges, Edge{From: item.ID, To: target.ID, Kind: EdgeDepends, Source: dependency.Source})
	}
	return edges
}

func resolveDependency(item Service, dependency Dependency, services []Service) (Service, bool) {
	for _, candidate := range services {
		if candidate.Name != dependency.Name {
			continue
		}
		if dependency.Source == DependencyCompose && candidate.Group != item.Group {
			continue
		}
		return candidate, true
	}
	return Service{}, false
}
