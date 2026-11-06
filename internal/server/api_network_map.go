package server

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/netmap"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// listNetworks répond la carte du réseau de la vue d'ensemble : l'arbre que
// internal/netmap déduit des faits. Le front place et trace ; rien ici ne dit où.
func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	input, err := s.networkMapInput(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, networkMapToJSON(netmap.Build(input)))
}

// networkMapInput rassemble les faits que la carte lit : machines, hôtes,
// pare-feu, ce qui tourne et les liens. Chaque lecture passe par le composant
// qui en répond.
func (s *Server) networkMapInput(ctx context.Context) (netmap.Input, error) {
	facts, err := s.readMachineFacts(ctx)
	if err != nil {
		return netmap.Input{}, err
	}
	statuses, err := s.machines.List(ctx)
	if err != nil {
		return netmap.Input{}, err
	}
	input := netmap.Input{}
	for _, status := range statuses {
		converted := s.machineJSONOf(status, facts)
		placed := netmap.Machine{ID: status.ID, Name: status.Name, Kind: converted.Kind, Online: status.Online, Address: status.Address}
		if converted.Host != nil {
			placed.HostID = converted.Host.MachineID
		}
		input.Machines = append(input.Machines, placed)
		if summary := facts.summaries[status.ID]; summary.Declared {
			input.Hypervisors = append(input.Hypervisors, s.hypervisorOf(status.ID, summary))
		}
	}
	if input.Firewalls, err = s.networkFirewalls(ctx); err != nil {
		return netmap.Input{}, err
	}
	declared, err := s.reader.ListDeclaredServices(ctx, directory.Filter{})
	if err != nil {
		return netmap.Input{}, err
	}
	if input.Instances, err = s.networkInstances(ctx, declared); err != nil {
		return netmap.Input{}, err
	}
	if input.Links, err = s.networkLinks(ctx, declared); err != nil {
		return netmap.Input{}, err
	}
	return input, nil
}

// hypervisorOf donne à chaque invité le tag VLAN de sa première carte
// étiquetée ; le service le tient en mémoire depuis le dernier relevé.
func (s *Server) hypervisorOf(machineID string, summary proxmox.Summary) netmap.Hypervisor {
	networks := s.proxmox.Networks(machineID)
	hypervisor := netmap.Hypervisor{MachineID: machineID, Node: summary.Node}
	for _, guest := range summary.Members {
		hypervisor.Guests = append(hypervisor.Guests, netmap.Guest{
			VMID: guest.VMID, Kind: guest.Kind, Name: guest.Name, Status: guest.Status, Template: guest.Template,
			Node: guest.Node, MachineID: guest.LinkedMachineID, VLAN: vlanOf(networks[guest.VMID]),
		})
	}
	return hypervisor
}

