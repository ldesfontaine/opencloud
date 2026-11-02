package link

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Nature : ce que le lien dit.
type Nature string

const (
	// NatureOrder : B doit démarrer avant A (depends_on, --link).
	NatureOrder Nature = "order"
	// NatureConfig : A est configuré pour joindre B. L'opérateur qui écrit un
	// lien à la main dit cela.
	NatureConfig Nature = "config"
	// NatureRoute : le proxy envoie vers le service ce qui arrive à son adresse
	// publique. Déduite (SignalPublicURL), puis confirmée par l'opérateur.
	NatureRoute Nature = "route"
	// NatureTraffic : une connexion vue. Sa source est un compte ou une adresse
	// de l'hôte, jamais un service (la table de l'hôte n'attribue pas de
	// processus).
	NatureTraffic Nature = "traffic"
)

// Proof : d'où vient le lien. Un mot, jamais la couleur seule à l'écran.
type Proof string

const (
	// ProofDeclared : écrit, par l'opérateur ou par la configuration d'un outil.
	ProofDeclared Proof = "declared"
	// ProofDeduced : calculé à partir d'un autre fait. Une route déduite se
	// recalcule à chaque lecture et ne s'écrit pas (Link.Proposed) ; la trace
	// d'une route que l'opérateur a écartée (Link.Dismissed) et le lien lu dans
	// l'environnement d'un conteneur (SignalEnvironment) sont les deux seules
	// lignes « déduit » qui existent en base.
	ProofDeduced Proof = "deduced"
	// ProofObserved : une connexion vue dans la table de l'hôte
	// (SignalHostConnection). Gardé en mémoire par hostconn.Service, jamais en
	// base : la table des liens ne connaît pas ce signal.
	ProofObserved Proof = "observed"
)

// Signal : le signal précis derrière la preuve.
type Signal string

const (
	SignalDependsOn     Signal = "depends_on"
	SignalContainerLink Signal = "container_link"
	SignalOperator      Signal = "operator"
	// SignalPublicURL : le service a une adresse publique et un service fournit
	// la capacité proxy. Jamais écrit : la table n'en connaît pas la valeur.
	SignalPublicURL Signal = "public_url"
	// SignalEnvironment : l'environnement d'un conteneur nomme l'hôte d'une
	// autre instance de la machine. Écrit à chaque inventaire, jamais la
	// variable ni sa valeur (étude services-modèle §2.4).
	SignalEnvironment Signal = "environment"
	// SignalHostConnection : une connexion vue dans la table de l'hôte vers un
	// port que l'instance publie ou déclare (étude services-modèle §2.3,
	// signal 9). Jamais écrit : les sources sont dans hostconn.Service.
	SignalHostConnection Signal = "host_connection"
)

// StaleAfter : un lien déclaré par un outil et non revu depuis ce délai est
// « ancien ». Il n'est jamais supprimé pour cela : un instantané manque les
// liens rares.
const StaleAfter = 7 * 24 * time.Hour

const (
	// MaxKeyLength borne une clé : un nom de conteneur, d'unité ou de projet.
	MaxKeyLength = 300
	// MaxLinksPerMachine borne ce qu'une machine peut porter : un rapport
	// d'agent ne remplit pas la base.
	MaxLinksPerMachine = 2000
)

// Les préfixes de clé disent le genre d'instance.
const (
	composePrefix   = "compose:"
	containerPrefix = "container:"
	systemdPrefix   = "systemd:"
	// Les sources d'une connexion vue ne sont pas des instances : un compte de
	// l'hôte, une machine du réseau local, ou Internet.
	accountPrefix  = "account:"
	addressPrefix  = "address:"
	internetPrefix = "internet:"
)

// Kind : le genre de ce qu'une clé désigne. Le front en tire le libellé et
// l'adresse où aller ; il ne lit pas le préfixe lui-même.
type Kind string

