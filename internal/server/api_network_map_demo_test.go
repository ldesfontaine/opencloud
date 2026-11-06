package server

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/netmap"
)

// Les jeux de démonstration de la carte Réseau : la forme d'un vrai réseau
// (une maison à zones, un hôte physique, un VPS dont l'agent n'a pas détecté le
// type), avec des noms et des adresses génériques. Chacun passe par
// netmap.Build puis par la conversion de l'API : le résultat est figé dans
// testdata/network_<nom>.golden.json, que le parcours navigateur sert à la
// place de la réponse du serveur et dont il tire ses captures. Aucune forme n'est
// écrite à la main côté navigateur.

func demoInterface(name, device, role, address string) netmap.Interface {
	return netmap.Interface{Name: name, Device: device, Role: role, Address: address, Up: true}
}

func demoInstance(machineID, key string, replicas ...string) netmap.Instance {
	instance := netmap.Instance{MachineID: machineID, Key: key, Kind: netmap.ServiceCompose, ContainerID: machineID + ":" + key}
	for _, state := range replicas {
		instance.Replicas = append(instance.Replicas, netmap.Replica{State: state})
	}
	return instance
}

// demoNetworkInput : treize interfaces dont la boucle locale, le WAN et un
// tunnel WireGuard ; un VPS à adresse publique de type inconnu avec un service ;
// un hôte physique qui porte dix invités répartis sur cinq zones, avec cinq
// services ; une machine physique sans service dans une zone à part ; deux
// entrées publiques.
func demoNetworkInput() netmap.Input {
	proxy, relay := "proxy", "vps"
	guests := []struct {
		id   string
		vmid int
		kind string
		ip   string
	}{
		{"bastion", 1120, "qemu", "10.10.11.20"}, {"dns", 1121, "lxc", "10.10.11.21"},
		{"proxy", 6010, "qemu", "10.10.60.10"}, {"mail-gw", 6011, "lxc", "10.10.60.11"},
		{"vault", 5010, "qemu", "10.10.50.10"}, {"notes", 5014, "qemu", "10.10.50.14"}, {"files", 5015, "lxc", "10.10.50.15"},
		{"forge", 7010, "lxc", "10.10.70.10"}, {"ci", 7011, "qemu", "10.10.70.11"},
		{"lab", 8010, "qemu", "10.10.80.10"},
	}
	input := netmap.Input{Machines: []netmap.Machine{
		{ID: "pve", Name: "pve", Kind: netmap.KindPhysical, Online: true, Address: "10.10.11.2:8006"},
		{ID: "pi", Name: "pi", Kind: netmap.KindPhysical, Online: true, Address: "10.10.90.2"},
		{ID: relay, Name: "vps-relais", Kind: netmap.KindUnknown, Online: true, Address: "198.51.100.24"},
	}}
	hypervisor := netmap.Hypervisor{MachineID: "pve", Node: "pve"}
	for _, guest := range guests {
		kind := netmap.KindVM
		if guest.kind == "lxc" {
			kind = netmap.KindLXC
		}
		input.Machines = append(input.Machines, netmap.Machine{ID: guest.id, Name: guest.id, Kind: kind, Online: true, Address: guest.ip, HostID: "pve"})
		hypervisor.Guests = append(hypervisor.Guests, netmap.Guest{VMID: guest.vmid, Kind: guest.kind, Name: guest.id, MachineID: guest.id})
	}
	input.Hypervisors = []netmap.Hypervisor{hypervisor}
	input.Firewalls = []netmap.Firewall{{ID: "fw", Name: "pare-feu", Interfaces: []netmap.Interface{
		demoInterface("lo0", "lo0", "Loopback", "127.0.0.1/8"),
		demoInterface("wan", "igc0", "WAN", "203.0.113.2/24"),
		demoInterface("wg", "wg0", "WG_RELAIS", "10.77.0.2/30"),
		demoInterface("rescue", "igc1", "RESCUE", "10.10.99.1/24"),
		demoInterface("switch", "igc2", "SWITCH_MGMT", "10.10.2.1/24"),
		demoInterface("users", "igc3_vlan20", "USERS", "10.10.20.1/24"),
		demoInterface("admin", "igc3_vlan11", "ADMIN", "10.10.11.1/24"),
		demoInterface("projects", "igc3_vlan70", "PROJETS", "10.10.70.1/24"),
		demoInterface("edge", "igc3_vlan60", "EDGE", "10.10.60.1/24"),
		demoInterface("services", "igc3_vlan50", "SERVICES", "10.10.50.1/24"),
		demoInterface("backup", "igc3_vlan90", "BACKUP", "10.10.90.1/24"),
		demoInterface("ai", "igc3_vlan80", "IA", "10.10.80.1/24"),
		demoInterface("guest", "igc3_vlan30", "INVITES", "10.10.30.1/24"),
	}}}
	traefik := demoInstance(proxy, "compose:edge/traefik", "running")
	traefik.PublicPorts, traefik.SeenFromInternet = []int{443}, true
	edgeRelay := demoInstance(relay, "compose:edge/relais", "running")
	edgeRelay.PublicPorts = []int{443, 80}
	input.Instances = []netmap.Instance{
		edgeRelay, traefik,
		demoInstance("vault", "compose:vw/vaultwarden", "running"),
		demoInstance("notes", "compose:notes/notes", "running"),
		demoInstance("forge", "compose:forge/forgejo", "running", "exited"),
		demoInstance("lab", "compose:lab/agent-ia", "running"),
	}
	input.Links = []netmap.Link{
		{ID: "lnk_1", From: netmap.End{MachineID: relay, Key: "compose:edge/relais"}, To: netmap.End{MachineID: proxy, Key: "compose:edge/traefik"}, Nature: "traffic", Proof: "observed", Signal: "host_connection", Port: 443},
		{ID: "lnk_2", From: netmap.End{MachineID: proxy, Key: "compose:edge/traefik"}, To: netmap.End{MachineID: "vault", Key: "compose:vw/vaultwarden"}, Nature: "route", Proof: "deduced", Signal: "public_url", Port: 8080, Proposed: true},
		{ID: "lnk_3", From: netmap.End{MachineID: proxy, Key: "compose:edge/traefik"}, To: netmap.End{MachineID: "forge", Key: "compose:forge/forgejo"}, Nature: "route", Proof: "declared", Signal: "operator", Port: 3000},
		{ID: "lnk_4", From: netmap.End{MachineID: proxy, Key: "compose:edge/traefik"}, To: netmap.End{MachineID: "notes", Key: "compose:notes/notes"}, Nature: "route", Proof: "declared", Signal: "operator", Port: 8081, Stale: true},
	}
	return input
}

