package link

import (
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
	// NatureRoute et NatureTraffic : réservées aux routes publiques et aux
	// connexions vues, qui n'ont pas encore de source.
	NatureRoute   Nature = "route"
	NatureTraffic Nature = "traffic"
)

func Natures() []Nature {
	return []Nature{NatureOrder, NatureConfig, NatureRoute, NatureTraffic}
}

// Proof : d'où vient le lien. Un mot, jamais la couleur seule à l'écran.
type Proof string

const (
	// ProofDeclared : écrit, par l'opérateur ou par la configuration d'un outil.
	ProofDeclared Proof = "declared"
	// ProofDeduced : calculé à partir d'un autre fait. Pas encore écrit.
	ProofDeduced Proof = "deduced"
	// ProofObserved : une connexion ou un état lu. Pas encore écrit.
	ProofObserved Proof = "observed"
)

func Proofs() []Proof {
	return []Proof{ProofDeclared, ProofDeduced, ProofObserved}
}

// Signal : le signal précis derrière la preuve.
type Signal string

const (
	SignalDependsOn     Signal = "depends_on"
	SignalContainerLink Signal = "container_link"
	SignalOperator      Signal = "operator"
)

func Signals() []Signal {
	return []Signal{SignalDependsOn, SignalContainerLink, SignalOperator}
}

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
)

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
	for _, prefix := range []string{composePrefix, containerPrefix, systemdPrefix} {
		if rest, found := strings.CutPrefix(key, prefix); found {
			return rest
		}
	}
	return key
}

// validKey : un préfixe connu, un nom, rien d'invisible.
func validKey(key string) bool {
	if len(key) == 0 || len(key) > MaxKeyLength || LabelOf(key) == key || LabelOf(key) == "" {
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

// Stale : un lien d'un outil qu'on ne revoit plus. Un lien écrit par
// l'opérateur ne vieillit pas : personne ne le « revoit ».
func (l Link) Stale(now time.Time) bool {
	return l.Signal != SignalOperator && now.Sub(l.LastSeen) > StaleAfter
}

// Removable : seul l'opérateur retire ce qu'il a écrit. Un lien tiré de
// depends_on se retire du fichier Compose, sinon il revient au rapport suivant.
func (l Link) Removable() bool {
	return l.Signal == SignalOperator
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

// Order : un lien d'ordre lu dans la configuration d'une machine. Les deux
// extrémités sont sur elle.
type Order struct {
	From   string
	To     string
	Signal Signal
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
