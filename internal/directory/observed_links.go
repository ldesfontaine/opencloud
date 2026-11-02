package directory

import (
	"context"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/link"
)

// Observer dit ce que les agents ont vu joindre les ports de leur hôte
// (hostconn.Service). nil est toléré.
type Observer interface {
	Seen(machineID string) []link.Sighting
}

// SetObserver branche les connexions vues : sans lui, aucun lien n'est observé.
func (d *Directory) SetObserver(observer Observer) {
	d.observer = observer
}

// observedLinks rapproche ce que la machine a vu joindre ses ports des
// instances du service qui les publient. Un lien observé a pour source un
// compte, une adresse ou Internet, jamais un service : la table de l'hôte
// n'attribue pas de processus (étude services-modèle §2.3, signal 9). Il n'y a
// pas de connexion entre conteneurs, qu'un conteneur ne montre pas à l'hôte.
func (d *Directory) observedLinks(ctx context.Context, owner Service) ([]link.Link, error) {
	if d.observer == nil {
		return nil, nil
	}
	sightings := d.observer.Seen(owner.MachineID)
	if len(sightings) == 0 {
		return nil, nil
	}
	byPort, err := d.hostPorts(ctx, owner)
	if err != nil {
		return nil, err
	}
	var observed []link.Link
	for _, sighting := range sightings {
		key, found := byPort[sighting.Port]
		if !found {
			continue
		}
		from, to := link.Endpoint{MachineID: owner.MachineID, Key: sighting.Source}, link.Endpoint{MachineID: owner.MachineID, Key: key}
		observed = append(observed, link.Link{
			ID: link.ObservedID(from, to, sighting.Port), From: from, To: to,
			Nature: link.NatureTraffic, Proof: link.ProofObserved, Signal: link.SignalHostConnection,
			Port: sighting.Port, FirstSeen: sighting.FirstSeen, LastSeen: sighting.LastSeen,
		})
	}
	return observed, nil
}

// hostPorts rend les ports TCP de l'hôte que les unités du service publient,
// avec la clé de l'unité : ceux que ses conteneurs publient aujourd'hui, et
// ceux que l'opérateur a déclarés (le port d'un binaire, par exemple). Si deux
// unités en réclament un, la première le garde.
func (d *Directory) hostPorts(ctx context.Context, owner Service) (map[int]string, error) {
	ports := map[int]string{}
	var living []container.Container
	if d.containers != nil && owner.Origin != OriginBinary {
		var err error
		if living, err = d.containers.List(ctx, owner.MachineID); err != nil {
			return nil, err
		}
	}
	for _, unit := range owner.Units {
		key := UnitKey(owner, unit)
		if key == "" {
			continue
		}
		for _, declared := range unit.Ports {
			if port, found := declaredHostPort(declared, owner.Origin); found {
				setIfFree(ports, port, key)
			}
		}
		for _, item := range living {
			if container.InstanceKey(item) != key || item.IsArchived() {
				continue
			}
			for _, published := range item.Ports {
				if published.HostPort > 0 && published.Protocol != "udp" {
					setIfFree(ports, published.HostPort, key)
				}
			}
		}
	}
	return ports, nil
}

func setIfFree(ports map[int]string, port int, key string) {
	if _, taken := ports[port]; !taken {
		ports[port] = key
	}
}

// declaredHostPort lit un port déclaré (« 8080:80/tcp », « 443 ») et rend celui
// de l'hôte. Dans un conteneur, un port seul est celui du conteneur : il n'est
// pas publié, l'hôte n'y voit rien. Pour un binaire, le port seul est celui
// qu'il écoute sur l'hôte. Un port UDP n'est pas dans la table lue.
func declaredHostPort(declared string, origin Origin) (int, bool) {
	text, protocol, _ := strings.Cut(declared, "/")
	if protocol == "udp" {
		return 0, false
	}
	published, _, mapped := strings.Cut(text, ":")
	if !mapped {
		if origin != OriginBinary {
			return 0, false
		}
		published = text
	}
	port, err := strconv.Atoi(published)
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}