// flatNetworkInput : sans Proxmox, sans pare-feu, sans VPS ; des machines à
// plat dans des zones de sous-réseau.
func flatNetworkInput() netmap.Input {
	return netmap.Input{
		Machines: []netmap.Machine{
			{ID: "a", Name: "serveur-a", Kind: netmap.KindPhysical, Online: true, Address: "10.2.1.10"},
			{ID: "b", Name: "serveur-b", Kind: netmap.KindVM, Online: true, Address: "10.2.1.11"},
			{ID: "c", Name: "nas", Kind: netmap.KindPhysical, Online: true, Address: "10.2.9.4"},
			{ID: "d", Name: "imprimante", Kind: netmap.KindUnknown, Online: true, Address: "10.2.9.9"},
		},
		Instances: []netmap.Instance{
			demoInstance("a", "compose:web/web", "running"), demoInstance("a", "compose:web/cache", "running"),
			demoInstance("b", "compose:db/postgres", "running"),
		},
		Links: []netmap.Link{{ID: "lnk_a", From: netmap.End{MachineID: "a", Key: "compose:web/web"}, To: netmap.End{MachineID: "b", Key: "compose:db/postgres"}, Nature: "order", Proof: "declared", Signal: "depends_on", Port: 5432}},
	}
}

// bigNetworkInput : quarante machines sur trois hôtes, de quoi montrer le
// repli. Les hôtes « calme » n'ont aucun service en défaut ; « alerte » en a un.
func bigNetworkInput() netmap.Input {
	input := netmap.Input{}
	for hostIndex, host := range []string{"calme-1", "calme-2", "alerte"} {
		input.Machines = append(input.Machines, netmap.Machine{ID: host, Name: host, Kind: netmap.KindPhysical, Online: true, Address: "10.30." + strconv.Itoa(hostIndex) + ".2"})
		hypervisor := netmap.Hypervisor{MachineID: host, Node: host}
		for index := 0; index < 12; index++ {
			id := host + "-g" + strconv.Itoa(index)
			input.Machines = append(input.Machines, netmap.Machine{
				ID: id, Name: host + "-vm" + strconv.Itoa(index), Kind: netmap.KindVM, Online: true, HostID: host,
				Address: "10.30." + strconv.Itoa(hostIndex) + "." + strconv.Itoa(10+index),
			})
			hypervisor.Guests = append(hypervisor.Guests, netmap.Guest{VMID: 100 + index, Kind: "qemu", Name: id, Node: host, MachineID: id})
			if index%4 == 3 {
				continue
			}
			// Une réplique tombée : le service est dégradé, l'hôte reste ouvert.
			replicas := []string{"running"}
			if host == "alerte" && index == 0 {
				replicas = append(replicas, "exited")
			}
			input.Instances = append(input.Instances, demoInstance(id, "compose:app/app"+strconv.Itoa(index), replicas...))
		}
		input.Hypervisors = append(input.Hypervisors, hypervisor)
	}
	input.Machines = append(input.Machines, netmap.Machine{ID: "pi", Name: "pi", Kind: netmap.KindPhysical, Online: true, Address: "10.30.9.2"})
	return input
}