const (
	KindCompose   Kind = "compose"
	KindContainer Kind = "container"
	KindSystemd   Kind = "systemd"
	KindAccount   Kind = "account"
	KindAddress   Kind = "address"
	KindInternet  Kind = "internet"
)

var kindPrefixes = []struct {
	prefix string
	kind   Kind
}{
	{composePrefix, KindCompose}, {containerPrefix, KindContainer}, {systemdPrefix, KindSystemd},
	{accountPrefix, KindAccount}, {addressPrefix, KindAddress}, {internetPrefix, KindInternet},
}

// KindOf rend le genre d'une clé ; vide si le préfixe est inconnu.
func KindOf(key string) Kind {
	for _, known := range kindPrefixes {
		if strings.HasPrefix(key, known.prefix) {
			return known.kind
		}
	}
	return ""
}

// AccountKey : un compte de l'hôte qui se connecte.
func AccountKey(name string) string {
	return accountPrefix + name
}

// AddressKey : une machine du réseau local qui se connecte.
func AddressKey(address string) string {
	return addressPrefix + address
}

// InternetKey : tout ce qui vient d'Internet, sans adresse.
func InternetKey() string {
	return internetPrefix + "public"
}

// ComposeKey : un service d'un projet Compose, répliques comprises.
func ComposeKey(project, service string) string {
	return composePrefix + project + "/" + service
}

// ContainerKey : un conteneur seul, par son nom.
func ContainerKey(name string) string {
	return containerPrefix + name
}

// SystemdKey : une unité systemd, par son nom.
func SystemdKey(unit string) string {
	return systemdPrefix + unit
}

// LabelOf rend le nom qu'on lit d'une clé : sans son préfixe.
func LabelOf(key string) string {
	for _, known := range kindPrefixes {
		if rest, found := strings.CutPrefix(key, known.prefix); found {
			return rest
		}
	}
	return key
}

// validKey : une instance (compose, conteneur, unité), un nom, rien
// d'invisible. Un compte ou une adresse n'est pas une instance : l'opérateur
// ne l'écrit pas en lien.
func validKey(key string) bool {
	if len(key) == 0 || len(key) > MaxKeyLength || LabelOf(key) == key || LabelOf(key) == "" {
		return false
	}
	switch KindOf(key) {
	case KindAccount, KindAddress, KindInternet:
		return false
	}
	return !strings.ContainsFunc(key, func(character rune) bool { return character < ' ' || character == 0x7f })
}

// Endpoint : une instance sur une machine.
type Endpoint struct {
	MachineID string
	Key       string
}

// Link : A vers B, avec sa preuve et sa fraîcheur.
type Link struct {
	ID        string
	From      Endpoint
	To        Endpoint
	Nature    Nature
	Proof     Proof
	Signal    Signal
	Port      int
	FirstSeen time.Time
	LastSeen  time.Time
}

// Stale : un lien qu'on ne revoit plus. Un lien écrit par l'opérateur ne
// vieillit pas : personne ne le « revoit ». Une route proposée non plus : elle
// est refaite à chaque lecture, elle n'a pas de dernière vue.
func (l Link) Stale(now time.Time) bool {
	return !l.Proposed() && l.Signal != SignalOperator && now.Sub(l.LastSeen) > StaleAfter
}

// Proposed : une route déduite que l'opérateur n'a pas encore décidée. Elle
// n'a pas de ligne en base, seulement un identifiant refait de la paire.
func (l Link) Proposed() bool {
	return strings.HasPrefix(l.ID, DeducedPrefix)
}

// Removable : seul l'opérateur retire ce qu'il a écrit. Un lien tiré de
// depends_on se retire du fichier Compose, sinon il revient au rapport suivant.
func (l Link) Removable() bool {
	return l.Signal == SignalOperator && !l.Dismissed()
}

// Dismissed : la trace d'un lien déduit que l'opérateur a écarté. Il ne
// revient pas tant que l'opérateur ne le rétablit pas, même si la déduction
// qui l'avait fait naître change.
func (l Link) Dismissed() bool {
	return l.Proof == ProofDeduced && l.Signal == SignalOperator
}

