package netmap

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// maxHostDepth borne la remontée d'une chaîne d'hôtes : un cycle ne boucle pas.
const maxHostDepth = 8

// Build dessine la carte. Les boîtes, les zones et les puces sortent dans un
// ordre fixe : deux lectures des mêmes faits rendent la même carte.
func Build(in Input) Map {
	b := newBuilder(in)
	b.placeMachines()
	b.placeGuests()
	b.giveZones()
	b.attachServices()
	return b.finish()
}

// builder porte l'état d'un dessin ; chaque étape y pose un morceau.
type builder struct {
	in        Input
	machines  map[string]Machine
	boxes     map[string]*Box
	order     []string
	parent    map[string]string
	children  map[string][]string
	subjects  map[string]subject
	zone      map[string]string
	zoner     *zoner
	services  map[string]string
	entries   []Entry
	hosts     map[string]Hypervisor
	nodeOwner map[string]string
}

func newBuilder(in Input) *builder {
	b := &builder{
		in: in, machines: map[string]Machine{}, boxes: map[string]*Box{}, parent: map[string]string{},
		children: map[string][]string{}, subjects: map[string]subject{}, services: map[string]string{},
		hosts: map[string]Hypervisor{}, nodeOwner: map[string]string{}, zoner: newZoner(in.Firewalls),
	}
	for _, found := range in.Machines {
		b.machines[found.ID] = found
	}
	for _, hypervisor := range in.Hypervisors {
		b.hosts[hypervisor.MachineID] = hypervisor
		if hypervisor.Node != "" {
			b.nodeOwner[hypervisor.Node] = hypervisor.MachineID
		}
	}
	return b
}

func machineBoxID(machineID string) string { return "m:" + machineID }

// placeMachines crée la boîte de chaque machine enrôlée et range les invités
// sous la racine de leur chaîne d'hôtes. Une machine dont l'hôte est inconnu,
// ou dont la chaîne boucle, reste à plat.
func (b *builder) placeMachines() {
	ids := make([]string, 0, len(b.machines))
	for id := range b.machines {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, c string) int { return compareMachines(b.machines[a], b.machines[c]) })
	vmids := b.vmidsOfMachines()
	for _, id := range ids {
		found := b.machines[id]
		b.addBox(&Box{
			ID: machineBoxID(id), MachineID: id, Name: found.Name, Kind: found.Kind, VMID: vmids[id],
			Managed: true, Online: found.Online, Address: hostOf(found.Address),
		})
		b.subjects[machineBoxID(id)] = subject{address: addressOf(found.Address), vlan: b.vlanOfMachine(id)}
	}
	for _, id := range ids {
		if root := b.rootOf(id); root != "" {
			b.parent[machineBoxID(id)] = machineBoxID(root)
			b.children[machineBoxID(root)] = append(b.children[machineBoxID(root)], machineBoxID(id))
		}
	}
}

func compareMachines(a, c Machine) int {
	if byName := strings.Compare(a.Name, c.Name); byName != 0 {
		return byName
	}
	return strings.Compare(a.ID, c.ID)
}

func (b *builder) addBox(box *Box) {
	b.boxes[box.ID] = box
	b.order = append(b.order, box.ID)
}

// rootOf remonte la chaîne d'hôtes d'une machine jusqu'à la machine qui n'en
// a pas ; vide si la machine n'a pas d'hôte connu, ou si la chaîne boucle.
func (b *builder) rootOf(id string) string {
	current := id
	for depth := 0; depth < maxHostDepth; depth++ {
		hostID := b.machines[current].HostID
		if _, known := b.machines[hostID]; !known || hostID == "" {
			if current == id {
				return ""
			}
			return current
		}
		if hostID == id {
			return ""
		}
		current = hostID
	}
	return ""
}

// vmidsOfMachines rend le numéro d'invité de chaque machine que son hôte
// rapproche, et ses VLAN, pour la zone.
func (b *builder) vmidsOfMachines() map[string]int {
	vmids := map[string]int{}
	for _, hypervisor := range b.in.Hypervisors {
		for _, guest := range hypervisor.Guests {
			if guest.MachineID != "" && !guest.Template {
				vmids[guest.MachineID] = guest.VMID
			}
		}
	}
	return vmids
}

func (b *builder) vlanOfMachine(id string) int {
	for _, hypervisor := range b.in.Hypervisors {
		for _, guest := range hypervisor.Guests {
			if guest.MachineID == id && guest.VLAN > 0 {
				return guest.VLAN
			}
		}
	}
	return 0
}