func mapJSONOf(input netmap.Input) networkMapJSON {
	return networkMapToJSON(netmap.Build(input))
}

// Les jeux de démonstration, figés tels que l'API les rend.
func TestAPI_NetworkMap_DemoDatasetsAreFrozen(t *testing.T) {
	for name, input := range map[string]netmap.Input{"demo": demoNetworkInput(), "flat": flatNetworkInput(), "big": bigNetworkInput()} {
		body, err := json.MarshalIndent(mapJSONOf(input), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		goldentest.Compare(t, filepath.Join("testdata", "network_"+name+".golden.json"), string(body)+"\n")
	}
}

// Le jeu « demo » a la forme annoncée : de quoi lire la séparation Internet /
// ailleurs / chez vous sans explication.
func TestAPI_NetworkMap_DemoDatasetHasTheShapeOfARealNetwork(t *testing.T) {
	result := mapJSONOf(demoNetworkInput())

	if len(result.Outside) != 1 || result.Outside[0].Kind != "unknown" || len(result.Outside[0].Services) != 1 {
		t.Errorf("le VPS de type inconnu est ailleurs avec son service : %+v", result.Outside)
	}
	if len(result.Firewalls) != 1 || len(result.Firewalls[0].Interfaces) != 12 {
		t.Errorf("treize interfaces, sans la boucle locale : %+v", result.Firewalls)
	}
	if len(result.Home) != 6 || len(result.EmptyZones) != 4 || result.Folding {
		t.Errorf("six couloirs, quatre zones vides, aucun repli : %d, %v, %v", len(result.Home), result.EmptyZones, result.Folding)
	}
	if len(result.Tunnels) != 1 || result.Tunnels[0].Outside != result.Outside[0].ID || len(result.Entries) != 2 {
		t.Errorf("un tunnel du VPS, deux entrées publiques : %+v %+v", result.Tunnels, result.Entries)
	}
	guests, services, bare := 0, 0, 0
	for _, box := range mapBoxes(result) {
		services += len(box.Services)
		if box.HostID != "" {
			guests++
		}
		if box.Name == "pi" && len(box.Services) == 0 && box.GuestCount == 0 {
			bare++
		}
	}
	if guests != 10 || services != 5 || bare != 1 {
		t.Errorf("dix invités, cinq services, le Pi sans service : %d, %d, %d", guests, services, bare)
	}
}