func (s *Server) networkFirewalls(ctx context.Context) ([]netmap.Firewall, error) {
	summaries, err := s.reader.ListFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	firewalls := make([]netmap.Firewall, 0, len(summaries))
	for _, summary := range summaries {
		view, err := s.reader.GetFirewallView(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		shown := netmap.Firewall{ID: view.ID, Name: view.Name}
		for _, iface := range view.Interfaces {
			shown.Interfaces = append(shown.Interfaces, netmap.Interface{
				Name: iface.Name, Device: iface.Device, Role: iface.Role, Address: iface.Address, Up: iface.Status == "up",
			})
		}
		firewalls = append(firewalls, shown)
	}
	return firewalls, nil
}

// networkInstances rend ce qui tourne : les conteneurs vivants, regroupés par
// instance (les répliques d'un service Compose n'en font qu'une), et les
// unités systemd que l'annuaire déclare et que la machine montre.
func (s *Server) networkInstances(ctx context.Context, declared []directory.Service) ([]netmap.Instance, error) {
	living, err := s.containers.List(ctx, "")
	if err != nil {
		return nil, err
	}
	type slot struct{ machineID, key string }
	byKey := map[slot]*netmap.Instance{}
	instances := []netmap.Instance{}
	for _, item := range living {
		if !item.ArchivedAt.IsZero() {
			continue
		}
		key := slot{item.MachineID, container.InstanceKey(item)}
		found := byKey[key]
		if found == nil {
			kind := netmap.ServiceContainer
			if link.KindOf(key.key) == link.KindCompose {
				kind = netmap.ServiceCompose
			}
			instances = append(instances, netmap.Instance{MachineID: item.MachineID, Key: key.key, Kind: kind, ContainerID: item.ID})
			found = &instances[len(instances)-1]
			byKey[key] = found
		}
		found.Replicas = append(found.Replicas, netmap.Replica{State: string(item.State), Unhealthy: item.Health == container.HealthUnhealthy})
		if found.ServiceID == "" {
			found.ServiceID = item.DeclaredServiceID
		}
		for _, port := range item.Ports {
			if port.IsPublic() && !slices.Contains(found.PublicPorts, port.HostPort) {
				found.PublicPorts = append(found.PublicPorts, port.HostPort)
			}
		}
	}
	units, err := s.directory.ObservedUnits(ctx, declared)
	if err != nil {
		return nil, err
	}
	// Un service déclaré que la machine ne montre pas reste une puce : ses liens
	// y tiennent, et l'écart se lit à sa pastille.
	for _, owner := range declared {
		for _, unit := range owner.Units {
			key := directory.UnitKey(owner, unit)
			if key == "" || byKey[slot{owner.MachineID, key}] != nil {
				continue
			}
			instances = append(instances, declaredInstance(owner, unit, key, units[unit.ID]))
		}
	}
	return instances, nil
}

// networkLinks rend les liens que la fiche d'un service montre, tous
// services confondus : écrits, déduits à confirmer et vus. Une route que
// l'opérateur a écartée n'est pas tracée.
func (s *Server) networkLinks(ctx context.Context, declared []directory.Service) ([]netmap.Link, error) {
	if s.links == nil {
		return nil, nil
	}
	stored, err := s.links.List(ctx, "")
	if err != nil {
		return nil, err
	}
	now := s.now()
	links := make([]netmap.Link, 0, len(stored))
	known := map[string]bool{}
	add := func(found link.Link) {
		if known[found.ID] {
			return
		}
		known[found.ID] = true
		links = append(links, netmap.Link{
			ID: found.ID, From: netmap.End{MachineID: found.From.MachineID, Key: found.From.Key}, To: netmap.End{MachineID: found.To.MachineID, Key: found.To.Key},
			Nature: string(found.Nature), Proof: string(found.Proof), Signal: string(found.Signal), Port: found.Port, Stale: found.Stale(now),
			Proposed: found.Proposed(), FirstSeen: timeText(found.FirstSeen), LastSeen: timeText(found.LastSeen),
		})
	}
	for _, found := range stored {
		if !found.Dismissed() {
			add(found)
		}
	}
	for _, owner := range declared {
		reached, err := s.directory.ServiceLinks(ctx, owner.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range reached {
			if !item.Dismissed {
				add(item.Link)
			}
		}
	}
	return links, nil
}

// declaredInstance : l'unité que l'annuaire déclare, avec ce que la machine en
// dit. Une unité systemd lue porte son état ; une unité absente du relevé, ou
// un conteneur qu'aucun inventaire ne montre, est « absent » ; une machine qui n'a pas dit ses unités, inconnue.
func declaredInstance(owner directory.Service, unit directory.Unit, key string, observation directory.UnitObservation) netmap.Instance {
	instance := netmap.Instance{MachineID: owner.MachineID, Key: key, Kind: netmap.ServiceContainer, ServiceID: owner.ID, Missing: true}
	switch link.KindOf(key) {
	case link.KindCompose:
		instance.Kind = netmap.ServiceCompose
	case link.KindSystemd:
		instance.Kind = netmap.ServiceSystemd
		instance.Missing = observation.Presence == directory.PresenceMissing
		if observation.Presence == directory.PresenceFound {
			instance.Active = string(observation.Unit.Active)
		}
	}
	return instance
}

func timeText(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

func vlanOf(nics []proxmox.NIC) int {
	for _, nic := range nics {
		if nic.VLAN > 0 {
			return nic.VLAN
		}
	}
	return 0
}

// La carte telle que le front la lit.
type networkMapJSON struct {
	Zones []mapZoneJSON `json:"zones"`
	// Les machines hors de la maison, à plat.
	Outside []mapBoxJSON `json:"outside"`
	// Un couloir par zone qui contient une machine ; le couloir sans zone ferme la liste.
	Home []mapLaneJSON `json:"home"`
	// Les zones où aucune machine ne tourne.
	EmptyZones []string `json:"empty_zones"`
	// Vrai quand la carte est assez grande pour que les hôtes se replient.
	Folding   bool              `json:"folding"`
	Firewalls []mapFirewallJSON `json:"firewalls"`
	Tunnels   []mapTunnelJSON   `json:"tunnels"`
	Entries   []mapEntryJSON    `json:"entries"`
	Links     []mapLinkJSON     `json:"links"`
}

type mapZoneJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// firewall, vlan ou subnet : d'où le nom vient.
	Source string `json:"source"`
	CIDR   string `json:"cidr"`
	VLAN   int    `json:"vlan"`
	// La teinte stable de la zone, de 0 à 7.
	Tone int `json:"tone"`
}

type mapLaneJSON struct {
	ZoneID string       `json:"zone_id"`
	Boxes  []mapBoxJSON `json:"boxes"`
}

// Une machine. managed : un agent la lit ; faux pour un invité que seul son
// hôte connaît. host_id : la carte de son hôte ; guest_count : ses invités ;
// folded : l'hôte se présente replié.
type mapBoxJSON struct {
	ID          string           `json:"id"`
	MachineID   string           `json:"machine_id"`
	Name        string           `json:"name"`
	Kind        string           `json:"kind"`
	VMID        int              `json:"vmid"`
	Node        string           `json:"node"`
	Managed     bool             `json:"managed"`
	Online      bool             `json:"online"`
	GuestStatus string           `json:"guest_status"`
	Address     string           `json:"address"`
	ZoneID      string           `json:"zone_id"`
	Services    []mapServiceJSON `json:"services"`
	HostID      string           `json:"host_id"`
	GuestCount  int              `json:"guest_count"`
	Folded      bool             `json:"folded"`
}

// Une puce : un service dans sa machine. state : running, degraded, stopped,
// failed ou unknown. container_id et service_id disent où elle mène.
type mapServiceJSON struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Replicas    int    `json:"replicas"`
	ContainerID string `json:"container_id"`
	ServiceID   string `json:"service_id"`
}

type mapFirewallJSON struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Interfaces []mapInterfaceJSON `json:"interfaces"`
}

