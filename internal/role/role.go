package role

import (
	"errors"
	"regexp"
	"slices"
)

// Name est un rôle de la liste fermée : un rôle que le serveur ne connaît
// pas est refusé, l'agent ne peut pas en inventer.
type Name string

const ProxmoxVE Name = "proxmox_ve"

func Names() []Name {
	return []Name{ProxmoxVE}
}

// Role est un rôle vu sur la machine, avec la version trouvée (vide si les
// signes ne la disent pas).
type Role struct {
	Name    Name   `json:"name"`
	Version string `json:"version"`
}

// Report est tout ce que l'agent voit à l'instant : un rôle absent du rapport
// est un rôle que la machine n'a plus.
type Report struct {
	Roles []Role `json:"roles"`
}

// MaxRoles borne un rapport : un pair peut mentir, la liste est fermée.
const MaxRoles = 8

// versionPattern : les caractères d'une version de paquet Debian. Le texte
// vient d'un pair et finit dans l'interface.
var versionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~:-]{0,63}$`)

// ErrReportInvalid : le rapport sort de ce qu'un agent peut avoir relevé. Il
// est refusé en entier.
var ErrReportInvalid = errors.New("roles report out of range")

// IsVersion dit si un texte peut passer pour une version.
func IsVersion(text string) bool {
	return versionPattern.MatchString(text)
}

func (r Report) Validate() error {
	if len(r.Roles) > MaxRoles {
		return ErrReportInvalid
	}
	seen := map[Name]bool{}
	for _, role := range r.Roles {
		if !slices.Contains(Names(), role.Name) || seen[role.Name] {
			return ErrReportInvalid
		}
		seen[role.Name] = true
		if role.Version != "" && !IsVersion(role.Version) {
			return ErrReportInvalid
		}
	}
	return nil
}
