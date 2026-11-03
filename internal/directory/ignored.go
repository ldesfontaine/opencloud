package directory

import (
	"slices"
	"strings"
	"time"
)

// IgnoreKind dit ce que la clé ignorée désigne parmi ce que la machine
// montre : un service Compose (« projet/service »), tout un projet Compose,
// ou un conteneur seul par son nom. Jamais un identifiant Docker, que la
// recréation du conteneur ferait perdre.
type IgnoreKind string

const (
	IgnoreComposeService IgnoreKind = "compose_service"
	IgnoreComposeProject IgnoreKind = "compose_project"
	IgnoreContainerName  IgnoreKind = "container_name"
)

// ComposeServiceValue écrit la valeur d'une clé IgnoreComposeService.
func ComposeServiceValue(project, composeService string) string {
	return project + "/" + composeService
}

func IgnoreKinds() []IgnoreKind {
	return []IgnoreKind{IgnoreComposeService, IgnoreComposeProject, IgnoreContainerName}
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
	if !k.hasValidValue() {
		return ErrIgnoreInvalid
	}
	return nil
}

// Le motif d'un projet Compose est celui qu'un service déclaré lui donne ;
// celui d'un service Compose est le nom de l'unité qui le porte.
func (k IgnoredKey) hasValidValue() bool {
	switch k.Kind {
	case IgnoreComposeProject:
		return namePattern.MatchString(k.Value)
	case IgnoreComposeService:
		project, composeService, found := strings.Cut(k.Value, "/")
		return found && namePattern.MatchString(project) && unitNamePattern.MatchString(composeService)
	}
	return containerNamePattern.MatchString(k.Value)
}
