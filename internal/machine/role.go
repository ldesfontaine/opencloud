package machine

import (
	"slices"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Role dit ce que l'opérateur fait de la machine. Il se choisit à l'ajout, là
// où il sait ce qu'il installe, et fixe le préréglage des droits et les
// modules que la machine porte. À ne pas confondre avec role.Name, ce que
// l'agent reconnaît de lui-même : celui-là se montre, celui-ci se décide.
type Role string

const (
	RoleGeneric Role = "generic"
	RoleDocker  Role = "docker"
	RoleProxmox Role = "proxmox"
)

// Roles rend la liste fermée, dans l'ordre où elle se propose : chaque rôle a
// son libellé dans les catalogues sous role.<nom>.
func Roles() []Role {
	return []Role{RoleGeneric, RoleDocker, RoleProxmox}
}

var ErrRoleUnknown = refusal.Invalid("machine.role_unknown", "unknown machine role")

// ParseRole lit un rôle ; le texte vide est le rôle générique, celui des
// demandes d'avant les rôles.
func ParseRole(text string) (Role, error) {
	if text == "" {
		return RoleGeneric, nil
	}
	if !slices.Contains(Roles(), Role(text)) {
		return "", ErrRoleUnknown
	}
	return Role(text), nil
}

// RolePreset rend les droits que le rôle coche d'avance. Ils se décochent :
// ce que l'agent peut faire reste ce que l'opérateur a validé.
func RolePreset(role Role) []Capability {
	switch role {
	case RoleDocker:
		return []Capability{{Name: CapabilityProbeLAN}, {Name: CapabilityDockerDeploy}, {Name: CapabilityDockerUpdate}}
	case RoleProxmox:
		return []Capability{{Name: CapabilityProbeLAN}, {Name: CapabilityProxmoxPower}, {Name: CapabilityProxmoxSnapshot}}
	default:
		return []Capability{{Name: CapabilityProbeLAN}}
	}
}