// Observed : une connexion vue dans la table de l'hôte. Elle n'a pas de ligne
// en base, seulement un identifiant refait de la paire et du port.
func (l Link) Observed() bool {
	return strings.HasPrefix(l.ID, ObservedPrefix)
}

// Touches dit si une extrémité du lien est sur la machine.
func (l Link) Touches(machineID string) bool {
	return l.From.MachineID == machineID || l.To.MachineID == machineID
}

// Definition : ce que l'opérateur donne pour écrire un lien.
type Definition struct {
	From Endpoint
	To   Endpoint
	Port int
}

// DeducedID : l'identifiant d'un lien déduit. Il n'a pas de ligne en base : il
// se refait de la paire, donc il est le même à chaque lecture.
func DeducedID(from, to Endpoint, nature Nature) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{from.MachineID, from.Key, to.MachineID, to.Key, string(nature)}, "\x00")))
	return DeducedPrefix + hex.EncodeToString(sum[:8])
}

// Sighting : une source a été vue joindre un port de l'hôte. L'instance qui
// publie ce port se trouve à la lecture, pas à l'écriture : un conteneur
// recréé ou un port déclaré après coup rattache les mêmes vues.
type Sighting struct {
	Source    string
	Port      int
	FirstSeen time.Time
	LastSeen  time.Time
}

// ObservedID : l'identifiant d'un lien observé, refait de ses extrémités et du
// port.
func ObservedID(from, to Endpoint, port int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{from.MachineID, from.Key, to.MachineID, to.Key, string(NatureTraffic), strconv.Itoa(port)}, "\x00")))
	return ObservedPrefix + hex.EncodeToString(sum[:8])
}

// ObservedPrefix précède l'identifiant d'un lien observé.
const ObservedPrefix = "obs_"

// DeducedPrefix précède l'identifiant d'un lien déduit.
const DeducedPrefix = "ded_"

// Seen : un lien lu dans la configuration des conteneurs d'une machine. Les
// deux extrémités sont sur elle. Le signal dit le reste : sa nature et sa
// preuve (Seen.Kind).
type Seen struct {
	From   string
	To     string
	Signal Signal
	// Port est celui que la configuration nomme ; 0 : aucun.
	Port int
}

// Kind rend la nature et la preuve que le signal porte. Un signal qu'un
// rapport ne peut pas écrire (l'opérateur, l'adresse publique) n'en a pas.
func (s Seen) Kind() (Nature, Proof, bool) {
	switch s.Signal {
	case SignalDependsOn, SignalContainerLink:
		return NatureOrder, ProofDeclared, true
	case SignalEnvironment:
		return NatureConfig, ProofDeduced, true
	default:
		return "", "", false
	}
}

// Les refus, un par cause.
var (
	ErrNotFound     = refusal.Invalid("link.not_found", "link not found")
	ErrInvalidKey   = refusal.Invalid("link.invalid_key", "link end is not a valid instance key")
	ErrSelf         = refusal.Invalid("link.self", "link joins an instance to itself")
	ErrInvalidPort  = refusal.Invalid("link.invalid_port", "link port must be between 1 and 65535")
	ErrMachine      = refusal.Invalid("link.machine_invalid", "link machine does not exist")
	ErrDuplicate    = refusal.Conflict("link.duplicate", "link already exists")
	ErrNotRemovable = refusal.Conflict("link.not_removable", "link comes from a tool configuration")
	ErrTooMany      = refusal.Conflict("link.too_many", "machine reached its link limit")
)

// Validate refuse une définition avant toute écriture.
func (d Definition) Validate() error {
	if !validKey(d.From.Key) || !validKey(d.To.Key) || d.From.MachineID == "" || d.To.MachineID == "" {
		return ErrInvalidKey
	}
	if d.From == d.To {
		return ErrSelf
	}
	if d.Port < 0 || d.Port > 65535 {
		return ErrInvalidPort
	}
	return nil
}
