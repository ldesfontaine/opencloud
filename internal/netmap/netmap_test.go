package netmap

import (
	"slices"
	"testing"
)

// Les adresses des tests sont fictives (RFC 5737 et plages privées d'exemple).
func homeFirewall() Firewall {
	return Firewall{ID: "fw", Name: "pare-feu", Interfaces: []Interface{
		{Name: "wan", Device: "igb0", Role: "WAN", Address: "203.0.113.2/24", Up: true},
		{Name: "lan", Device: "igb1", Role: "SERVICES", Address: "10.1.50.1/24", Up: true},
		{Name: "opt1", Device: "igb2_vlan60", Role: "DMZ", Address: "10.1.60.1/24", Up: true},
		{Name: "opt2", Device: "wg0", Role: "TUNNEL", Address: "10.77.0.2/30", Up: true},
	}}
}

func zoneByName(t *testing.T, result Map, name string) Zone {
	t.Helper()
	for _, zone := range result.Zones {
		if zone.Name == name {
			return zone
		}
	}
	t.Fatalf("zone %q absente de %+v", name, result.Zones)
	return Zone{}
}

func box(t *testing.T, boxes []Box, name string) Box {
	t.Helper()
	for _, found := range boxes {
		if found.Name == name {
			return found
		}
		for _, group := range found.Groups {
			for _, guest := range group.Guests {
				if guest.Name == name {
					return guest
				}
			}
		}
	}
	t.Fatalf("boîte %q absente", name)
	return Box{}
}

func fullInput() Input {
	return Input{
		Machines: []Machine{
			{ID: "pve", Name: "pve", Kind: KindPhysical, Online: true, Address: "10.1.11.2:8443"},
			{ID: "pi", Name: "pi", Kind: KindPhysical, Online: true, Address: "10.1.90.2"},
			{ID: "vault", Name: "vault", Kind: KindVM, Online: true, Address: "10.1.50.10", HostID: "pve"},
			{ID: "proxy", Name: "proxy", Kind: KindVM, Online: true, HostID: "pve"},
			{ID: "vps", Name: "vps", Kind: KindVPS, Online: true, Address: "198.51.100.7"},
		},
		Hypervisors: []Hypervisor{{MachineID: "pve", Node: "pve", Guests: []Guest{
			{VMID: 5010, Kind: "qemu", Name: "vault", MachineID: "vault", VLAN: 50},
			{VMID: 6010, Kind: "qemu", Name: "proxy", MachineID: "proxy", VLAN: 60},
			{VMID: 5011, Kind: "lxc", Name: "forge", Status: "running", VLAN: 50},
			{VMID: 9000, Kind: "qemu", Name: "modele", Template: true},
		}}},
		Firewalls: []Firewall{homeFirewall()},
		Instances: []Instance{
			{MachineID: "vault", Key: "compose:vw/vaultwarden", Kind: ServiceCompose, Replicas: []Replica{{State: "running"}}, ContainerID: "c1"},
			{MachineID: "proxy", Key: "compose:edge/traefik", Kind: ServiceCompose, Replicas: []Replica{{State: "running"}}, PublicPorts: []int{443, 80}},
			{MachineID: "vps", Key: "systemd:wg-quick@wg0.service", Kind: ServiceSystemd, Active: "active", ServiceID: "svc-wg"},
		},
		Links: []Link{
			{ID: "l1", From: End{"proxy", "compose:edge/traefik"}, To: End{"vault", "compose:vw/vaultwarden"}, Nature: "route", Proof: "deduced", Signal: "public_url", Port: 8080},
			{ID: "l2", From: End{"proxy", "compose:edge/traefik"}, To: End{"vault", "container:absent"}, Nature: "order", Proof: "declared"},
		},
	}
}

