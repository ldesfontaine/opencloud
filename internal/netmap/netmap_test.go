package netmap

import (
	"slices"
	"strconv"
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

func allBoxes(result Map) []Box {
	boxes := slices.Clone(result.Outside)
	for _, lane := range result.Home {
		boxes = append(boxes, lane.Boxes...)
	}
	return boxes
}

func box(t *testing.T, result Map, name string) Box {
	t.Helper()
	for _, found := range allBoxes(result) {
		if found.Name == name {
			return found
		}
	}
	t.Fatalf("boîte %q absente", name)
	return Box{}
}

func laneOf(t *testing.T, result Map, zoneID string) Lane {
	t.Helper()
	for _, lane := range result.Home {
		if lane.ZoneID == zoneID {
			return lane
		}
	}
	t.Fatalf("couloir %q absent de %+v", zoneID, result.Home)
	return Lane{}
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

func TestBuild_EveryGuestHasItsCardInTheLaneOfItsZoneAndNamesItsHost(t *testing.T) {
	result := Build(fullInput())

	host := box(t, result, "pve")
	if host.GuestCount != 3 || host.HostID != "" {
		t.Fatalf("pve porte trois invités (le modèle n'en est pas un) : %+v", host)
	}
	services := zoneByName(t, result, "SERVICES")
	dmz := zoneByName(t, result, "DMZ")
	// vault par son adresse, forge (sans agent) par son tag VLAN vu chez vault,
	// proxy (sans adresse) par le nom de l'appareil VLAN du pare-feu.
	for name, want := range map[string]struct {
		zone string
		vmid int
	}{"vault": {services.ID, 5010}, "forge": {services.ID, 5011}, "proxy": {dmz.ID, 6010}} {
		got := box(t, result, name)
		if got.ZoneID != want.zone || got.VMID != want.vmid || got.HostID != host.ID {
			t.Errorf("%s : %+v", name, got)
		}
		if lane := laneOf(t, result, want.zone); !slices.ContainsFunc(lane.Boxes, func(found Box) bool { return found.ID == got.ID }) {
			t.Errorf("%s n'est pas dans le couloir de sa zone", name)
		}
	}
	if box(t, result, "forge").Managed {
		t.Error("forge n'a pas d'agent")
	}
	if result.Zones[0].ID != services.ID || result.Home[0].ZoneID != services.ID || result.Home[1].ZoneID != dmz.ID {
		t.Errorf("couloirs dans l'ordre des zones du pare-feu attendus : %+v", result.Home)
	}
	if services.Source != SourceFirewall || services.VLAN != 50 {
		t.Errorf("la zone du pare-feu apprend le tag du vote : %+v", services)
	}
	if slices.ContainsFunc(allBoxes(result), func(found Box) bool { return found.Name == "modele" }) {
		t.Error("un modèle de VM n'est pas une machine")
	}
}

func TestBuild_OutsideSideAndTunnel(t *testing.T) {
	result := Build(fullInput())

	if len(result.Outside) != 1 || result.Outside[0].Name != "vps" || result.Outside[0].ZoneID != "" {
		t.Fatalf("le VPS est dehors, sans zone : %+v", result.Outside)
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

// La règle générale : une machine sans hôte connu est dehors si son adresse est
// publique, quel que soit son type ; chez vous si elle est privée.
func TestBuild_UnhostedMachineWithPublicAddressIsOutsideWhateverItsKind(t *testing.T) {
	cases := []struct {
		name        string
		machine     Machine
		wantOutside bool
	}{
		{"type inconnu, adresse publique", Machine{ID: "a", Name: "a", Kind: KindUnknown, Address: "198.51.100.24:8443"}, true},
		{"physique, adresse publique", Machine{ID: "a", Name: "a", Kind: KindPhysical, Address: "198.51.100.24"}, true},
		{"VPS, adresse privée (derrière un tunnel)", Machine{ID: "a", Name: "a", Kind: KindVPS, Address: "10.77.0.1"}, true},
		{"type inconnu, adresse privée", Machine{ID: "a", Name: "a", Kind: KindUnknown, Address: "10.1.50.9"}, false},
		{"type inconnu, sans adresse", Machine{ID: "a", Name: "a", Kind: KindUnknown}, false},
		{"type inconnu, nom plutôt qu'adresse", Machine{ID: "a", Name: "a", Kind: KindUnknown, Address: "nas.lan"}, false},
	}
	for _, c := range cases {
		result := Build(Input{Machines: []Machine{c.machine}, Firewalls: []Firewall{homeFirewall()}})
		if outside, home := len(result.Outside) == 1, len(result.Home) == 1; outside != c.wantOutside || home == c.wantOutside {
			t.Errorf("%s : dehors %v, chez vous %v, attendu dehors %v (%+v)", c.name, outside, home, c.wantOutside, result)
		}
	}
}

// Un hôte en adresse publique emmène ses invités ; un invité n'a pas de zone
// tant que son hôte est dehors.
func TestBuild_GuestsFollowTheirHostOutside(t *testing.T) {
	input := Input{
		Machines: []Machine{
			{ID: "h", Name: "h", Kind: KindPhysical, Online: true, Address: "198.51.100.24"},
			{ID: "g", Name: "g", Kind: KindVM, Online: true, Address: "10.1.50.9", HostID: "h"},
		},
		Hypervisors: []Hypervisor{{MachineID: "h", Guests: []Guest{{VMID: 100, Kind: "qemu", Name: "g", MachineID: "g"}}}},
		Firewalls:   []Firewall{homeFirewall()},
	}
	result := Build(input)

	if len(result.Outside) != 2 || len(result.Home) != 0 {
		t.Fatalf("l'hôte et son invité sont dehors : %+v", result)
	}
	if got := box(t, result, "g"); got.ZoneID != "" || got.HostID != "m:h" {
		t.Errorf("g : %+v", got)
	}
}

// Les zones du pare-feu où rien ne tourne ne sont pas des couloirs : elles se
// listent à part. La boucle locale n'est ni une zone ni une interface.
func TestBuild_EmptyZonesAreListedApartAndLoopbackIsNeverShown(t *testing.T) {
	input := fullInput()
	input.Firewalls[0].Interfaces = append(input.Firewalls[0].Interfaces,
		Interface{Name: "opt3", Device: "igb3", Role: "RESCUE", Address: "10.1.99.1/24", Up: true},
		Interface{Name: "loopback", Device: "lo0", Role: "Loopback", Address: "127.0.0.1/8", Up: true},
		Interface{Name: "lo", Device: "lo", Role: "Boucle", Address: "::1/128", Up: true},
	)
	result := Build(input)

	rescue := zoneByName(t, result, "RESCUE")
	if !slices.Equal(result.EmptyZones, []string{rescue.ID}) {
		t.Errorf("seule RESCUE est sans machine : %v", result.EmptyZones)
	}
	for _, lane := range result.Home {
		if lane.ZoneID == rescue.ID {
			t.Error("une zone sans machine n'a pas de couloir")
		}
	}
	for _, zone := range result.Zones {
		if zone.Name == "Loopback" || zone.Name == "Boucle" {
			t.Errorf("la boucle locale est une zone : %+v", zone)
		}
	}
	for _, iface := range result.Firewalls[0].Interfaces {
		if iface.Device == "lo0" || iface.Device == "lo" {
			t.Errorf("la boucle locale est dessinée : %+v", iface)
		}
	}
}

// Rien dans la sortie ne relie une interface à une machine : une interface
// n'a qu'une zone, et le trait d'un tunnel va de la machine du dehors à
// l'interface du tunnel.
func TestBuild_NoInterfaceIsTiedToAMachine(t *testing.T) {
	result := Build(fullInput())

	zones := map[string]bool{}
	for _, zone := range result.Zones {
		zones[zone.ID] = true
	}
	for _, iface := range result.Firewalls[0].Interfaces {
		if iface.ZoneID != "" && !zones[iface.ZoneID] {
			t.Errorf("l'interface %s désigne autre chose qu'une zone : %q", iface.Name, iface.ZoneID)
		}
		if (iface.Wan || iface.Tunnel) && iface.ZoneID != "" {
			t.Errorf("l'interface %s est publique ou de tunnel et n'a pas de zone : %+v", iface.Name, iface)
		}
	}
	tunnel := result.Tunnels[0]
	if tunnel.Outside != result.Outside[0].ID || tunnel.Interface != "opt2" {
		t.Errorf("le tunnel va de la machine du dehors à l'interface du tunnel : %+v", tunnel)
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

	vault := box(t, result, "vault")
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

	if len(allBoxes(result)) != 3 || len(result.Home) != 2 || len(result.Outside) != 0 || len(result.Firewalls) != 0 || len(result.Tunnels) != 0 {
		t.Fatalf("carte à plat attendue : %+v", result)
	}
	if len(result.Zones) != 2 || result.Zones[0].Source != SourceSubnet {
		t.Fatalf("deux zones de sous-réseau attendues : %+v", result.Zones)
	}
	if box(t, result, "a").ZoneID != box(t, result, "b").ZoneID || box(t, result, "a").ZoneID == box(t, result, "c").ZoneID {
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
	if got := box(t, result, "proxy"); got.ZoneID != "vlan:60" {
		t.Errorf("un tag sans adresse ni pare-feu fait sa zone : %+v", got)
	}
}

func TestBuild_WithoutAnyVPSOrHypervisorTheMapStillBuilds(t *testing.T) {
	result := Build(Input{})
	if result.Home == nil || result.Outside == nil {
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
	if box(t, result, "h1").GuestCount != 0 || box(t, result, "h2").GuestCount != 1 || box(t, result, "lourd").HostID != "m:h2" {
		t.Errorf("l'invité est chez h2 seulement : %+v", result.Home)
	}
}

func TestBuild_HostCycleDoesNotLoop(t *testing.T) {
	input := Input{Machines: []Machine{
		{ID: "a", Name: "a", Kind: KindVM, Online: true, HostID: "b"},
		{ID: "b", Name: "b", Kind: KindVM, Online: true, HostID: "a"},
	}}
	if got := Build(input); len(allBoxes(got)) != 2 || allBoxes(got)[0].HostID != "" || allBoxes(got)[1].HostID != "" {
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

// manyMachines : un hôte et count-1 invités, chacun avec un service.
func manyMachines(count int) Input {
	input := Input{Machines: []Machine{{ID: "pve", Name: "pve", Kind: KindPhysical, Online: true, Address: "10.1.11.2"}}}
	hypervisor := Hypervisor{MachineID: "pve"}
	for index := 1; index < count; index++ {
		id := "vm" + strconv.Itoa(index)
		input.Machines = append(input.Machines, Machine{ID: id, Name: id, Kind: KindVM, Online: true, Address: "10.1.11." + strconv.Itoa(10+index), HostID: "pve"})
		hypervisor.Guests = append(hypervisor.Guests, Guest{VMID: 100 + index, Kind: "qemu", Name: id, MachineID: id})
		input.Instances = append(input.Instances, Instance{MachineID: id, Key: "compose:app/web", Kind: ServiceCompose, Replicas: []Replica{{State: "running"}}})
	}
	input.Hypervisors = []Hypervisor{hypervisor}
	return input
}

// Sous le seuil, aucun hôte ne se replie, même sans service en défaut.
func TestBuild_NoHostFoldsBelowTheThreshold(t *testing.T) {
	result := Build(manyMachines(39))

	if result.Folding || box(t, result, "pve").Folded {
		t.Errorf("la carte de 39 machines ne se replie pas : %+v", box(t, result, "pve"))
	}
}

// Au seuil, l'hôte sans service en défaut se replie ; celui dont un invité va
// mal reste ouvert.
func TestBuild_HostFoldsAtTheThresholdUnlessAGuestServiceIsInTrouble(t *testing.T) {
	calm := Build(manyMachines(40))
	if !calm.Folding || !box(t, calm, "pve").Folded {
		t.Fatalf("l'hôte calme se replie à 40 machines : %+v", box(t, calm, "pve"))
	}
	if box(t, calm, "vm1").Folded {
		t.Error("un invité sans invité ne se replie pas")
	}

	troubled := manyMachines(40)
	troubled.Instances[0].Replicas = []Replica{{State: "exited"}, {State: "running"}}
	if got := Build(troubled); !got.Folding || box(t, got, "pve").Folded {
		t.Errorf("l'hôte dont un invité est dégradé reste ouvert : %+v", box(t, got, "pve"))
	}
}
