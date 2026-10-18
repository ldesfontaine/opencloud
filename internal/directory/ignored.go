package directory

import (
	"slices"
	"strings"
	"time"
)

// IgnoreKind dit ce que la clé ignorée désigne parmi ce que la machine
// montre : tout un projet Compose, ou un conteneur seul par son nom. Jamais
// un identifiant Docker, que la recréation du conteneur ferait perdre.
type IgnoreKind string

const (
	IgnoreComposeProject IgnoreKind = "compose_project"
	IgnoreContainerName  IgnoreKind = "container_name"
)

func IgnoreKinds() []IgnoreKind {
	return []IgnoreKind{IgnoreComposeProject, IgnoreContainerName}
}

// IgnoredKey est un « présent non déclaré, et c'est voulu » : l'opérateur
// a dit qu'il n'y a rien à déclarer là.
type IgnoredKey struct {
	MachineID string
	Kind      IgnoreKind
	Value     string
	CreatedAt time.Time
}

// Complete retire les espaces qu'un copier-coller laisse autour de la
// machine et de la valeur, pour que la clé retrouve celle qu'on a ignorée.
func (k IgnoredKey) Complete() IgnoredKey {
	k.MachineID = strings.TrimSpace(k.MachineID)
	k.Value = strings.TrimSpace(k.Value)
	return k
}

// Validate refuse une clé sans machine, d'un genre inconnu, ou dont la
// valeur ne serait jamais celle d'un projet ou d'un conteneur. k doit être
// complétée.
func (k IgnoredKey) Validate() error {
	if k.MachineID == "" || !slices.Contains(IgnoreKinds(), k.Kind) {
		return ErrIgnoreInvalid
	}
	// Le motif d'un projet Compose est celui qu'un service déclaré lui donne.
	pattern := containerNamePattern
	if k.Kind == IgnoreComposeProject {
		pattern = namePattern
	}
	if !pattern.MatchString(k.Value) {
		return ErrIgnoreInvalid
	}
	return nil
}