func TestBuild_GuestsLiveInsideTheirHostGroupedByZone(t *testing.T) {
	result := Build(fullInput())

	for _, top := range result.Home {
		if top.Name == "vault" || top.Name == "proxy" {
			t.Fatalf("%s apparaît à côté de son hôte", top.Name)
		}
	}
	host := box(t, result.Home, "pve")
	if len(host.Groups) != 2 {
		t.Fatalf("pve doit porter deux zones d'invités : %+v", host.Groups)
	}
	services := zoneByName(t, result, "SERVICES")
	dmz := zoneByName(t, result, "DMZ")
	if host.Groups[0].ZoneID != services.ID || host.Groups[1].ZoneID != dmz.ID {
		t.Errorf("zones dans l'ordre du pare-feu attendues : %+v", host.Groups)
	}
	// vault par son adresse, forge (sans agent) par son tag VLAN vu chez vault,
	// proxy (sans adresse) par le nom de l'appareil VLAN du pare-feu.
	if got := box(t, result.Home, "forge"); got.Managed || got.VMID != 5011 || got.ZoneID != services.ID {
		t.Errorf("forge : %+v", got)
	}
	if got := box(t, result.Home, "proxy"); got.ZoneID != dmz.ID || got.VMID != 6010 {
		t.Errorf("proxy : %+v", got)
	}
	if services.Source != SourceFirewall || services.VLAN != 50 {
		t.Errorf("la zone du pare-feu apprend le tag du vote : %+v", services)
	}
	for _, guest := range host.Groups[0].Guests {
		if guest.Name == "modele" {
			t.Error("un modèle de VM n'est pas une machine")
		}
	}
}

func TestBuild_OutsideSideAndTunnel(t *testing.T) {
	result := Build(fullInput())

	if len(result.Outside) != 1 || result.Outside[0].Name != "vps" {
		t.Fatalf("le VPS est dehors : %+v", result.Outside)
	}
	if len(result.Tunnels) != 1 {
		t.Fatalf("un tunnel attendu : %+v", result.Tunnels)
	}
	tunnel := result.Tunnels[0]
	if tunnel.Proof != ProofObserved || tunnel.Label != "WireGuard" || tunnel.Firewall != "fw" || tunnel.Interface != "opt2" || tunnel.Service != "s:vps|systemd:wg-quick@wg0.service" {
		t.Errorf("tunnel : %+v", tunnel)
	}
	if got := result.Firewalls[0].Interfaces; !got[0].Wan || !got[3].Tunnel || got[1].ZoneID == "" || got[3].ZoneID != "" {
		t.Errorf("interfaces du pare-feu : %+v", got)
	}
}

func TestBuild_TunnelProofFollowsWhatIsSeen(t *testing.T) {
	input := fullInput()
	input.Firewalls = nil
	if proof := Build(input).Tunnels[0].Proof; proof != ProofDeclared {
		t.Errorf("service d'annuaire sans pare-feu : %s", proof)
	}
	input.Instances[2].ServiceID = ""
	if proof := Build(input).Tunnels[0].Proof; proof != ProofDeduced {
		t.Errorf("unité vue seule : %s", proof)
	}
}

func TestBuild_ServicesStatesEntriesAndLinks(t *testing.T) {
	result := Build(fullInput())

	vault := box(t, result.Home, "vault")
	if len(vault.Services) != 1 || vault.Services[0].Label != "vaultwarden" || vault.Services[0].State != StateRunning {
		t.Errorf("services de vault : %+v", vault.Services)
	}
	if len(result.Entries) != 1 || result.Entries[0].To != "s:proxy|compose:edge/traefik" || !slices.Equal(result.Entries[0].Ports, []int{80, 443}) {
		t.Errorf("entrées : %+v", result.Entries)
	}
	if len(result.Links) != 1 || result.Links[0].ID != "l1" || result.Links[0].Proof != "deduced" {
		t.Errorf("seul le lien entre deux puces de la carte est tracé : %+v", result.Links)
	}
}

func TestBuild_WithoutProxmoxMachinesStayFlatWithSubnetZones(t *testing.T) {
	input := Input{Machines: []Machine{
		{ID: "a", Name: "a", Kind: KindPhysical, Online: true, Address: "10.2.1.5"},
		{ID: "b", Name: "b", Kind: KindVM, Online: true, Address: "10.2.1.6"},
		{ID: "c", Name: "c", Kind: KindPhysical, Online: true, Address: "10.2.9.5"},
	}}
	result := Build(input)

	if len(result.Home) != 3 || len(result.Outside) != 0 || len(result.Firewalls) != 0 || len(result.Tunnels) != 0 {
		t.Fatalf("carte à plat attendue : %+v", result)
	}
	if len(result.Zones) != 2 || result.Zones[0].Source != SourceSubnet {
		t.Fatalf("deux zones de sous-réseau attendues : %+v", result.Zones)
	}
	if box(t, result.Home, "a").ZoneID != box(t, result.Home, "b").ZoneID || box(t, result.Home, "a").ZoneID == box(t, result.Home, "c").ZoneID {
		t.Errorf("a et b partagent une zone, pas c")
	}
}