// placeGuests ajoute les invités que l'hôte connaît sans machine enrôlée : une
// boîte sans agent, sous son hôte. Un invité que la grappe annonce deux fois
// n'est gardé que chez l'hôte de son nœud.
func (b *builder) placeGuests() {
	hostIDs := make([]string, 0, len(b.hosts))
	for id := range b.hosts {
		hostIDs = append(hostIDs, id)
	}
	slices.Sort(hostIDs)
	for _, hostID := range hostIDs {
		if _, known := b.machines[hostID]; !known {
			continue
		}
		hypervisor := b.hosts[hostID]
		for _, guest := range hypervisor.Guests {
			if guest.Template || guest.MachineID != "" || !b.ownsGuest(hypervisor, guest) {
				continue
			}
			id := fmt.Sprintf("g:%s:%d", hostID, guest.VMID)
			kind := KindVM
			if guest.Kind == "lxc" {
				kind = KindLXC
			}
			b.addBox(&Box{ID: id, Name: guest.Name, Kind: kind, VMID: guest.VMID, Node: guest.Node, GuestStatus: guest.Status})
			b.subjects[id] = subject{vlan: guest.VLAN}
			root := hostID
			if found := b.rootOf(hostID); found != "" {
				root = found
			}
			b.parent[id] = machineBoxID(root)
			b.children[machineBoxID(root)] = append(b.children[machineBoxID(root)], id)
		}
	}
}

// ownsGuest : l'invité est celui de ce nœud, ou d'un nœud qu'aucun hôte
// déclaré ne porte.
func (b *builder) ownsGuest(hypervisor Hypervisor, guest Guest) bool {
	owner, claimed := b.nodeOwner[guest.Node]
	return guest.Node == "" || !claimed || owner == hypervisor.MachineID
}

// giveZones donne à chaque boîte de la maison sa zone ; celles du dehors n'en
// ont pas.
func (b *builder) giveZones() {
	home := map[string]subject{}
	for id, found := range b.subjects {
		if b.isOutside(b.boxes[id]) {
			continue
		}
		home[id] = found
	}
	b.zone = b.zoner.resolve(home)
	for id, zoneID := range b.zone {
		b.boxes[id].ZoneID = zoneID
	}
}

// isOutside : la machine n'est pas dans la maison. Un invité suit son hôte.
// Une machine sans hôte connu est dehors quand c'est un VPS ou quand son
// adresse est publique, quel que soit le type que l'agent a pu détecter.
func (b *builder) isOutside(box *Box) bool {
	if parent, hosted := b.parent[box.ID]; hosted {
		return b.isOutside(b.boxes[parent])
	}
	if box.Kind == KindVPS {
		return true
	}
	address := b.subjects[box.ID].address
	return address.IsValid() && isPublic(address.String())
}

// attachServices pose les puces sur leurs machines et en tire les entrées
// d'Internet.
func (b *builder) attachServices() {
	for _, instance := range b.in.Instances {
		box, known := b.boxes[machineBoxID(instance.MachineID)]
		if !known {
			continue
		}
		id := serviceID(instance.MachineID, instance.Key)
		service := Service{
			ID: id, Label: labelOf(instance.Key), Kind: instance.Kind, State: stateOf(instance, box.Online),
			Replicas: len(instance.Replicas), ContainerID: instance.ContainerID, ServiceID: instance.ServiceID,
		}
		box.Services = append(box.Services, service)
		b.services[id] = box.ID
		if len(instance.PublicPorts) > 0 || instance.SeenFromInternet {
			ports := slices.Clone(instance.PublicPorts)
			slices.Sort(ports)
			b.entries = append(b.entries, Entry{To: id, Ports: slices.Compact(ports), Observed: instance.SeenFromInternet})
		}
	}
	for _, box := range b.boxes {
		slices.SortFunc(box.Services, func(a, c Service) int { return strings.Compare(a.Label+a.ID, c.Label+c.ID) })
	}
}

func serviceID(machineID, key string) string { return "s:" + machineID + "|" + key }

// labelOf rend le nom qu'on lit d'une clé d'instance : le service d'un projet
// Compose, le nom d'un conteneur, l'unité sans son suffixe.
func labelOf(key string) string {
	_, name, _ := strings.Cut(key, ":")
	if _, service, found := strings.Cut(name, "/"); found {
		name = service
	}
	return strings.TrimSuffix(name, ".service")
}

