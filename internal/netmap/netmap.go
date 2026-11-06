package netmap

// Les genres de machine : ceux de internal/machine, redits ici pour que le
// paquet ne dépende que de faits.
const (
	KindPhysical = "physical"
	KindVM       = "vm"
	KindLXC      = "lxc"
	KindVPS      = "vps"
	KindUnknown  = "unknown"
)

// D'où vient une zone : le nom d'une interface du pare-feu, un tag VLAN lu
// chez l'hôte, ou seulement le sous-réseau de l'adresse.
const (
	SourceFirewall = "firewall"
	SourceVLAN     = "vlan"
	SourceSubnet   = "subnet"
)

// L'état d'un service : ce que sa pastille dit.
const (
	StateRunning  = "running"
	StateDegraded = "degraded"
	StateStopped  = "stopped"
	StateFailed   = "failed"
	// StateMissing : l'annuaire le déclare, la machine ne le montre pas.
	StateMissing = "missing"
	StateUnknown = "unknown"
)

// Le genre d'un service : ce que la clé d'instance désigne.
const (
	ServiceCompose   = "compose"
	ServiceContainer = "container"
	ServiceSystemd   = "systemd"
)

// La preuve d'un tunnel, avec les mots des liens stockés.
const (
	ProofObserved = "observed"
	ProofDeclared = "declared"
	ProofDeduced  = "deduced"
)

// Machine est une machine enrôlée telle que la carte la lit.
type Machine struct {
	ID     string
	Name   string
	Kind   string
	Online bool
	// L'adresse par laquelle elle se présente, avec son port ou sans.
	Address string
	// L'hôte qui la porte (une autre machine), vide s'il n'est pas connu.
	HostID string
}

// Guest est un invité d'un hyperviseur, rapproché ou non d'une machine.
type Guest struct {
	VMID     int
	Kind     string
	Name     string
	Status   string
	Template bool
	Node     string
	// La machine enrôlée qu'il désigne, vide sinon.
	MachineID string
	VLAN      int
}

// Hypervisor est un hôte déclaré avec ce qu'il a relevé.
type Hypervisor struct {
	MachineID string
	Node      string
	Guests    []Guest
}

// Interface est celle d'un pare-feu : son nom pour l'opérateur, son appareil,
// l'adresse en notation CIDR et si elle est branchée.
type Interface struct {
	Name    string
	Device  string
	Role    string
	Address string
	Up      bool
}

// Firewall est un pare-feu déclaré et son dernier relevé.
type Firewall struct {
	ID         string
	Name       string
	Interfaces []Interface
}

// Replica est un conteneur d'une instance.
type Replica struct {
	State     string
	Unhealthy bool
}

// Instance est ce qui tourne sur une machine : un service Compose (ses
// répliques ensemble), un conteneur seul ou une unité systemd déclarée.
type Instance struct {
	MachineID string
	Key       string
	Kind      string
	// Les conteneurs de l'instance ; vide pour une unité systemd.
	Replicas []Replica
	// Pour une unité systemd : son état actif ; vide s'il n'est pas relevé.
	Active      string
	ContainerID string
	ServiceID   string
	ServiceName string
	// Les ports publiés sur toutes les interfaces de la machine.
	PublicPorts []int
	// Missing : déclaré, absent de ce que la machine montre.
	Missing bool
	// Vue servie depuis Internet par une connexion observée.
	SeenFromInternet bool
}

// Link est un lien stocké, déduit ou observé entre deux instances.
type Link struct {
	ID        string
	From      End
	To        End
	Nature    string
	Proof     string
	Signal    string
	Port      int
	Stale     bool
	Proposed  bool
	FirstSeen string
	LastSeen  string
}

// End est une extrémité de lien : une instance d'une machine.
type End struct {
	MachineID string
	Key       string
}

// Input est tout ce que la carte lit.
type Input struct {
	Machines    []Machine
	Hypervisors []Hypervisor
	Firewalls   []Firewall
	Instances   []Instance
	Links       []Link
}