func TestBuild_WithoutFirewallVLANTagNamesTheSubnetZone(t *testing.T) {
	input := fullInput()
	input.Firewalls = nil
	input.Instances = nil
	result := Build(input)

	zone := zoneByName(t, result, "VLAN 50")
	if zone.Source != SourceVLAN || zone.CIDR != "10.1.50.0/24" {
		t.Errorf("zone : %+v", zone)
	}
	if got := box(t, result.Home, "proxy"); got.ZoneID != "vlan:60" {
		t.Errorf("un tag sans adresse ni pare-feu fait sa zone : %+v", got)
	}
}

func TestBuild_WithoutAnyVPSOrHypervisorTheMapStillBuilds(t *testing.T) {
	result := Build(Input{})
	if result.Home == nil || result.Outside == nil || result.Zones != nil && len(result.Zones) != 0 {
		t.Errorf("carte vide : %+v", result)
	}
}

func TestBuild_ZoneTonesAreStableAndDistinct(t *testing.T) {
	first, second := Build(fullInput()), Build(fullInput())
	if !slices.Equal(first.Zones, second.Zones) {
		t.Fatal("deux lectures des mêmes faits dessinent d'autres teintes")
	}
	taken := map[int]bool{}
	for _, zone := range first.Zones {
		if taken[zone.Tone] {
			t.Errorf("teinte %d prise deux fois : %+v", zone.Tone, first.Zones)
		}
		taken[zone.Tone] = true
	}
}

func TestBuild_ClusterGuestIsKeptByTheHostOfItsNode(t *testing.T) {
	shared := []Guest{{VMID: 100, Kind: "qemu", Name: "lourd", Node: "pve2"}}
	input := Input{
		Machines: []Machine{
			{ID: "h1", Name: "h1", Kind: KindPhysical, Online: true},
			{ID: "h2", Name: "h2", Kind: KindPhysical, Online: true},
		},
		Hypervisors: []Hypervisor{{MachineID: "h1", Node: "pve1", Guests: shared}, {MachineID: "h2", Node: "pve2", Guests: shared}},
	}
	result := Build(input)
	if len(box(t, result.Home, "h1").Groups) != 0 || len(box(t, result.Home, "h2").Groups) != 1 {
		t.Errorf("l'invité est chez h2 seulement : %+v", result.Home)
	}
}

func TestBuild_HostCycleDoesNotLoop(t *testing.T) {
	input := Input{Machines: []Machine{
		{ID: "a", Name: "a", Kind: KindVM, Online: true, HostID: "b"},
		{ID: "b", Name: "b", Kind: KindVM, Online: true, HostID: "a"},
	}}
	if got := Build(input); len(got.Home) != 2 {
		t.Errorf("une chaîne d'hôtes qui boucle laisse les machines à plat : %+v", got.Home)
	}
}

func TestStateOf(t *testing.T) {
	cases := []struct {
		name     string
		instance Instance
		online   bool
		want     string
	}{
		{"conteneur sain", Instance{Replicas: []Replica{{State: "running"}}}, true, StateRunning},
		{"une réplique sur deux", Instance{Replicas: []Replica{{State: "running"}, {State: "exited"}}}, true, StateDegraded},
		{"healthcheck en échec", Instance{Replicas: []Replica{{State: "running", Unhealthy: true}}}, true, StateDegraded},
		{"arrêté", Instance{Replicas: []Replica{{State: "exited"}}}, true, StateStopped},
		{"unité en échec", Instance{Kind: ServiceSystemd, Active: "failed"}, true, StateFailed},
		{"unité inactive", Instance{Kind: ServiceSystemd, Active: "inactive"}, true, StateStopped},
		{"machine muette", Instance{Replicas: []Replica{{State: "running"}}}, false, StateUnknown},
	}
	for _, c := range cases {
		if got := stateOf(c.instance, c.online); got != c.want {
			t.Errorf("%s : %s, attendu %s", c.name, got, c.want)
		}
	}
}