// stateOf dit l'état d'une puce. Une machine qui ne signale plus ne dit plus
// rien de ce qui tourne : inconnu, jamais un arrêt.
func stateOf(instance Instance, online bool) string {
	if !online {
		return StateUnknown
	}
	if instance.Missing {
		return StateMissing
	}
	if instance.Kind == ServiceSystemd {
		switch instance.Active {
		case "active":
			return StateRunning
		case "failed":
			return StateFailed
		case "inactive", "deactivating":
			return StateStopped
		case "activating", "reloading":
			return StateDegraded
		}
		return StateUnknown
	}
	running, unwell := 0, 0
	for _, replica := range instance.Replicas {
		switch {
		case replica.State == "running" && !replica.Unhealthy:
			running++
		case replica.State == "running" || replica.State == "restarting" || replica.State == "paused":
			unwell++
		}
	}
	switch {
	case len(instance.Replicas) == 0:
		return StateUnknown
	case running == len(instance.Replicas):
		return StateRunning
	case running > 0 || unwell > 0:
		return StateDegraded
	}
	return StateStopped
}

func hostOf(address string) string {
	if found := addressOf(address); found.IsValid() {
		return found.String()
	}
	return address
}

func (b *builder) finish() Map {
	result := Map{Outside: []Box{}, Home: []Lane{}, EmptyZones: []string{}, Firewalls: []FirewallBox{}, Tunnels: []Tunnel{}, Entries: []Entry{}, Links: []MapLink{}}
	used := map[string]bool{}
	for _, id := range b.order {
		if zoneID := b.boxes[id].ZoneID; zoneID != "" {
			used[zoneID] = true
		}
	}
	result.Zones = b.zoner.list(used)
	result.Folding = len(b.order) >= FoldFrom
	lanes := map[string][]Box{}
	for _, id := range b.order {
		box := b.completed(id, result.Folding)
		if b.isOutside(b.boxes[id]) {
			result.Outside = append(result.Outside, box)
			continue
		}
		lanes[box.ZoneID] = append(lanes[box.ZoneID], box)
	}
	sortHostsFirst(result.Outside)
	for _, zone := range result.Zones {
		if boxes, found := lanes[zone.ID]; found {
			sortHostsFirst(boxes)
			result.Home = append(result.Home, Lane{ZoneID: zone.ID, Boxes: boxes})
		} else {
			result.EmptyZones = append(result.EmptyZones, zone.ID)
		}
		delete(lanes, zone.ID)
	}
	// Ce qui reste n'a pas de zone connue : un couloir à la fin.
	if boxes, found := lanes[""]; found {
		sortHostsFirst(boxes)
		result.Home = append(result.Home, Lane{Boxes: boxes})
	}
	result.Firewalls = b.firewallBoxes()
	result.Tunnels = b.tunnels(result)
	result.Entries, result.Links = b.entriesAndLinks()
	return result
}

