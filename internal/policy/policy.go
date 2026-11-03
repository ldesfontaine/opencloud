package policy

// Les genres de zone. Une zone « network » est un réseau que le pare-feu
// sépare des autres ; « self » est le pare-feu lui-même ; « internet » est ce
// qui n'appartient à aucune zone connue.
const (
	ZoneNetwork  = "network"
	ZoneSelf     = "self"
	ZoneInternet = "internet"
)

// Les genres d'objet : ce qu'une règle désigne, côté source ou destination.
const (
	// ObjectAny : n'importe qui.
	ObjectAny = "any"
	// ObjectSelf : le pare-feu lui-même.
	ObjectSelf = "self"
	// ObjectZone : tout le réseau d'une zone.
	ObjectZone = "zone"
	// ObjectHost : une adresse.
	ObjectHost = "host"
	// ObjectNetwork : un bloc d'adresses (CIDR).
	ObjectNetwork = "network"
	// ObjectNamed : un groupe que le pare-feu nomme (alias) et que le
	// connecteur ne sait pas déplier : sa zone reste inconnue.
	ObjectNamed = "named"
)

// Les effets d'une règle.
const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)

// Les valeurs spéciales de Endpoint.Zone.
const (
	// AllZones : toute zone (une règle « vers n'importe où »).
	AllZones = "*"
	// UnknownZone : la règle ne dit pas où elle s'applique (règle flottante,
	// groupe nommé) ; la grille la met à part au lieu de la deviner.
	UnknownZone = ""
)

// Zone est un ensemble d'appareils que le pare-feu traite d'un bloc.
type Zone struct {
	ID string
	// Label : le nom que le pare-feu lui donne ; vide pour une zone que le
	// connecteur a ajoutée (self, internet), que l'écran nomme dans sa langue.
	Label   string
	Kind    string
	Network string
	IsUp    bool
}

// Object est ce qu'une règle désigne. Son identifiant est stable : deux
// règles qui désignent la même chose partagent l'objet.
type Object struct {
	ID    string
	Kind  string
	Label string
	// Zone : où l'objet se trouve ; UnknownZone, ou AllZones pour « any ».
	Zone string
	// Negated : « tout sauf » l'objet.
	Negated bool
}

// Service est le « quoi » d'une règle : un protocole et des ports.
type Service struct {
	// Vide : tous les protocoles.
	Protocol string
	// Port de destination ; vide : tous.
	Port string
	// Port de source ; vide : tous. Rare, mais une règle qui en pose un ne
	// se lit pas comme les autres.
	SourcePort string
}

// Endpoint est un bout de règle : la zone où il se trouve et l'objet désigné.
type Endpoint struct {
	Zone   string
	Object string
	// Except : avec Zone = AllZones, la zone que « tout sauf » écarte.
	Except string
}

// Rule est « de → vers : quoi, pourquoi ». Une règle qui s'applique à
// plusieurs zones de départ est rendue une fois par zone.
type Rule struct {
	// Ref : l'identifiant de la règle chez le pare-feu.
	Ref     string
	From    Endpoint
	To      Endpoint
	What    Service
	Why     string
	Effect  string
	Enabled bool
}

// Model est tout ce qu'on sait de ce que le réseau autorise.
type Model struct {
	Zones   []Zone
	Objects []Object
	Rules   []Rule
}

// ObjectByID rend l'objet d'un identifiant ; faux s'il n'existe pas.
func (m Model) ObjectByID(id string) (Object, bool) {
	for _, object := range m.Objects {
		if object.ID == id {
			return object, true
		}
	}
	return Object{}, false
}