// FoldFrom est le nombre de machines à partir duquel un hôte sans service en
// défaut se présente replié sur sa carte. En dessous, rien ne se replie.
const FoldFrom = 40

// Map est la carte, en trois régions : Internet (les entrées), l'extérieur
// (les machines hors de la maison) et la maison (un couloir par zone qui
// contient une machine).
type Map struct {
	Zones []Zone
	// Outside : les machines hors de la maison, à plat.
	Outside []Box
	// Home : un couloir par zone qui contient au moins une machine, dans
	// l'ordre des zones ; le couloir sans zone, s'il existe, ferme la liste.
	Home []Lane
	// EmptyZones : les zones (du pare-feu) où aucune machine ne tourne.
	EmptyZones []string
	// Folding : la carte est assez grande pour que les hôtes se replient.
	Folding   bool
	Firewalls []FirewallBox
	Tunnels   []Tunnel
	Entries   []Entry
	Links     []MapLink
}

// Lane est le couloir d'une zone : les machines qui y sont, les hôtes d'abord.
// Une zone vide (ZoneID) est celui des machines que rien ne place.
type Lane struct {
	ZoneID string
	Boxes  []Box
}

// Zone est une zone réseau déduite des données.
type Zone struct {
	ID     string
	Name   string
	Source string
	// Le sous-réseau et le tag quand les données les disent.
	CIDR string
	VLAN int
	// La teinte stable : un rang de 0 à ToneCount-1.
	Tone int
}

// Box est une machine. Un invité dit son hôte, un hôte dit combien il en porte :
// chacun a sa carte, dans le couloir de sa zone.
type Box struct {
	ID        string
	MachineID string
	Name      string
	Kind      string
	VMID      int
	Node      string
	// Vrai quand la machine est enrôlée (un agent la lit) ; faux pour un
	// invité que seul son hôte connaît.
	Managed bool
	Online  bool
	// L'état de l'invité chez son hôte ; vide pour une machine enrôlée.
	GuestStatus string
	Address     string
	ZoneID      string
	Services    []Service
	// L'hôte qui porte la machine (identifiant de sa carte) ; vide sinon.
	HostID string
	// Le nombre d'invités d'un hôte, toutes zones confondues.
	GuestCount int
	// Folded : l'hôte se présente replié, ses invités cachés.
	Folded bool
}

// Service est une puce : un service dans sa machine, avec son état.
type Service struct {
	ID          string
	Label       string
	Kind        string
	State       string
	Replicas    int
	ContainerID string
	ServiceID   string
}

// FirewallBox est le pare-feu dessiné entre les zones.
type FirewallBox struct {
	ID         string
	Name       string
	Interfaces []FirewallInterface
}

// FirewallInterface relie une interface à sa zone. Wan : adresse publique.
// Tunnel : un appareil de tunnel (WireGuard, OpenVPN, IPsec).
type FirewallInterface struct {
	Name    string
	Device  string
	Role    string
	CIDR    string
	Up      bool
	ZoneID  string
	Wan     bool
	Tunnel  bool
	Tunnels []string
}

// Tunnel est un lien chiffré entre le dehors et la maison.
type Tunnel struct {
	ID    string
	Label string
	Proof string
	// Le côté dehors : la boîte qui porte le tunnel, et sa puce quand on la
	// voit ; vides si rien dehors ne le dit.
	Outside string
	Service string
	// L'interface du pare-feu qui le reçoit : pare-feu et nom d'interface ;
	// vides si aucun pare-feu ne le voit.
	Firewall  string
	Interface string
}

// Entry relie Internet à un service qui reçoit du trafic : des ports publiés
// et, si une connexion d'Internet a été vue, le mot « observé ».
type Entry struct {
	To       string
	Ports    []int
	Observed bool
}

// MapLink est un lien entre deux puces.
type MapLink struct {
	ID        string
	From      string
	To        string
	Nature    string
	Proof     string
	Signal    string
	Port      int
	Stale     bool
	Proposed  bool
	FirstSeen string
	LastSeen  string
}