// sortHostsFirst met les hôtes d'abord, puis le reste par nom.
func sortHostsFirst(boxes []Box) {
	slices.SortStableFunc(boxes, func(a, c Box) int {
		if hostA, hostC := a.GuestCount > 0, c.GuestCount > 0; hostA != hostC {
			if hostA {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name+a.ID, c.Name+c.ID)
	})
}

// completed rend la boîte avec ce que la carte en dit : son hôte, le nombre de
// ses invités, et s'il se présente replié.
func (b *builder) completed(id string, folding bool) Box {
	box := *b.boxes[id]
	if box.Services == nil {
		box.Services = []Service{}
	}
	box.HostID = b.parent[id]
	box.GuestCount = len(b.children[id])
	box.Folded = folding && box.GuestCount > 0 && !b.hasTrouble(id)
	return box
}

// hasTrouble : un service de la machine ou de ses invités est en échec ou à
// moitié là. Un hôte dans ce cas reste ouvert.
func (b *builder) hasTrouble(id string) bool {
	for _, machineID := range append([]string{id}, b.children[id]...) {
		for _, service := range b.boxes[machineID].Services {
			if service.State == StateFailed || service.State == StateDegraded {
				return true
			}
		}
	}
	return false
}

func (b *builder) firewallBoxes() []FirewallBox {
	boxes := make([]FirewallBox, 0, len(b.in.Firewalls))
	for _, firewall := range b.in.Firewalls {
		box := FirewallBox{ID: firewall.ID, Name: firewall.Name, Interfaces: make([]FirewallInterface, 0, len(firewall.Interfaces))}
		for _, iface := range firewall.Interfaces {
			if isLoopback(iface) {
				continue
			}
			shown := FirewallInterface{
				Name: iface.Name, Device: iface.Device, Role: iface.Role, CIDR: iface.Address, Up: iface.Up,
				Wan: isPublic(iface.Address), Tunnel: isTunnelDevice(iface.Device),
			}
			if !shown.Wan && !shown.Tunnel {
				if _, known := b.zoner.zones["fw:"+firewall.ID+":"+iface.Name]; known {
					shown.ZoneID = "fw:" + firewall.ID + ":" + iface.Name
				}
			}
			box.Interfaces = append(box.Interfaces, shown)
		}
		boxes = append(boxes, box)
	}
	slices.SortStableFunc(boxes, func(a, c FirewallBox) int { return strings.Compare(a.Name+a.ID, c.Name+c.ID) })
	return boxes
}

// tunnels déduit les tunnels : le service d'une machine du dehors qui porte
// WireGuard, et l'interface de tunnel que le pare-feu lit. Les deux vus : un
// tunnel observé. Le service d'un annuaire seul : déclaré. Une unité seule :
// déduite.
func (b *builder) tunnels(result Map) []Tunnel {
	var seenByFirewall []Tunnel
	for _, firewall := range result.Firewalls {
		for _, iface := range firewall.Interfaces {
			if iface.Tunnel {
				seenByFirewall = append(seenByFirewall, Tunnel{Firewall: firewall.ID, Interface: iface.Name, Label: tunnelLabel(iface.Device)})
			}
		}
	}
	tunnels := []Tunnel{}
	next := 0
	for _, box := range result.Outside {
		for _, service := range box.Services {
			if !isTunnelService(service.Label) {
				continue
			}
			tunnel := Tunnel{Outside: box.ID, Service: service.ID, Label: tunnelLabel(service.Label), Proof: ProofDeduced}
			if service.ServiceID != "" {
				tunnel.Proof = ProofDeclared
			}
			if next < len(seenByFirewall) {
				tunnel.Firewall, tunnel.Interface, tunnel.Proof = seenByFirewall[next].Firewall, seenByFirewall[next].Interface, ProofObserved
				next++
			}
			tunnel.ID = "t:" + service.ID
			tunnels = append(tunnels, tunnel)
		}
	}
	for _, rest := range seenByFirewall[next:] {
		rest.ID = "t:" + rest.Firewall + ":" + rest.Interface
		rest.Proof = ProofObserved
		if len(result.Outside) == 1 {
			rest.Outside = result.Outside[0].ID
		}
		tunnels = append(tunnels, rest)
	}
	return tunnels
}

func isTunnelService(label string) bool {
	label = strings.ToLower(label)
	return strings.Contains(label, "wireguard") || strings.HasPrefix(label, "wg-quick") || strings.HasPrefix(label, "wg")
}

func tunnelLabel(name string) string {
	switch lower := strings.ToLower(name); {
	case strings.HasPrefix(lower, "wg") || strings.Contains(lower, "wireguard"):
		return "WireGuard"
	case strings.HasPrefix(lower, "ovpn") || strings.Contains(lower, "openvpn"):
		return "OpenVPN"
	case strings.HasPrefix(lower, "ipsec"):
		return "IPsec"
	}
	return strings.ToUpper(name[:min(len(name), 1)]) + name[min(len(name), 1):]
}

// entriesAndLinks rend les entrées d'Internet et les liens entre puces. Un
// lien dont une extrémité n'est pas une puce de la carte est laissé à la
// fiche du service ; une connexion venue d'Internet devient une entrée vue.
func (b *builder) entriesAndLinks() ([]Entry, []MapLink) {
	entries := map[string]Entry{}
	for _, entry := range b.entries {
		entries[entry.To] = entry
	}
	links := []MapLink{}
	seen := map[string]bool{}
	for _, found := range b.in.Links {
		from, to := serviceID(found.From.MachineID, found.From.Key), serviceID(found.To.MachineID, found.To.Key)
		if _, known := b.services[to]; !known || seen[found.ID] {
			continue
		}
		if strings.HasPrefix(found.From.Key, "internet:") {
			entry := entries[to]
			entry.To, entry.Observed = to, true
			if found.Port > 0 && !slices.Contains(entry.Ports, found.Port) {
				entry.Ports = append(entry.Ports, found.Port)
				slices.Sort(entry.Ports)
			}
			entries[to] = entry
			continue
		}
		if _, known := b.services[from]; !known || from == to {
			continue
		}
		seen[found.ID] = true
		links = append(links, MapLink{
			ID: found.ID, From: from, To: to, Nature: found.Nature, Proof: found.Proof, Signal: found.Signal,
			Port: found.Port, Stale: found.Stale, Proposed: found.Proposed, FirstSeen: found.FirstSeen, LastSeen: found.LastSeen,
		})
	}
	slices.SortFunc(links, func(a, c MapLink) int {
		return strings.Compare(a.From+"\x00"+a.To+"\x00"+strconv.Itoa(a.Port)+a.ID, c.From+"\x00"+c.To+"\x00"+strconv.Itoa(c.Port)+c.ID)
	})
	rendered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Ports == nil {
			entry.Ports = []int{}
		}
		rendered = append(rendered, entry)
	}
	slices.SortFunc(rendered, func(a, c Entry) int { return strings.Compare(a.To, c.To) })
	return rendered, links
}
