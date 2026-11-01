package machine

import (
	"net"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/egress"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Kind dit où tourne la machine : sur du matériel, dans une VM, dans un
// conteneur LXC, ou sur un VPS. Il se déduit ; l'opérateur le corrige. À ne
// pas confondre avec Role, ce que l'opérateur en fait.
type Kind string

const (
	KindPhysical Kind = "physical"
	KindVM       Kind = "vm"
	KindLXC      Kind = "lxc"
	// KindVPS : virtualisée, sans hôte connu, et joignable par une adresse
	// publique ; ou dite VPS par l'opérateur. Une VM sur un réseau privé est
	// une VM : l'opérateur la loge chez lui.
	KindVPS     Kind = "vps"
	KindUnknown Kind = "unknown"
)

// Kinds rend la liste fermée, dans l'ordre où elle se propose : chaque type a
// son libellé dans les catalogues sous machine.kind_<nom>.
func Kinds() []Kind {
	return []Kind{KindPhysical, KindVM, KindLXC, KindVPS, KindUnknown}
}

var (
	ErrKindUnknown = refusal.Invalid("machine.kind_invalid", "unknown machine kind")
	ErrHostInvalid = refusal.Invalid("machine.host_invalid", "the host must be another existing machine, not one that runs on this machine")
)

// ParseKind lit un type ; le texte vide est « pas de correction ».
func ParseKind(text string) (Kind, error) {
	if text == "" {
		return "", nil
	}
	if !slices.Contains(Kinds(), Kind(text)) {
		return "", ErrKindUnknown
	}
	return Kind(text), nil
}

// KindOfVirt traduit la réponse de systemd-detect-virt en type rapporté par
// l'agent : « physical », « vm », « lxc » ou « unknown ». Jamais « vps » :
// virtualisée ou non, la machine ne sait pas si son hôte est connu.
// Un conteneur d'applications (Docker, Podman) n'est pas un type de machine :
// l'agent y tourne, mais ne dit pas où vit la machine.
func KindOfVirt(virt string) Kind {
	switch virt {
	case "":
		return KindUnknown
	case "none":
		return KindPhysical
	case "lxc", "lxc-libvirt", "openvz":
		return KindLXC
	case "docker", "podman", "systemd-nspawn", "rkt", "wsl", "proot", "pouch", "container-other":
		return KindUnknown
	}
	return KindVM
}

// KindOfGuest est le type d'un invité de Proxmox, quand l'hôte le dit.
func KindOfGuest(guestKind string) Kind {
	switch guestKind {
	case "qemu":
		return KindVM
	case "lxc":
		return KindLXC
	}
	return KindUnknown
}

// Placement est ce qu'il faut savoir d'une machine pour dire où elle tourne.
type Placement struct {
	// Reported : ce que l'agent a rapporté.
	Reported Kind
	// Override : la correction de l'opérateur ; vide sans correction.
	Override Kind
	// GuestKind : le type de l'invité que l'hôte rapproche de la machine ; vide
	// sans rapprochement. L'hôte le dit mieux que la machine.
	GuestKind Kind
	// HasHost : un hôte est connu, corrigé à la main ou rapproché.
	HasHost bool
	// Address : l'adresse par laquelle la machine se présente, avec son port
	// ou sans. Elle seule distingue un VPS (adresse publique) d'une VM chez soi.
	Address string
}

// IsPublicAddress dit si l'adresse est routable sur Internet : ni privée
// (RFC 1918), ni partagée (RFC 6598), ni ULA, ni boucle, ni lien-local. Un nom
// ou un texte illisible n'est pas public : on ne devine pas un VPS.
func IsPublicAddress(address string) bool {
	host := address
	if withoutPort, _, err := net.SplitHostPort(address); err == nil {
		host = withoutPort
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if egress.Private(ip) || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return true
}

// EffectiveKind choisit : la correction de l'opérateur, puis ce que l'hôte dit
// de son invité, puis ce que l'agent a rapporté. Une machine virtualisée sans
// hôte connu est un VPS si son adresse est publique, sinon une VM.
func EffectiveKind(placement Placement) Kind {
	switch {
	case placement.Override != "":
		return placement.Override
	case placement.GuestKind != "" && placement.GuestKind != KindUnknown:
		return placement.GuestKind
	case placement.Reported == KindVM && !placement.HasHost && IsPublicAddress(placement.Address):
		return KindVPS
	}
	if placement.Reported == "" {
		return KindUnknown
	}
	return placement.Reported
}