type mapInterfaceJSON struct {
	Name   string `json:"name"`
	Device string `json:"device"`
	Role   string `json:"role"`
	CIDR   string `json:"cidr"`
	Up     bool   `json:"up"`
	ZoneID string `json:"zone_id"`
	Wan    bool   `json:"wan"`
	Tunnel bool   `json:"tunnel"`
}

// Un tunnel chiffré du dehors vers la maison ; proof : observed, declared ou
// deduced, avec les mots des liens.
type mapTunnelJSON struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Proof     string `json:"proof"`
	Outside   string `json:"outside"`
	Service   string `json:"service"`
	Firewall  string `json:"firewall"`
	Interface string `json:"interface"`
}

type mapEntryJSON struct {
	To       string `json:"to"`
	Ports    []int  `json:"ports"`
	Observed bool   `json:"observed"`
}

type mapLinkJSON struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Nature    string `json:"nature"`
	Proof     string `json:"proof"`
	Signal    string `json:"signal"`
	Port      int    `json:"port"`
	Stale     bool   `json:"stale"`
	Proposed  bool   `json:"proposed"`
	FirstSeen string `json:"first_seen_at"`
	LastSeen  string `json:"last_seen_at"`
}

func networkMapToJSON(result netmap.Map) networkMapJSON {
	return networkMapJSON{
		Zones: httpx.ConvertAll(result.Zones, func(zone netmap.Zone) mapZoneJSON {
			return mapZoneJSON{ID: zone.ID, Name: zone.Name, Source: zone.Source, CIDR: zone.CIDR, VLAN: zone.VLAN, Tone: zone.Tone}
		}),
		Outside: httpx.ConvertAll(result.Outside, mapBoxToJSON),
		Home: httpx.ConvertAll(result.Home, func(lane netmap.Lane) mapLaneJSON {
			return mapLaneJSON{ZoneID: lane.ZoneID, Boxes: httpx.ConvertAll(lane.Boxes, mapBoxToJSON)}
		}),
		EmptyZones: result.EmptyZones,
		Folding:    result.Folding,
		Firewalls: httpx.ConvertAll(result.Firewalls, func(box netmap.FirewallBox) mapFirewallJSON {
			return mapFirewallJSON{ID: box.ID, Name: box.Name, Interfaces: httpx.ConvertAll(box.Interfaces, func(iface netmap.FirewallInterface) mapInterfaceJSON {
				return mapInterfaceJSON{Name: iface.Name, Device: iface.Device, Role: iface.Role, CIDR: iface.CIDR, Up: iface.Up, ZoneID: iface.ZoneID, Wan: iface.Wan, Tunnel: iface.Tunnel}
			})}
		}),
		Tunnels: httpx.ConvertAll(result.Tunnels, func(tunnel netmap.Tunnel) mapTunnelJSON {
			return mapTunnelJSON{ID: tunnel.ID, Label: tunnel.Label, Proof: tunnel.Proof, Outside: tunnel.Outside, Service: tunnel.Service, Firewall: tunnel.Firewall, Interface: tunnel.Interface}
		}),
		Entries: httpx.ConvertAll(result.Entries, func(entry netmap.Entry) mapEntryJSON {
			return mapEntryJSON{To: entry.To, Ports: entry.Ports, Observed: entry.Observed}
		}),
		Links: httpx.ConvertAll(result.Links, func(item netmap.MapLink) mapLinkJSON {
			return mapLinkJSON{
				ID: item.ID, From: item.From, To: item.To, Nature: item.Nature, Proof: item.Proof, Signal: item.Signal, Port: item.Port,
				Stale: item.Stale, Proposed: item.Proposed, FirstSeen: item.FirstSeen, LastSeen: item.LastSeen,
			}
		}),
	}
}

func mapBoxToJSON(box netmap.Box) mapBoxJSON {
	return mapBoxJSON{
		ID: box.ID, MachineID: box.MachineID, Name: box.Name, Kind: box.Kind, VMID: box.VMID, Node: box.Node, Managed: box.Managed,
		Online: box.Online, GuestStatus: box.GuestStatus, Address: box.Address, ZoneID: box.ZoneID,
		Services: httpx.ConvertAll(box.Services, func(service netmap.Service) mapServiceJSON {
			return mapServiceJSON{
				ID: service.ID, Label: service.Label, Kind: service.Kind, State: service.State, Replicas: service.Replicas,
				ContainerID: service.ContainerID, ServiceID: service.ServiceID,
			}
		}),
		HostID: box.HostID, GuestCount: box.GuestCount, Folded: box.Folded,
	}
}
