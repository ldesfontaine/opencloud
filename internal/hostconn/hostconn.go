package hostconn

import (
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

// Les bornes d'un rapport : l'agent les applique en lisant, le serveur les
// vérifie en recevant (un pair peut mentir).
const (
	MaxConnections = 512
	MaxNameSize    = 40
)

// ErrReportInvalid : le rapport sort de ce qu'un agent peut avoir relevé. Il
// est refusé en entier.
var ErrReportInvalid = errors.New("host connections report out of range")

// Origin dit qui joint le port. Trois mots, une liste fermée.
type Origin string

const (
	// OriginAccount : un compte de l'hôte (127.0.0.1 ou une adresse de la
	// machine) ; Name est son nom.
	OriginAccount Origin = "account"
	// OriginAddress : une machine du réseau local (adresse privée, lien local,
	// 100.64.0.0/10) ; Name est son adresse.
	OriginAddress Origin = "address"
	// OriginInternet : toute adresse publique, sans l'adresse : celle d'un
	// visiteur ne quitte pas la machine.
	OriginInternet Origin = "internet"
)

// Reason dit pourquoi la table n'a pas pu être lue.
type Reason string

// ReasonUnreadable : /proc/net/tcp manque (une machine qui n'est pas Linux) ou
// ne se lit pas.
const ReasonUnreadable Reason = "unreadable"

func Reasons() []Reason {
	return []Reason{ReasonUnreadable}
}

// Connection : quelqu'un joint ce port de l'hôte. Une ligne par port et par
// source, quel que soit le nombre de connexions.
type Connection struct {
	Port   int    `json:"port"`
	Origin Origin `json:"origin"`
	Name   string `json:"name,omitempty"`
}

// Report est le relevé d'une machine, remplacé à chaque fois. Available dit
// que la table a pu être lue ; sinon Reason le dit et Connections est vide.
type Report struct {
	Available bool   `json:"available"`
	Reason    Reason `json:"reason,omitempty"`
	// Truncated : plus de MaxConnections lignes, les suivantes (dans l'ordre
	// des ports) n'y sont pas.
	Truncated   bool         `json:"truncated,omitempty"`
	Connections []Connection `json:"connections"`
}

// accountPattern est celui d'un nom de compte Unix portable (useradd), sans
// espace ni caractère de contrôle.
var accountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)

// IsAccountName dit si un nom est celui d'un compte relevable.
func IsAccountName(name string) bool {
	return accountPattern.MatchString(name)
}

// IsLocal dit si l'adresse est celle de la machine elle-même, hors les
// adresses de ses interfaces que seul l'agent connaît.
func IsLocal(address netip.Addr) bool {
	return address.IsLoopback() || address.IsUnspecified()
}

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// IsLAN dit si l'adresse est celle du réseau de l'opérateur et non d'Internet :
// privée, lien local, ou l'espace partagé (100.64.0.0/10) que les réseaux
// superposés (Tailscale) utilisent.
func IsLAN(address netip.Addr) bool {
	return address.IsPrivate() || address.IsLinkLocalUnicast() || sharedAddressSpace.Contains(address)
}

// Validate refuse en bloc un rapport hors de ce qu'un agent peut avoir relevé.
func (r Report) Validate() error {
	if !r.Available {
		if !slices.Contains(Reasons(), r.Reason) || len(r.Connections) > 0 || r.Truncated {
			return ErrReportInvalid
		}
		return nil
	}
	if r.Reason != "" || len(r.Connections) > MaxConnections {
		return ErrReportInvalid
	}
	seen := make(map[Connection]bool, len(r.Connections))
	for _, connection := range r.Connections {
		if seen[connection] || !connection.valid() {
			return ErrReportInvalid
		}
		seen[connection] = true
	}
	return nil
}

func (c Connection) valid() bool {
	if c.Port < 1 || c.Port > 65535 || len(c.Name) > MaxNameSize {
		return false
	}
	switch c.Origin {
	case OriginAccount:
		return IsAccountName(c.Name)
	case OriginAddress:
		address, err := netip.ParseAddr(c.Name)
		// Une adresse écrite autrement que l'agent l'écrit (zone, forme mappée,
		// majuscules) n'est pas la sienne.
		return err == nil && address.Zone() == "" && address.String() == c.Name && !address.Is4In6() && !IsLocal(address) && IsLAN(address)
	case OriginInternet:
		return c.Name == ""
	default:
		return false
	}
}

// Sort range les lignes par port, puis par source : le même état donne le
// même rapport, que l'agent compare à celui d'avant pour savoir s'il a changé.
func Sort(connections []Connection) {
	slices.SortFunc(connections, func(a, b Connection) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		if a.Origin != b.Origin {
			return strings.Compare(string(a.Origin), string(b.Origin))
		}
		return strings.Compare(a.Name, b.Name)
	})
}
